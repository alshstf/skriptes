// Package gen — офлайн-генератор датасета био-таймлайна (cmd/biogen):
// источники (Wikidata-скелет + текст Википедии) → извлечение вех моделью по
// строгой схеме → автопроверка по цитатам и фактам → отчёт ревью → файл
// датасета. Runtime приложения этот пакет не использует.
package gen

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const userAgent = "skriptes-biogen/0.1 (https://github.com/alshstf/skriptes)"

// Fact — одна строка Wikidata-скелета: свойство, подпись значения, даты.
type Fact struct {
	Prop  string `json:"prop"`            // "P26" (брак), "P40" (ребёнок), …
	Label string `json:"label"`           // подпись значения
	QID   string `json:"qid,omitempty"`   // QID значения
	Date  string `json:"date,omitempty"`  // точечная дата (P569/P570/P585 значения)
	Start string `json:"start,omitempty"` // P580
	End   string `json:"end,omitempty"`   // P582
	// Для родных: их даты жизни — сверка утрат.
	Born string `json:"born,omitempty"`
	Died string `json:"died,omitempty"`
}

// Article — текст статьи Википедии после обрезки небиографических разделов.
type Article struct {
	Lang  string `json:"lang"`
	Title string `json:"title"`
	URL   string `json:"url"`
	Rev   int64  `json:"rev"`
	Text  string `json:"text"`
}

// Sources — всё, что видит модель и проверка.
type Sources struct {
	QID      string    `json:"qid"`
	Label    string    `json:"label"`
	Born     string    `json:"born"`
	Died     string    `json:"died,omitempty"`
	Facts    []Fact    `json:"facts"`
	Articles []Article `json:"articles"`
}

// Fetcher — HTTP-доступ к Wikidata и Википедии. BaseURL-ы переопределяются
// тестами (httptest).
type Fetcher struct {
	Client    *http.Client
	SPARQLURL string // https://query.wikidata.org/sparql
	WikiAPI   func(lang string) string
	// WikidataAPI — https://www.wikidata.org/w/api.php (sitelinks); nil = прод.
	WikidataAPI func() string
	// MaxTextRunes — кап текста одной статьи после обрезки (модель видит не больше).
	MaxTextRunes int
}

// NewFetcher — продовые адреса.
func NewFetcher(c *http.Client) *Fetcher {
	return &Fetcher{
		Client:       c,
		SPARQLURL:    "https://query.wikidata.org/sparql",
		WikiAPI:      func(lang string) string { return "https://" + lang + ".wikipedia.org/w/api.php" },
		MaxTextRunes: 90_000,
	}
}

// Fetch — источники по QID: скелет фактов + статьи (ru, при короткой ru — и en).
func (f *Fetcher) Fetch(ctx context.Context, qid string) (*Sources, error) {
	src := &Sources{QID: qid}
	if err := f.fetchFacts(ctx, src); err != nil {
		return nil, fmt.Errorf("wikidata: %w", err)
	}
	ru, err := f.fetchArticle(ctx, "ru", qid)
	if err != nil {
		return nil, fmt.Errorf("ruwiki: %w", err)
	}
	if ru != nil {
		src.Articles = append(src.Articles, *ru)
	}
	// Короткая русская статья (< 10 тыс. символов) — добираем английскую.
	if ru == nil || utf8.RuneCountInString(ru.Text) < 10_000 {
		en, err := f.fetchArticle(ctx, "en", qid)
		if err != nil {
			return nil, fmt.Errorf("enwiki: %w", err)
		}
		if en != nil {
			src.Articles = append(src.Articles, *en)
		}
	}
	if len(src.Articles) == 0 {
		return nil, fmt.Errorf("нет статьи ни в ru, ни в en Википедии")
	}
	return src, nil
}

