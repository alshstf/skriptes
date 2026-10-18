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
	QID   string `json:"qid"`
	Label string `json:"label"`
	Born  string `json:"born"`
	Died  string `json:"died,omitempty"`
	// OrigLang — язык оригинала (код раздела Википедии), если не ru и не en:
	// статья на нём добавляется к ru/en.
	OrigLang string    `json:"orig_lang,omitempty"`
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
	// Langs — явный список языков статей (например, ["ru", "en"]); пусто —
	// эвристика: ru, плюс en, если ru короче 20 тыс. символов или en в полтора
	// раза длиннее ru, плюс статья на языке оригинала (OrigLang).
	Langs []string
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

// Fetch — источники по QID: скелет фактов + статьи (ru, при короткой ru — и en,
// у пишущих не по-русски и не по-английски — и на языке оригинала).
func (f *Fetcher) Fetch(ctx context.Context, qid string) (*Sources, error) {
	src := &Sources{QID: qid}
	if err := f.fetchFacts(ctx, src); err != nil {
		return nil, fmt.Errorf("wikidata: %w", err)
	}
	if len(f.Langs) > 0 {
		for _, lang := range f.Langs {
			a, err := f.fetchArticle(ctx, lang, qid)
			if err != nil {
				return nil, fmt.Errorf("%swiki: %w", lang, err)
			}
			if a != nil {
				src.Articles = append(src.Articles, *a)
			}
		}
		if len(src.Articles) == 0 {
			return nil, fmt.Errorf("нет статьи в Википедии (%v)", f.Langs)
		}
		return src, nil
	}
	ru, err := f.fetchArticle(ctx, "ru", qid)
	if err != nil {
		return nil, fmt.Errorf("ruwiki: %w", err)
	}
	if ru != nil {
		src.Articles = append(src.Articles, *ru)
	}
	// Английская статья — если русская короткая (< 20 тыс. символов) или en
	// заметно полнее (в полтора раза длиннее): у иностранных авторов ru обычно
	// пересказ en, и без en модель теряет половину вех (замер 2026-10-13 на
	// пяти авторах: Перси 6 → 23, О’Коннор 8 → 15, Хайсмит 10 → 37). У русских
	// классиков ru длиннее — en не берём, чтобы не удваивать вход.
	en, err := f.fetchArticle(ctx, "en", qid)
	if err != nil {
		return nil, fmt.Errorf("enwiki: %w", err)
	}
	if en != nil {
		ruLen := 0
		if ru != nil {
			ruLen = utf8.RuneCountInString(ru.Text)
		}
		if ru == nil || ruLen < 20_000 || utf8.RuneCountInString(en.Text) > ruLen*3/2 {
			src.Articles = append(src.Articles, *en)
		}
	}
	// Статья на языке оригинала: у немцев, французов, поляков… она обычно
	// подробнее и en, и ru (пересказов с неё); модель переводит, как и en.
	// Совсем короткая ничего не добавит — не берём.
	if src.OrigLang != "" {
		o, err := f.fetchArticle(ctx, src.OrigLang, qid)
		if err != nil {
			return nil, fmt.Errorf("%swiki: %w", src.OrigLang, err)
		}
		if o != nil && utf8.RuneCountInString(o.Text) >= minOrigRunes {
			src.Articles = append(src.Articles, *o)
		}
	}
	if len(src.Articles) == 0 {
		return nil, fmt.Errorf("нет статьи ни в ru, ни в en Википедии")
	}
	return src, nil
}

// minOrigRunes — статья на языке оригинала короче (после обрезки) не берётся.
const minOrigRunes = 5_000

var wikiLangRe = regexp.MustCompile(`^[a-z]{2,3}(-[a-z]+)?$`)

// regionSubtagRe — код с региональным уточнением («en-gb», «pt-br»): раздела
// Википедии под ним нет, берётся основной язык. «be-tarask», «zh-yue» —
// отдельные разделы, не регион.
var regionSubtagRe = regexp.MustCompile(`^([a-z]{2,3})-[a-z]{2}$`)

