package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// WikidataAdaptationsProvider — экранизации книг через Wikidata.
//
// Алгоритм:
//
//  1. Резолвим книгу в QID через action=wbsearchentities (сначала ru,
//     потом en). Берём топ-N кандидатов и валидируем каждого SPARQL'ом:
//     должен иметь автора (P50), чьё имя матчится с q.Authors. Иначе
//     рискуем взять одноимённую книгу другого автора (типичный случай —
//     "Идиот" Достоевского vs. одноимённые произведения других авторов).
//
//  2. Для подтверждённого QID — SPARQL "?film wdt:P144 wd:QID" получает
//     все экранизации с дополнительными полями (год P577, режиссёр P57,
//     IMDB-ID P345, постер P18, тип P31).
//
// Wikidata SPARQL endpoint имеет публичный лимит ~30 req/min для
// анонимов; для lazy-enrichment одной книги это с большим запасом.
// Все запросы кэшируются в metadata_cache (планируется отдельным PR);
// в текущей версии — простой in-flight dedup в Enricher.
type WikidataAdaptationsProvider struct {
	httpClient *http.Client

	// Endpoints (override-able for httptest).
	searchURL  string // https://www.wikidata.org/w/api.php
	sparqlURL  string // https://query.wikidata.org/sparql
	commonsURL string // https://commons.wikimedia.org/wiki/Special:FilePath/
}

// wdUserAgent — Wikidata Query Service требует User-Agent (см.
// https://www.mediawiki.org/wiki/Wikidata_Query_Service/User_Manual#Query_limits).
// При его отсутствии endpoint возвращает 403.
const wdUserAgent = "skriptes/0.1 (https://github.com/alshstf/skriptes; adaptations-enricher)"

func NewWikidataAdaptationsProvider(httpClient *http.Client) *WikidataAdaptationsProvider {
	if httpClient == nil {
		httpClient = SourceHTTPClient(15 * time.Second)
	}
	return &WikidataAdaptationsProvider{
		httpClient: httpClient,
		searchURL:  "https://www.wikidata.org/w/api.php",
		sparqlURL:  "https://query.wikidata.org/sparql",
		commonsURL: "https://commons.wikimedia.org/wiki/Special:FilePath/",
	}
}

// WithEndpoints — для тестов: переопределить хосты на httptest.Server.
// Можно передать пустую строку для значения которое не нужно переопределять.
func (p *WikidataAdaptationsProvider) WithEndpoints(searchURL, sparqlURL, commonsURL string) *WikidataAdaptationsProvider {
	if searchURL != "" {
		p.searchURL = searchURL
	}
	if sparqlURL != "" {
		p.sparqlURL = sparqlURL
	}
	if commonsURL != "" {
		p.commonsURL = commonsURL
	}
	return p
}

func (p *WikidataAdaptationsProvider) Name() string { return wikidataSource }

// wikidataSource — имя источника Wikidata в учёте попыток (book_*_lookups).
const wikidataSource = "wikidata"

// FetchAdaptations — основной entrypoint. Может вернуть пустой срез без
// ошибки, если книга найдена но экранизаций нет (это нормально, не
// ErrNotFound). ErrNotFound — если книгу не удалось сопоставить с QID
// вообще.
func (p *WikidataAdaptationsProvider) FetchAdaptations(ctx context.Context, q BookQuery) ([]Adaptation, error) {
	items, _, err := p.FetchAdaptationsWithQID(ctx, q)
	return items, err
}

// FetchAdaptationsWithQID — FetchAdaptations плюс проверенный по автору QID
// книги: EnsureAdaptations сохраняет его ключом группировки изданий (#467).
func (p *WikidataAdaptationsProvider) FetchAdaptationsWithQID(ctx context.Context, q BookQuery) ([]Adaptation, string, error) {
	if q.Title == "" {
		return nil, "", ErrNotFound
	}
	qid, err := p.resolveBookQID(ctx, q)
	if err != nil {
		return nil, "", err
	}
	if qid == "" {
		return nil, "", ErrNotFound
	}
	adaptations, err := p.queryAdaptations(ctx, qid)
	if err != nil {
		return nil, "", err
	}
	return adaptations, qid, nil
}

