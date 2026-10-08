package awards

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Лауреаты из Wikidata: утверждения «награда» (P166) с годом (квалификатор P585).
// Награду записывают то на работу (фильм, книгу), то на человека с квалификатором
// «за работу» (P1686), то на человека без него. Книжная премия: работа — сама
// награждённая работа или «за работу» у человека; человек без работы — премия
// автору, если в тот же год нет его награждённой работы. Кинопремия: фильм —
// награждённая работа или «за работу» у режиссёра/продюсера; показываем только
// экранизации книг каталога (сопоставление в Match по QID фильма). Книжная
// премия позже публикации больше чем на maxAwardLag лет — ошибка данных
// («Человек-невидимка» Эллисона с NBA 2020 вместо 1953) — пропускается.

const sparqlEndpoint = "https://query.wikidata.org/sparql"

// wdUserAgent — Wikidata Query Service без User-Agent отвечает 403.
const wdUserAgent = "skriptes (https://github.com/alshstf/skriptes; awards)"

// wikidataQuery — утверждения о награде с годом, подписями и авторами.
func wikidataQuery(qid string) string {
	return `SELECT ?item ?human ?t ?forw ?iru ?ien ?fru ?fen ?auth ?aru ?aen ?ipub ?fpub WHERE {
  ?item p:P166 ?st . ?st ps:P166 wd:` + qid + ` ; pq:P585 ?t .
  BIND(EXISTS { ?item wdt:P31 wd:Q5 } AS ?human)
  OPTIONAL { ?st pq:P1686 ?forw .
    OPTIONAL { ?forw rdfs:label ?fru FILTER(LANG(?fru) = "ru") }
    OPTIONAL { ?forw rdfs:label ?fen FILTER(LANG(?fen) = "en") }
    OPTIONAL { ?forw wdt:P577 ?fpub } }
  OPTIONAL { ?item wdt:P577 ?ipub }
  OPTIONAL { ?item rdfs:label ?iru FILTER(LANG(?iru) = "ru") }
  OPTIONAL { ?item rdfs:label ?ien FILTER(LANG(?ien) = "en") }
  OPTIONAL { ?item wdt:P50 ?auth .
    OPTIONAL { ?auth rdfs:label ?aru FILTER(LANG(?aru) = "ru") }
    OPTIONAL { ?auth rdfs:label ?aen FILTER(LANG(?aen) = "en") } }
}`
}