// sparqlFacts — один UNION-запрос: жизнь, браки, дети, родители, места
// жительства, учёба, награды, главные работы. Подписи — ru, фолбэк en.
const sparqlFacts = `
SELECT ?prop ?itemLabel ?v ?vLabel ?date ?start ?end ?vBorn ?vDied WHERE {
  VALUES ?item { wd:%s }
  {
    VALUES ?prop { "P569" "P570" }
    ?item p:P569|p:P570 ?st .
    ?st ps:P569|ps:P570 ?date .
    BIND(IF(EXISTS { ?st ps:P569 ?date }, "P569", "P570") AS ?prop)
    OPTIONAL { ?item wdt:P19 ?v . FILTER(?prop = "P569") }
    OPTIONAL { ?item wdt:P20 ?v . FILTER(?prop = "P570") }
  } UNION {
    BIND("P26" AS ?prop) ?item p:P26 ?st . ?st ps:P26 ?v .
    OPTIONAL { ?st pq:P580 ?start } OPTIONAL { ?st pq:P582 ?end }
  } UNION {
    BIND("P40" AS ?prop) ?item wdt:P40 ?v .
    OPTIONAL { ?v wdt:P569 ?vBorn } OPTIONAL { ?v wdt:P570 ?vDied }
  } UNION {
    VALUES ?prop { "P22" "P25" "P3373" }
    ?item ?pd ?v . ?pp wikibase:directClaim ?pd . BIND(STRAFTER(STR(?pp), "entity/") AS ?prop)
    FILTER(?pd IN (wdt:P22, wdt:P25, wdt:P3373))
    OPTIONAL { ?v wdt:P569 ?vBorn } OPTIONAL { ?v wdt:P570 ?vDied }
  } UNION {
    BIND("P551" AS ?prop) ?item p:P551 ?st . ?st ps:P551 ?v .
    OPTIONAL { ?st pq:P580 ?start } OPTIONAL { ?st pq:P582 ?end }
  } UNION {
    BIND("P69" AS ?prop) ?item p:P69 ?st . ?st ps:P69 ?v .
    OPTIONAL { ?st pq:P580 ?start } OPTIONAL { ?st pq:P582 ?end }
  } UNION {
    BIND("P166" AS ?prop) ?item p:P166 ?st . ?st ps:P166 ?v .
    OPTIONAL { ?st pq:P585 ?date }
  } UNION {
    BIND("P800" AS ?prop) ?item wdt:P800 ?v .
    OPTIONAL { ?v wdt:P571 ?date }
  }
  SERVICE wikibase:label { bd:serviceParam wikibase:language "ru,en". }
}`

type sparqlValue struct {
	Value string `json:"value"`
}