// resolveBookQID — wbsearchentities по title, валидация по автору.
//
// Пробуем язык q.Lang (по умолчанию ru), потом en. Берём топ-10
// кандидатов из wbsearchentities (он не различает "роман" от "альбома"
// от "телешоу" — фильтрация по P31 и автору делается SPARQL'ом).
func (p *WikidataAdaptationsProvider) resolveBookQID(ctx context.Context, q BookQuery) (string, error) {
	if isQID(q.WikidataQID) {
		return q.WikidataQID, nil // QID работы уже известен (#294) — поиск не нужен
	}
	// failure — сбой поиска или проверки кандидата (429 под троттлингом, сеть):
	// если ни один кандидат не подтвердился, это не «не найдено» — нужный мог
	// быть среди непроверенных. Раньше сбой превращался в ErrNotFound: lookups
	// писали not_found на 90 дней, экранизации — «нет» навсегда (грабля №20, #281).
	var failure error
	for _, lang := range bookSearchLangs(q.Lang) {
		qids, err := p.searchEntities(ctx, q.Title, lang)
		if err != nil {
			if !errors.Is(err, ErrNotFound) {
				failure = err
			}
			continue
		}
		for _, qid := range qids {
			ok, err := p.validateBookQID(ctx, qid, q.Authors)
			if err != nil {
				failure = err
				continue
			}
			if ok {
				return qid, nil
			}
		}
	}
	if failure != nil {
		if errors.Is(failure, ErrUpstream) {
			return "", failure
		}
		return "", fmt.Errorf("%w: wikidata: %w", ErrUpstream, failure)
	}
	return "", ErrNotFound
}

// ResolveWorkKey — Wikidata QID работы как ключ группировки изданий (Tier-2).
// Переиспользует resolveBookQID (wbsearchentities по названию + валидация по
// автору P50). Для переводов ищем по оригинальному названию (SrcTitle), если
// оно есть — выше шанс найти исходную работу. Реализует WorkKeyResolver.
func (p *WikidataAdaptationsProvider) ResolveWorkKey(ctx context.Context, q WorkQuery) (string, error) {
	title := q.SrcTitle
	if title == "" {
		title = q.Title
	}
	if title == "" || len(q.Authors) == 0 {
		return "", ErrNotFound // без автора Wikidata-валидация всё равно отвергнет
	}
	return p.resolveBookQID(ctx, BookQuery{ID: q.BookID, Title: title, Authors: q.Authors, Lang: q.Lang})
}