// sparql — строки результата как «переменная → значение».
func (s *Syncer) sparql(ctx context.Context, query string) ([]map[string]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.sparqlURL+"?query="+url.QueryEscape(query), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/sparql-results+json")
	req.Header.Set("User-Agent", wdUserAgent)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wikidata sparql: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("wikidata sparql: status %d", resp.StatusCode)
	}
	var d struct {
		Results struct {
			Bindings []map[string]struct {
				Value string `json:"value"`
			} `json:"bindings"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return nil, fmt.Errorf("wikidata sparql: decode: %w", err)
	}
	out := make([]map[string]string, 0, len(d.Results.Bindings))
	for _, b := range d.Results.Bindings {
		row := make(map[string]string, len(b))
		for k, v := range b {
			row[k] = v.Value
		}
		out = append(out, row)
	}
	return out, nil
}

func (s *Syncer) fetchWikidata(ctx context.Context, a Award) ([]win, error) {
	var out []win
	for i, it := range a.Wikidata {
		rows, err := s.sparql(ctx, wikidataQuery(it.QID))
		if err != nil {
			return nil, fmt.Errorf("award %s (%s): %w", a.Key, it.QID, err)
		}
		out = append(out, wikidataWins(a, it, i, rows)...)
	}
	return out, nil
}

// wdEntity — QID из адреса сущности.
func wdEntity(uri string) string {
	if i := strings.LastIndexByte(uri, '/'); i >= 0 {
		return uri[i+1:]
	}
	return uri
}

// maxAwardLag — на сколько лет книжная премия может быть позже публикации.
const maxAwardLag = 5

// wdYear — год из даты Wikidata («1937-05-03T00:00:00Z»); 0 — нет даты.
func wdYear(t string) int {
	y, err := strconv.Atoi(strings.SplitN(strings.TrimPrefix(t, "+"), "-", 2)[0])
	if err != nil || y <= 0 {
		return 0
	}
	return y
}

// wdWork — награждённая работа (книга или фильм) за год.
type wdWork struct {
	qid, ru, en string
	pub         int             // самый ранний год публикации (P577), 0 — неизвестен
	authors     []string        // подписи авторов в порядке появления
	authorQIDs  map[string]bool // авторы-люди (P50 или награждённый человек)
}

// notePub — учесть год публикации (берётся самый ранний).
func (w *wdWork) notePub(y int) {
	if y > 0 && (w.pub == 0 || y < w.pub) {
		w.pub = y
	}
}

// wikidataWins — лауреаты одного элемента премии из строк SPARQL.
func wikidataWins(a Award, it WikidataItem, order int, rows []map[string]string) []win {
	type yk struct {
		year int
		qid  string
	}
	works := map[yk]*wdWork{}
	type person struct {
		year     int
		qid      string
		ru, en   string
		withWork bool
	}
	people := map[yk]*person{}
	addWork := func(year int, qid, ru, en string) *wdWork {
		k := yk{year, qid}
		w := works[k]
		if w == nil {
			w = &wdWork{qid: qid, ru: ru, en: en, authorQIDs: map[string]bool{}}
			works[k] = w
		}
		return w
	}
	addAuthor := func(w *wdWork, qid, name string) {
		if name == "" || w.authorQIDs[qid] {
			return
		}
		w.authorQIDs[qid] = true
		w.authors = append(w.authors, name)
	}
	for _, r := range rows {
		year := wdYear(r["t"])
		if year == 0 {
			continue
		}
		item := wdEntity(r["item"])
		human := r["human"] == "true" || r["human"] == "1"
		switch {
		case !human:
			w := addWork(year, item, r["iru"], r["ien"])
			w.notePub(wdYear(r["ipub"]))
			if q := wdEntity(r["auth"]); q != "" {
				addAuthor(w, q, firstNonEmpty(r["aru"], r["aen"]))
			}
		case r["forw"] != "":
			w := addWork(year, wdEntity(r["forw"]), r["fru"], r["fen"])
			w.notePub(wdYear(r["fpub"]))
			if !a.Film {
				addAuthor(w, item, firstNonEmpty(r["iru"], r["ien"]))
			}
			fallthrough
		default:
			k := yk{year, item}
			p := people[k]
			if p == nil {
				p = &person{year: year, qid: item, ru: r["iru"], en: r["ien"]}
				people[k] = p
			}
			p.withWork = p.withWork || r["forw"] != ""
		}
	}
	var out []win
	for k, w := range works {
		title := firstNonEmpty(w.ru, w.en)
		if title == "" || !a.Film && w.pub > 0 && k.year-w.pub > maxAwardLag {
			continue
		}
		x := win{year: k.year, nomination: it.Nomination, nomOrder: order, kind: "work", title: title,
			author: strings.Join(w.authors, ", "), link: w.qid, ref: fmt.Sprintf("%s/%s/%d/%s", a.Key, it.QID, k.year, w.qid)}
		if w.ru != "" && w.en != "" && w.en != w.ru {
			x.origTitle = w.en
		}
		if !a.Film && x.author == "" {
			continue // книга без автора не сопоставится и мало что скажет
		}
		out = append(out, x)
	}
	if !a.Film {
		// Человек без работы — премия автору, если в тот год нет его награждённой работы.
		for k, p := range people {
			if p.withWork {
				continue
			}
			covered := false
			for wk, w := range works {
				if wk.year == k.year && w.authorQIDs[p.qid] {
					covered = true
					break
				}
			}
			name := firstNonEmpty(p.ru, p.en)
			if covered || name == "" {
				continue
			}
			out = append(out, win{year: k.year, nomination: it.Nomination, nomOrder: order, kind: "author", author: name,
				link: p.qid, ref: fmt.Sprintf("%s/%s/%d/%s", a.Key, it.QID, k.year, p.qid)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ref < out[j].ref })
	return out
}