func (f *Fetcher) fetchFacts(ctx context.Context, src *Sources) error {
	q := fmt.Sprintf(sparqlFacts, src.QID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.SPARQLURL+"?format=json&query="+url.QueryEscape(q), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/sparql-results+json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := f.Client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("sparql: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Results struct {
			Bindings []map[string]sparqlValue `json:"bindings"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&body); err != nil {
		return fmt.Errorf("sparql decode: %w", err)
	}
	seen := map[string]bool{}
	for _, b := range body.Results.Bindings {
		if src.Label == "" {
			src.Label = b["itemLabel"].Value
		}
		fact := Fact{
			Prop:  b["prop"].Value,
			Label: b["vLabel"].Value,
			QID:   qidFromURI(b["v"].Value),
			Date:  isoDate(b["date"].Value),
			Start: isoDate(b["start"].Value),
			End:   isoDate(b["end"].Value),
			Born:  isoDate(b["vBorn"].Value),
			Died:  isoDate(b["vDied"].Value),
		}
		switch fact.Prop {
		case "P569":
			if src.Born == "" {
				src.Born = fact.Date
			}
		case "P570":
			if src.Died == "" {
				src.Died = fact.Date
			}
		}
		k := fact.Prop + "|" + fact.QID + "|" + fact.Label + "|" + fact.Date + "|" + fact.Start
		if seen[k] {
			continue
		}
		seen[k] = true
		src.Facts = append(src.Facts, fact)
	}
	sort.SliceStable(src.Facts, func(i, j int) bool { return src.Facts[i].Prop < src.Facts[j].Prop })
	return nil
}

func qidFromURI(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 && strings.HasPrefix(s[i+1:], "Q") {
		return s[i+1:]
	}
	return ""
}

// isoDate — "1868-05-24T00:00:00Z" → "1868-05-24"; пустое остаётся пустым.
func isoDate(s string) string {
	if len(s) >= 10 && s[4] == '-' {
		return s[:10]
	}
	return s
}

// fetchArticle — статья по QID через sitelinks (wbgetentities) → extracts с
// заголовками разделов → обрезка небиографических разделов.
func (f *Fetcher) fetchArticle(ctx context.Context, lang, qid string) (*Article, error) {
	title, err := f.sitelink(ctx, lang, qid)
	if err != nil || title == "" {
		return nil, err
	}
	v := url.Values{}
	v.Set("action", "query")
	v.Set("prop", "extracts|revisions")
	v.Set("explaintext", "1")
	v.Set("exsectionformat", "wiki")
	v.Set("rvprop", "ids")
	v.Set("redirects", "1")
	v.Set("titles", title)
	v.Set("format", "json")
	v.Set("formatversion", "2")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.WikiAPI(lang)+"?"+v.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("extracts: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Query struct {
			Pages []struct {
				Title     string `json:"title"`
				Missing   bool   `json:"missing"`
				Extract   string `json:"extract"`
				Revisions []struct {
					RevID int64 `json:"revid"`
				} `json:"revisions"`
			} `json:"pages"`
		} `json:"query"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&body); err != nil {
		return nil, fmt.Errorf("extracts decode: %w", err)
	}
	if len(body.Query.Pages) == 0 || body.Query.Pages[0].Missing {
		return nil, nil
	}
	p := body.Query.Pages[0]
	a := &Article{Lang: lang, Title: p.Title, URL: "https://" + lang + ".wikipedia.org/wiki/" + url.PathEscape(strings.ReplaceAll(p.Title, " ", "_"))}
	if len(p.Revisions) > 0 {
		a.Rev = p.Revisions[0].RevID
	}
	a.Text = TrimToBiography(p.Extract, f.MaxTextRunes)
	return a, nil
}

func (f *Fetcher) sitelink(ctx context.Context, lang, qid string) (string, error) {
	v := url.Values{}
	v.Set("action", "wbgetentities")
	v.Set("ids", qid)
	v.Set("props", "sitelinks")
	v.Set("sitefilter", lang+"wiki")
	v.Set("format", "json")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.wikidata.org/w/api.php?"+v.Encode(), nil)
	if err != nil {
		return "", err
	}
	if f.WikidataAPI != nil {
		req, _ = http.NewRequestWithContext(ctx, http.MethodGet, f.WikidataAPI()+"?"+v.Encode(), nil)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := f.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("wbgetentities: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Entities map[string]struct {
			Sitelinks map[string]struct {
				Title string `json:"title"`
			} `json:"sitelinks"`
		} `json:"entities"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", fmt.Errorf("wbgetentities decode: %w", err)
	}
	e, ok := body.Entities[qid]
	if !ok {
		return "", nil
	}
	return e.Sitelinks[lang+"wiki"].Title, nil
}

// Разделы, которые не биография: библиография, память, экранизации, сноски…
var nonBioSection = regexp.MustCompile(`(?i)^(библиограф|сочинен|произведен|издани|собрани|память|увековеч|экраниз|фильмограф|примечани|литератур|ссылки|см\. также|адреса|галере|награды и премии|премии и награды|образ в |в культуре|в искусстве|музеи|памятники|переводы|наследие|bibliography|works|selected works|legacy|adaptations|references|notes|external links|see also|further reading|awards|honou?rs|filmography|in popular culture|memorials)`)

var sectionRe = regexp.MustCompile(`(?m)^(={2,})\s*(.+?)\s*={2,}\s*$`)

// TrimToBiography — выкинуть небиографические разделы (с их подразделами) и
// обрезать текст до maxRunes. Заголовки разделов в extracts — "== Раздел ==".
func TrimToBiography(text string, maxRunes int) string {
	idx := sectionRe.FindAllStringSubmatchIndex(text, -1)
	if len(idx) == 0 {
		return capRunes(text, maxRunes)
	}
	var b strings.Builder
	b.WriteString(text[:idx[0][0]]) // лид
	skipLevel := 0
	for i, m := range idx {
		level := m[3] - m[2]
		title := text[m[4]:m[5]]
		end := len(text)
		if i+1 < len(idx) {
			end = idx[i+1][0]
		}
		if skipLevel > 0 && level > skipLevel {
			continue // подраздел пропущенного
		}
		skipLevel = 0
		if nonBioSection.MatchString(title) {
			skipLevel = level
			continue
		}
		b.WriteString(text[m[0]:end])
	}
	return capRunes(b.String(), maxRunes)
}

func capRunes(s string, n int) string {
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}