// searchEntities — wbsearchentities → массив QID'ов кандидатов.
func (p *WikidataAdaptationsProvider) searchEntities(ctx context.Context, title, lang string) ([]string, error) {
	v := url.Values{}
	v.Set("action", "wbsearchentities")
	v.Set("search", title)
	v.Set("language", lang)
	v.Set("type", "item")
	v.Set("limit", "10")
	v.Set("format", "json")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.searchURL+"?"+v.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build wbsearchentities: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", wdUserAgent)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wbsearchentities: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, statusErr(resp.StatusCode)
	}

	var body struct {
		Search []struct {
			ID string `json:"id"`
		} `json:"search"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("decode wbsearchentities: %w", err)
	}
	out := make([]string, 0, len(body.Search))
	for _, s := range body.Search {
		if s.ID != "" {
			out = append(out, s.ID)
		}
	}
	return out, nil
}

// validateBookQID — проверяем что у QID есть P50 (author), чьё имя
// (rdfs:label) пересекается с q.Authors. Используем SPARQL — это
// единый запрос, не надо тащить полную карточку через wbgetentities.
//
// Если в карточке нет P50 — отбрасываем (это может быть фильм,
// одноимённый альбом, итд).
func (p *WikidataAdaptationsProvider) validateBookQID(ctx context.Context, qid string, expectedAuthors []string) (bool, error) {
	if len(expectedAuthors) == 0 {
		// Нечем валидировать — лучше отвергнуть, чем взять чужую книгу.
		return false, nil
	}
	q := fmt.Sprintf(`
SELECT DISTINCT ?authorLabel WHERE {
  wd:%s wdt:P50 ?author .
  ?author rdfs:label ?authorLabel .
  FILTER(LANG(?authorLabel) IN ("ru","en"))
}
LIMIT 20
`, qid)

	labels, err := p.runSPARQLAuthorLabels(ctx, q)
	if err != nil {
		return false, err
	}
	if len(labels) == 0 {
		return false, nil
	}
	// Совпадение — нечувствительный к регистру и порядку слов поиск:
	// для каждого ожидаемого автора смотрим, есть ли среди labels такой,
	// который содержит ВСЕ слова из имени автора. Это допускает
	// "Толстой, Лев Николаевич" vs. "Лев Николаевич Толстой" vs. "Leo Tolstoy".
	for _, expected := range expectedAuthors {
		if matchAuthorAny(expected, labels) {
			return true, nil
		}
	}
	return false, nil
}

// queryAdaptations — SPARQL запрос экранизаций для подтверждённого QID.
//
// Возвращает уже дедуплицированный по ?film срез: при множественных
// директорах/типах SPARQL дал бы декартово произведение строк, мы
// агрегируем в Go (директоров через запятую, kind — первый встретившийся,
// year — первый встретившийся, и т.д.).
//
// Поля внешних идентификаторов (P345 IMDb, P2603 Кинопоиск) и
// wikibase:sitelinks используются для построения ext_url и popularity
// при агрегации.
func (p *WikidataAdaptationsProvider) queryAdaptations(ctx context.Context, bookQID string) ([]Adaptation, error) {
	query := fmt.Sprintf(`
SELECT ?film ?filmLabel ?year ?directorLabel ?imdbId ?kinopoiskId ?image ?kindLabel ?sitelinks ?tmdbMovie ?tmdbTv WHERE {
  ?film wdt:P144 wd:%s .
  OPTIONAL { ?film wdt:P577 ?date . BIND(YEAR(?date) AS ?year) }
  OPTIONAL { ?film wdt:P57 ?director . }
  OPTIONAL { ?film wdt:P345 ?imdbId . }
  OPTIONAL { ?film wdt:P2603 ?kinopoiskId . }
  OPTIONAL { ?film wdt:P18 ?image . }
  OPTIONAL { ?film wdt:P4947 ?tmdbMovie . }
  OPTIONAL { ?film wdt:P4983 ?tmdbTv . }
  OPTIONAL { ?film wdt:P31 ?kind . }
  OPTIONAL { ?film wikibase:sitelinks ?sitelinks . }
  SERVICE wikibase:label { bd:serviceParam wikibase:language "ru,en,mul,uk,de,fr,es,it,ja".
    ?film rdfs:label ?filmLabel .
    ?director rdfs:label ?directorLabel .
    ?kind rdfs:label ?kindLabel .
  }
}
LIMIT 200
`, bookQID)

	rows, err := p.runSPARQLAdaptations(ctx, query)
	if err != nil {
		return nil, err
	}
	return p.aggregateAdaptations(rows), nil
}

// runSPARQLAuthorLabels — упрощённая обёртка над SPARQL для одного
// поля ?authorLabel. Возвращает все значения как массив строк.
func (p *WikidataAdaptationsProvider) runSPARQLAuthorLabels(ctx context.Context, query string) ([]string, error) {
	body, err := p.doSPARQL(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()

	var resp struct {
		Results struct {
			Bindings []map[string]struct {
				Value string `json:"value"`
			} `json:"bindings"`
		} `json:"results"`
	}
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode sparql: %w", err)
	}
	out := make([]string, 0, len(resp.Results.Bindings))
	for _, b := range resp.Results.Bindings {
		if v, ok := b["authorLabel"]; ok && v.Value != "" {
			out = append(out, v.Value)
		}
	}
	return out, nil
}

// runSPARQLAdaptations — SPARQL-результат для экранизаций. Парсит
// фиксированный набор полей.
func (p *WikidataAdaptationsProvider) runSPARQLAdaptations(ctx context.Context, query string) ([]sparqlAdaptationRow, error) {
	body, err := p.doSPARQL(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()

	var resp struct {
		Results struct {
			Bindings []map[string]struct {
				Value string `json:"value"`
			} `json:"bindings"`
		} `json:"results"`
	}
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("decode sparql: %w", err)
	}
	out := make([]sparqlAdaptationRow, 0, len(resp.Results.Bindings))
	for _, b := range resp.Results.Bindings {
		row := sparqlAdaptationRow{
			FilmURI:     b["film"].Value,
			Title:       b["filmLabel"].Value,
			Year:        b["year"].Value,
			Director:    b["directorLabel"].Value,
			IMDBID:      b["imdbId"].Value,
			KinopoiskID: b["kinopoiskId"].Value,
			Image:       b["image"].Value,
			Kind:        b["kindLabel"].Value,
			Sitelinks:   b["sitelinks"].Value,
			TMDBMovieID: b["tmdbMovie"].Value,
			TMDBTVID:    b["tmdbTv"].Value,
		}
		if row.FilmURI == "" {
			continue
		}
		out = append(out, row)
	}
	return out, nil
}

// doSPARQL — POST запрос на /sparql с Accept: application/sparql-results+json.
// POST используется на случай длинных запросов; для коротких GET тоже
// работает, но единый путь проще.
func (p *WikidataAdaptationsProvider) doSPARQL(ctx context.Context, query string) (io.ReadCloser, error) {
	form := url.Values{}
	form.Set("query", query)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.sparqlURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("build sparql request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/sparql-results+json")
	req.Header.Set("User-Agent", wdUserAgent)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sparql: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		// Любой не-200 SPARQL-эндпоинта — сбой (429 троттлинга, 5xx), не «нет данных».
		return nil, fmt.Errorf("%w: sparql status %d", ErrUpstream, resp.StatusCode)
	}
	return resp.Body, nil
}

// aggregateAdaptations — схлопывает декартово произведение SPARQL-строк
// в одну запись per film. Поля:
//   - title / year / kind: первое непустое значение
//   - director: уникальные через ", " (несколько режиссёров — норма)
//   - imdb / kinopoisk / image / sitelinks: первое непустое
//
// ExtURL выбирается по приоритету Кинопоиск → IMDb → Wikidata.
// Wikidata-страница — fallback для зрителя нерелевантна (это страница
// для редакторов с QID и properties), Кинопоиск/IMDb — нормальные
// карточки с описанием/трейлерами/постером.
func (p *WikidataAdaptationsProvider) aggregateAdaptations(rows []sparqlAdaptationRow) []Adaptation {
	type agg struct {
		title       string
		year        string
		kinds       []string // все метки P31: у фильма их бывает несколько
		imdbID      string
		kinopoiskID string
		image       string
		sitelinks   string
		tmdbMovieID string
		tmdbTVID    string
		directors   []string // в порядке появления, без дублей
		dirSet      map[string]struct{}
	}
	byFilm := map[string]*agg{}
	order := []string{} // сохраняем порядок появления QID
	for _, r := range rows {
		a, ok := byFilm[r.FilmURI]
		if !ok {
			a = &agg{dirSet: map[string]struct{}{}}
			byFilm[r.FilmURI] = a
			order = append(order, r.FilmURI)
		}
		if a.title == "" {
			a.title = r.Title
		}
		if a.year == "" {
			a.year = r.Year
		}
		if r.Kind != "" && !slices.Contains(a.kinds, r.Kind) {
			a.kinds = append(a.kinds, r.Kind)
		}
		if a.imdbID == "" {
			a.imdbID = r.IMDBID
		}
		if a.kinopoiskID == "" {
			a.kinopoiskID = r.KinopoiskID
		}
		if a.image == "" {
			a.image = r.Image
		}
		if a.sitelinks == "" {
			a.sitelinks = r.Sitelinks
		}
		if a.tmdbMovieID == "" {
			a.tmdbMovieID = r.TMDBMovieID
		}
		if a.tmdbTVID == "" {
			a.tmdbTVID = r.TMDBTVID
		}
		if r.Director != "" {
			if _, seen := a.dirSet[r.Director]; !seen {
				a.dirSet[r.Director] = struct{}{}
				a.directors = append(a.directors, r.Director)
			}
		}
	}

	out := make([]Adaptation, 0, len(order))
	for _, uri := range order {
		a := byFilm[uri]
		qid := extractQID(uri)
		ad := Adaptation{
			Provider:    "wikidata",
			ExtID:       qid,
			Title:       strings.TrimSpace(a.title),
			Director:    strings.Join(a.directors, ", "),
			Kind:        screenKind(a.kinds),
			PosterURL:   p.posterURL(a.image),
			ExtURL:      pickExtURL(a.kinopoiskID, a.imdbID, qid),
			TMDBMovieID: a.tmdbMovieID,
			TMDBTVID:    a.tmdbTVID,
		}
		if a.year != "" {
			if n, err := strconv.Atoi(a.year); err == nil && n > 1800 && n < 2200 {
				y := n
				ad.Year = &y
			}
		}
		if a.sitelinks != "" {
			if n, err := strconv.Atoi(a.sitelinks); err == nil && n >= 0 {
				ad.Popularity = n
			}
		}
		if ad.Title == "" || isQID(ad.Title) {
			// Без названия запись бесполезна — фронт нечего показать (у сервиса
			// подписей нет метки ни на одном языке — он отдаёт голый QID).
			continue
		}
		if ad.Kind == kindOther {
			// Не экранизация: «основано на» (P144) у опер, игр, песен, настольных
			// игр и трактатов — на проде 37 % записей (#295).
			continue
		}
		out = append(out, ad)
	}
	return out
}

// pickExtURL — приоритет внешних ссылок: Кинопоиск > IMDb > Wikidata.
// Wikidata оставлен как fallback "лучше что-то чем ничего", но эта
// страница не предназначена для конечного пользователя (показывает
// QID, property/value пары без человеческого контекста).
//
// kinopoiskID — числовой (например "42664"); IMDb-id формата "tt0123456".
// Финальные URL'ы вида:
//
//	https://www.kinopoisk.ru/film/42664/
//	https://www.imdb.com/title/tt0123456/
//	https://www.wikidata.org/wiki/Q12345
func pickExtURL(kinopoiskID, imdbID, qid string) string {
	if kinopoiskID != "" {
		return "https://www.kinopoisk.ru/film/" + kinopoiskID + "/"
	}
	if imdbID != "" {
		return "https://www.imdb.com/title/" + imdbID + "/"
	}
	return "https://www.wikidata.org/wiki/" + qid
}

// posterURL — конструирует thumbnail URL из Commons-имени файла.
// Возвращает пустую строку для пустого input. Width=400 даёт нормальную
// картинку для горизонтального скролла; full-size перебрал бы трафик.
func (p *WikidataAdaptationsProvider) posterURL(commonsFile string) string {
	if commonsFile == "" {
		return ""
	}
	// commonsFile приходит как полный URL вида
	// "http://commons.wikimedia.org/wiki/Special:FilePath/Foo.jpg".
	// Берём филейм после Special:FilePath/, чтобы переписать на наш
	// commonsURL (для тестов важно) и добавить width=400.
	const marker = "Special:FilePath/"
	idx := strings.Index(commonsFile, marker)
	var fname string
	if idx >= 0 {
		fname = commonsFile[idx+len(marker):]
	} else {
		fname = commonsFile
	}
	// fname уже URL-encoded в значении SPARQL. Не дёргаем PathEscape
	// чтобы не получить двойное escape'ье.
	return strings.TrimRight(p.commonsURL, "/") + "/" + fname + "?width=400"
}

// sparqlAdaptationRow — сырая строка SPARQL-результата до агрегации.
type sparqlAdaptationRow struct {
	FilmURI     string // http://www.wikidata.org/entity/Q12345
	Title       string
	Year        string
	Director    string
	IMDBID      string
	KinopoiskID string
	Image       string
	Kind        string
	Sitelinks   string // целое число как строка (xsd:integer из SPARQL)
	TMDBMovieID string // P4947 — TMDb film ID (приоритетный источник постера)
	TMDBTVID    string // P4983 — TMDb TV series ID
}

// isQID — строка вида «Q12345» (QID подставляется в SPARQL как wd:QID, поэтому
// чужое значение из ext_ids не пропускаем).
func isQID(s string) bool {
	if len(s) < 2 || s[0] != 'Q' {
		return false
	}
	for _, r := range s[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// extractQID — выдёргивает Q-id из URI типа
// "http://www.wikidata.org/entity/Q12345" → "Q12345". Возвращает
// пустую строку если префикс не распознан (защищаемся от мусора).
func extractQID(uri string) string {
	const prefix = "/entity/"
	i := strings.LastIndex(uri, prefix)
	if i < 0 {
		return ""
	}
	return uri[i+len(prefix):]
}

// kindOther — не экранизация: такие записи не сохраняются (#295).
const kindOther = "other"

// screenKind — вид экранизации по всем меткам P31 сущности: экранный вид, если
// он есть хоть у одной метки (у фильма их бывает несколько, и первая может быть
// «произведение искусства»), с приоритетом мини-сериал > аниме > сериал > фильм.
// Нет меток вовсе — фильм (как раньше); метки есть, экранных нет — kindOther.
func screenKind(labels []string) string {
	if len(labels) == 0 {
		return "film"
	}
	best := kindOther
	rank := map[string]int{kindOther: 0, "film": 1, "tv_series": 2, "anime": 3, "miniseries": 4}
	for _, l := range labels {
		if k := mapWikidataKind(l); rank[k] > rank[best] {
			best = k
		}
	}
	return best
}

// mapWikidataKind — вид экранизации по одной метке P31 (ru/en, подстрокой).
// Белый список: всё, чего в нём нет (видеоигра, опера, песня, роман), — kindOther.
func mapWikidataKind(label string) string {
	if label == "" {
		return "film"
	}
	low := strings.ToLower(label)
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(low, s) {
				return true
			}
		}
		return false
	}
	switch {
	case has("miniseries", "мини-сериал"):
		return "miniseries"
	case has("anime", "аниме"):
		return "anime"
	case has("television series", "телесериал", "телевизионный сериал", "web series", "веб-сериал",
		"animated series", "мультсериал", "анимационный сериал"):
		return "tv_series"
	case has("film", "фильм", "кино", "television play", "teleplay", "телеспектакль"):
		return "film"
	default:
		return kindOther
	}
}

// bookSearchLangs — какие языки пробовать для wbsearchentities. См.
// аналогичный комментарий в WikipediaProvider.langs.
func bookSearchLangs(qLang string) []string {
	pref := strings.ToLower(qLang)
	switch pref {
	case "en":
		return []string{"en", "ru"}
	case "ru":
		return []string{"ru", "en"}
	default:
		return []string{"ru", "en"}
	}
}

// matchAuthorAny — нечувствительная к регистру и порядку проверка:
// есть ли среди candidates строка, содержащая ≥2 слов имени (либо все
// если в имени 1 токен). Имя нормализуется: запятые и точки убираются,
// разбивается по пробелам. Допускает форматы:
//
//	"Толстой, Лев Николаевич"  vs.  "Лев Толстой"  (отчества часто нет)
//	"Tolstoy, Leo"             vs.  "Leo Tolstoy"
//
// 2 токена — минимум для уверенности что это тот же человек: при 1
// токене (например только фамилия) ловится слишком много ложных
// срабатываний (одна "Толстой" может быть и Лев, и Алексей, и Татьяна).
//
// Cross-language (Кириллица vs Latin) разруливается тем, что SPARQL
// возвращает обе локали — для русской книги в авторском labels-списке
// будет и "Лев Толстой" и "Leo Tolstoy", достаточно чтобы хотя бы
// одна совпала по 2 токенам.
func matchAuthorAny(name string, candidates []string) bool {
	tokens := authorTokens(name)
	if len(tokens) == 0 {
		return false
	}
	needed := 2
	if len(tokens) < needed {
		needed = len(tokens)
	}
	for _, c := range candidates {
		if countMatchingTokens(strings.ToLower(c), tokens) >= needed {
			return true
		}
	}
	return false
}

func authorTokens(name string) []string {
	cleaned := strings.NewReplacer(",", " ", ".", " ", "_", " ").Replace(name)
	parts := strings.Fields(strings.ToLower(cleaned))
	out := parts[:0]
	for _, p := range parts {
		// Игнорируем инициалы вида "и." (после Replace они стали "и") —
		// они дают ложные срабатывания. Минимум 2 символа.
		if len([]rune(p)) >= 2 {
			out = append(out, p)
		}
	}
	return out
}

func countMatchingTokens(haystack string, tokens []string) int {
	n := 0
	for _, t := range tokens {
		if strings.Contains(haystack, t) {
			n++
		}
	}
	return n
}
