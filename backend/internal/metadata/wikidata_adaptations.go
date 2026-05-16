package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
		httpClient = &http.Client{Timeout: 15 * time.Second}
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

func (p *WikidataAdaptationsProvider) Name() string { return "wikidata" }

// FetchAdaptations — основной entrypoint. Может вернуть пустой срез без
// ошибки, если книга найдена но экранизаций нет (это нормально, не
// ErrNotFound). ErrNotFound — если книгу не удалось сопоставить с QID
// вообще.
func (p *WikidataAdaptationsProvider) FetchAdaptations(ctx context.Context, q BookQuery) ([]Adaptation, error) {
	if q.Title == "" {
		return nil, ErrNotFound
	}
	qid, err := p.resolveBookQID(ctx, q)
	if err != nil {
		return nil, err
	}
	if qid == "" {
		return nil, ErrNotFound
	}
	adaptations, err := p.queryAdaptations(ctx, qid)
	if err != nil {
		return nil, err
	}
	return adaptations, nil
}

// resolveBookQID — wbsearchentities по title, валидация по автору.
//
// Пробуем язык q.Lang (по умолчанию ru), потом en. Берём топ-10
// кандидатов из wbsearchentities (он не различает "роман" от "альбома"
// от "телешоу" — фильтрация по P31 и автору делается SPARQL'ом).
func (p *WikidataAdaptationsProvider) resolveBookQID(ctx context.Context, q BookQuery) (string, error) {
	for _, lang := range bookSearchLangs(q.Lang) {
		qids, err := p.searchEntities(ctx, q.Title, lang)
		if err != nil {
			continue
		}
		for _, qid := range qids {
			ok, err := p.validateBookQID(ctx, qid, q.Authors)
			if err != nil {
				continue
			}
			if ok {
				return qid, nil
			}
		}
	}
	return "", ErrNotFound
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
		return nil, ErrNotFound
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
func (p *WikidataAdaptationsProvider) queryAdaptations(ctx context.Context, bookQID string) ([]Adaptation, error) {
	query := fmt.Sprintf(`
SELECT ?film ?filmLabel ?year ?directorLabel ?imdbId ?image ?kindLabel WHERE {
  ?film wdt:P144 wd:%s .
  OPTIONAL { ?film wdt:P577 ?date . BIND(YEAR(?date) AS ?year) }
  OPTIONAL { ?film wdt:P57 ?director . }
  OPTIONAL { ?film wdt:P345 ?imdbId . }
  OPTIONAL { ?film wdt:P18 ?image . }
  OPTIONAL { ?film wdt:P31 ?kind . }
  SERVICE wikibase:label { bd:serviceParam wikibase:language "ru,en".
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
			FilmURI:  b["film"].Value,
			Title:    b["filmLabel"].Value,
			Year:     b["year"].Value,
			Director: b["directorLabel"].Value,
			IMDBID:   b["imdbId"].Value,
			Image:    b["image"].Value,
			Kind:     b["kindLabel"].Value,
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
		return nil, fmt.Errorf("sparql status %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// aggregateAdaptations — схлопывает декартово произведение SPARQL-строк
// в одну запись per film. Поля:
//   - title / year / kind: первое непустое значение
//   - director: уникальные через ", " (несколько режиссёров — норма)
//   - imdb / image: первое непустое
func (p *WikidataAdaptationsProvider) aggregateAdaptations(rows []sparqlAdaptationRow) []Adaptation {
	type agg struct {
		title     string
		year      string
		kind      string
		imdbID    string
		image     string
		directors []string // в порядке появления, без дублей
		dirSet    map[string]struct{}
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
		if a.kind == "" {
			a.kind = r.Kind
		}
		if a.imdbID == "" {
			a.imdbID = r.IMDBID
		}
		if a.image == "" {
			a.image = r.Image
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
			Provider:  "wikidata",
			ExtID:     qid,
			Title:     strings.TrimSpace(a.title),
			Director:  strings.Join(a.directors, ", "),
			Kind:      mapWikidataKind(a.kind),
			PosterURL: p.posterURL(a.image),
			ExtURL:    "https://www.wikidata.org/wiki/" + qid,
		}
		if a.year != "" {
			if n, err := strconv.Atoi(a.year); err == nil && n > 1800 && n < 2200 {
				y := n
				ad.Year = &y
			}
		}
		if ad.Title == "" {
			// без названия запись бесполезна — фронт нечего показать.
			continue
		}
		out = append(out, ad)
	}
	return out
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
	FilmURI  string // http://www.wikidata.org/entity/Q12345
	Title    string
	Year     string
	Director string
	IMDBID   string
	Image    string
	Kind     string
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

// mapWikidataKind — нормализация P31 label'а в фиксированное множество.
// Сравниваем по en-low-case подстроке (label приходит в ru/en, тут мы
// проверяем оба варианта). Неизвестные значения → "other".
func mapWikidataKind(label string) string {
	if label == "" {
		return "film"
	}
	low := strings.ToLower(label)
	switch {
	case strings.Contains(low, "miniseries") || strings.Contains(low, "мини-сериал"):
		return "miniseries"
	case strings.Contains(low, "television series") || strings.Contains(low, "телесериал") || strings.Contains(low, "телевизионный сериал"):
		return "tv_series"
	case strings.Contains(low, "anime") || strings.Contains(low, "аниме"):
		return "anime"
	case strings.Contains(low, "film") || strings.Contains(low, "фильм") || strings.Contains(low, "кино"):
		return "film"
	default:
		return "other"
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