// origLang — язык оригинала для статьи Википедии: языки произведений (P6886),
// без них — родной язык (P103), коды P424. Пишет по-русски или по-английски
// (в том числе «en-gb») — пусто: эти статьи берутся и так; из нескольких —
// первый по алфавиту.
func origLang(written, native []string) string {
	codes := written
	if len(codes) == 0 {
		codes = native
	}
	norm := make([]string, 0, len(codes))
	for _, c := range codes {
		c = strings.ToLower(strings.TrimSpace(c))
		if m := regionSubtagRe.FindStringSubmatch(c); m != nil {
			c = m[1]
		}
		if c == "nb" {
			c = "no" // норвежский букмол — no.wikipedia.org
		}
		if c == "ru" || c == "en" {
			return ""
		}
		norm = append(norm, c)
	}
	sort.Strings(norm)
	for _, c := range norm {
		if wikiLangRe.MatchString(c) {
			return c
		}
	}
	return ""
}

// sparqlFacts — один UNION-запрос: жизнь, браки, дети, родители, места
// жительства, учёба, награды, главные работы. Подписи — ru, фолбэк en.
const sparqlFacts = `
SELECT ?prop ?itemLabel ?v ?vLabel ?date ?start ?end ?vBorn ?vDied ?code WHERE {
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
  } UNION {
    VALUES ?prop { "P6886" "P103" }
    ?item ?pd ?v . ?pp wikibase:directClaim ?pd . BIND(STRAFTER(STR(?pp), "entity/") AS ?prop)
    FILTER(?pd IN (wdt:P6886, wdt:P103))
    ?v wdt:P424 ?code .
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
	langs := map[string][]string{} // P6886/P103 → коды языков; в факты не идут
	for _, b := range body.Results.Bindings {
		if src.Label == "" {
			src.Label = b["itemLabel"].Value
		}
		if p := b["prop"].Value; p == "P6886" || p == "P103" {
			if c := b["code"].Value; c != "" {
				langs[p] = append(langs[p], c)
			}
			continue
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
	src.OrigLang = origLang(langs["P6886"], langs["P103"])
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
// Кроме ru и en — основные языки оригинала (de, fr, es, it, pl, cs, pt, ja):
// списки произведений, сноски, ссылки, премии, наследие. Обзор творчества
// («Werk», «Obra», «Dílo» — как «Творчество») остаётся: там обстоятельства
// работы над текстами.
var nonBioSection = regexp.MustCompile(`(?i)^(библиограф|сочинен|произведен|издани|собрани|память|увековеч|экраниз|фильмограф|примечани|литератур|ссылки|см\. также|адреса|галере|награды и премии|премии и награды|образ в |в культуре|в искусстве|музеи|памятники|переводы|наследие|bibliogra|works|selected works|legacy|adaptations|referenc|notes|external links|see also|further reading|awards|honou?rs|filmogra|in popular culture|memorials|` +
	`werke|schriften|werkausgabe|literatur|weblinks|einzelnachweise|anmerkungen|quellen|siehe auch|verfilmung|auszeichnungen|ehrungen|rezeption|nachwirkung|nachleben|` +
	`œuvres|oeuvres|notes et références|références|liens externes|voir aussi|distinctions|hommages|postérité|filmographie|` +
	`obras|notas|enlaces externos|véase también|premios|adaptaciones|legado|filmografía|` +
	`opere|note|collegamenti esterni|voci correlate|riconoscimenti|adattamenti|premi|` +
	`dzieła|utwory|przypisy|linki zewnętrzne|zobacz też|upamiętnienie|ekranizacje|nagrody|odznaczenia|` +
	`odkazy|externí odkazy|ocenění|` +
	`referências|ligações externas|ver também|prémios|prêmios|` +
	`作品|著作|脚注|出典|参考文献|外部リンク|関連項目)`)

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
