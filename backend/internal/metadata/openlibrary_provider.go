package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OpenLibraryProvider — обложки через https://openlibrary.org/search.json
// и https://covers.openlibrary.org/b/id/{cover_i}-L.jpg.
//
// OL не требует API-ключа. Rate limit мягкий (~100 req/min), мы делаем
// <=2 запроса на одну книгу (search + cover), так что для lazy-enrichment
// этого хватит с большим запасом.
//
// Хит-rate для русскоязычных книг низкий — OL caталог сильно англо-
// центричный. Но для переводов и не-русских книг работает прилично.
type OpenLibraryProvider struct {
	httpClient *http.Client
	searchURL  string // override для тестов; по умолчанию https://openlibrary.org/search.json
	coverURL   string // override для тестов; по умолчанию https://covers.openlibrary.org

	// occupationGate — слой 2 точности матчинга автора (P106), зеркало
	// WikipediaProvider.occupationGate. nil = выключен. Отсекает однофамильца-
	// не-писателя ПОСЛЕ имя-гейта. QID берём бесплатно из remote_ids.wikidata
	// детальной записи автора (в отличие от wiki, где нужен отдельный pageprops).
	occupationGate func(ctx context.Context, qid string) (OccupationVerdict, error)
}

func NewOpenLibraryProvider(httpClient *http.Client) *OpenLibraryProvider {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &OpenLibraryProvider{
		httpClient: httpClient,
		searchURL:  "https://openlibrary.org/search.json",
		coverURL:   "https://covers.openlibrary.org",
	}
}

// WithEndpoints — для тестов: переопределить хосты на httptest.Server.
func (p *OpenLibraryProvider) WithEndpoints(searchURL, coverURL string) *OpenLibraryProvider {
	p.searchURL = searchURL
	p.coverURL = coverURL
	return p
}

// WithOccupationGate включает слой 2 точности для авторского матчинга OL:
// после имя-гейта проверяет профессию кандидата (Wikidata P106) по
// remote_ids.wikidata и отвергает явных не-писателей. nil = выкл. Реализация —
// та же WikidataAdaptationsProvider.OccupationVerdict, что и у wiki-пути.
func (p *OpenLibraryProvider) WithOccupationGate(fn func(ctx context.Context, qid string) (OccupationVerdict, error)) *OpenLibraryProvider {
	p.occupationGate = fn
	return p
}

func (p *OpenLibraryProvider) Name() string { return "openlibrary" }

// FetchCover делает:
//  1. GET /search.json?title=...&author=...&limit=1
//  2. читает docs[0].cover_i
//  3. GET covers.openlibrary.org/b/id/{cover_i}-L.jpg
func (p *OpenLibraryProvider) FetchCover(ctx context.Context, q BookQuery) (*CoverImage, error) {
	if q.Title == "" {
		return nil, ErrNotFound
	}

	v := url.Values{}
	v.Set("title", q.Title)
	if len(q.Authors) > 0 {
		v.Set("author", q.Authors[0])
	}
	v.Set("limit", "1")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.searchURL+"?"+v.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build search request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ol search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, statusErr(resp.StatusCode)
	}

	var sr olSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("decode search: %w", err)
	}
	if len(sr.Docs) == 0 || sr.Docs[0].CoverI == 0 {
		return nil, ErrNotFound
	}

	coverID := sr.Docs[0].CoverI
	coverReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/b/id/%d-L.jpg", p.coverURL, coverID), nil)
	if err != nil {
		return nil, fmt.Errorf("build cover request: %w", err)
	}
	coverResp, err := p.httpClient.Do(coverReq)
	if err != nil {
		return nil, fmt.Errorf("ol cover: %w", err)
	}
	if coverResp.StatusCode != http.StatusOK {
		_ = coverResp.Body.Close()
		return nil, statusErr(coverResp.StatusCode)
	}

	mime := coverResp.Header.Get("Content-Type")
	if mime == "" || !strings.HasPrefix(mime, "image/") {
		mime = "image/jpeg"
	}
	return &CoverImage{
		Reader:   coverResp.Body, // caller закроет
		Mime:     mime,
		SourceID: fmt.Sprintf("ol:cover:%d", coverID),
	}, nil
}

type olSearchDoc struct {
	CoverI           int64    `json:"cover_i"`
	Key              string   `json:"key"` // "/works/OL12345W"
	FirstPublishYear int      `json:"first_publish_year"`
	AuthorName       []string `json:"author_name"`
	// Счётчики известности (FetchRenown). ⚠️ Solr-schema search-полей OL
	// официально «не гарантированно стабильна» — парсим defensively.
	RatingsCount    int `json:"ratings_count"`
	WantToReadCount int `json:"want_to_read_count"`
}

// ResolveWorkKey — внешний идентификатор работы OpenLibrary для группировки
// изданий (Tier-2). Стратегия:
//  1. ISBN (самый точный, язык-агностичный): GET /isbn/{isbn}.json →
//     works[0].key. Гейт по автору не нужен — ISBN однозначен.
//  2. иначе поиск по названию (для переводов — оригинальному src_title) +
//     автору: /search.json?fields=key,author_name → docs[0].key ТОЛЬКО если
//     authorNameMatches (precision > recall, как в authorSearch).
//
// Возвращает чистый OL Work ID ("OL12345W") либо ErrNotFound.
func (p *OpenLibraryProvider) ResolveWorkKey(ctx context.Context, q WorkQuery) (string, error) {
	if isbn := normalizeISBN(q.ISBN); isbn != "" {
		if key, err := p.resolveWorkByISBN(ctx, isbn); err == nil {
			return key, nil
		} else if !errors.Is(err, ErrNotFound) {
			return "", err
		}
	}
	title := q.SrcTitle
	if title == "" {
		title = q.Title
	}
	if title == "" {
		return "", ErrNotFound
	}
	v := url.Values{}
	v.Set("title", title)
	if len(q.Authors) > 0 {
		v.Set("author", q.Authors[0])
	}
	v.Set("limit", "1")
	v.Set("fields", "key,author_name")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.searchURL+"?"+v.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("build work search: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ol work search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", statusErr(resp.StatusCode)
	}
	var sr olSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return "", fmt.Errorf("decode work search: %w", err)
	}
	if len(sr.Docs) == 0 || sr.Docs[0].Key == "" {
		return "", ErrNotFound
	}
	// Гейт по автору: OL-поиск может вернуть однофамильца/другую работу.
	gate := AuthorQuery{LastName: q.LastName, FirstName: q.FirstName}
	if !anyAuthorMatches(gate, sr.Docs[0].AuthorName) {
		return "", ErrNotFound
	}
	return strings.TrimPrefix(sr.Docs[0].Key, "/works/"), nil
}

// resolveWorkByISBN: GET /isbn/{isbn}.json → works[0].key.
func (p *OpenLibraryProvider) resolveWorkByISBN(ctx context.Context, isbn string) (string, error) {
	u := strings.TrimRight(p.workBaseURL(), "/") + "/isbn/" + url.PathEscape(isbn) + ".json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", fmt.Errorf("build isbn request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ol isbn: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", statusErr(resp.StatusCode)
	}
	var ed struct {
		Works []struct {
			Key string `json:"key"`
		} `json:"works"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ed); err != nil {
		return "", fmt.Errorf("decode isbn edition: %w", err)
	}
	if len(ed.Works) == 0 || ed.Works[0].Key == "" {
		return "", ErrNotFound
	}
	return strings.TrimPrefix(ed.Works[0].Key, "/works/"), nil
}

// anyAuthorMatches — проходит ли хоть один из кандидатов-имён гейт по автору.
func anyAuthorMatches(q AuthorQuery, candidates []string) bool {
	if q.LastName == "" {
		return true // нечем гейтить
	}
	for _, c := range candidates {
		if authorNameMatches(q, c) {
			return true
		}
	}
	return false
}

type olSearchResponse struct {
	Docs []olSearchDoc `json:"docs"`
}

// FetchRating — внешний рейтинг работы: резолвим OL Work ID (ResolveWorkKey:
// ISBN-first, иначе title+author за гейтом authorNameMatches) → GET
// /works/{key}/ratings.json → summary.average (1–5) / summary.count. Реализует
// RatingProvider. ErrNotFound, если работа не нашлась или рейтинга/голосов нет.
func (p *OpenLibraryProvider) FetchRating(ctx context.Context, q WorkQuery) (RatingResult, error) {
	key, err := p.ResolveWorkKey(ctx, q)
	if err != nil {
		return RatingResult{}, err // ErrNotFound пробрасывается как есть
	}
	u := strings.TrimRight(p.workBaseURL(), "/") + "/works/" + url.PathEscape(key) + "/ratings.json"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return RatingResult{}, fmt.Errorf("build ratings request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return RatingResult{}, fmt.Errorf("ol ratings: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return RatingResult{}, statusErr(resp.StatusCode)
	}
	var rr struct {
		Summary struct {
			Average *float64 `json:"average"`
			Count   int      `json:"count"`
		} `json:"summary"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return RatingResult{}, fmt.Errorf("decode ratings: %w", err)
	}
	if rr.Summary.Average == nil || *rr.Summary.Average <= 0 || rr.Summary.Count <= 0 {
		return RatingResult{}, ErrNotFound
	}
	return RatingResult{Average: *rr.Summary.Average, Count: rr.Summary.Count}, nil
}

// FetchRenown — счётчики известности работы из OL search: ratings_count +
// want_to_read_count прямо в поисковой выдаче (батч-friendly, отдельные
// ratings.json/bookshelves.json не нужны). Переводы одной книги у OL — РАЗНЫЕ
// records («Метро 2033» и "Metro 2033" раздельно), поэтому суммируем счётчики
// всех докoв, прошедших гейт по автору (title= уже сужает выдачу до названия).
// Для переводных книг ищем по оригиналу (SrcTitle) — как ResolveWorkKey.
// Реализует RenownProvider.
func (p *OpenLibraryProvider) FetchRenown(ctx context.Context, q WorkQuery) (RenownResult, error) {
	title := q.SrcTitle
	if title == "" {
		title = q.Title
	}
	if strings.TrimSpace(title) == "" {
		return RenownResult{}, ErrNotFound
	}
	v := url.Values{}
	v.Set("title", title)
	if len(q.Authors) > 0 {
		v.Set("author", q.Authors[0])
	}
	v.Set("limit", "5")
	v.Set("fields", "key,author_name,ratings_count,want_to_read_count")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.searchURL+"?"+v.Encode(), nil)
	if err != nil {
		return RenownResult{}, fmt.Errorf("build renown search: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return RenownResult{}, fmt.Errorf("ol renown search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return RenownResult{}, statusErr(resp.StatusCode)
	}
	var sr olSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return RenownResult{}, fmt.Errorf("decode renown search: %w", err)
	}
	gate := AuthorQuery{LastName: q.LastName, FirstName: q.FirstName}
	var out RenownResult
	for _, d := range sr.Docs {
		if !anyAuthorMatches(gate, d.AuthorName) {
			continue
		}
		out.Ratings += d.RatingsCount
		out.Want += d.WantToReadCount
	}
	if out.Ratings+out.Want <= 0 {
		return RenownResult{}, ErrNotFound
	}
	return out, nil
}

// FetchYear — год первого издания произведения из OpenLibrary search
// (поле first_publish_year). Реализует YearProvider. Это «дата выхода»
// произведения: OL отдаёт самый ранний год издания среди всех изданий
// work'а — то, что нужно для written_year.
func (p *OpenLibraryProvider) FetchYear(ctx context.Context, q BookQuery) (int, error) {
	if q.Title == "" {
		return 0, ErrNotFound
	}
	v := url.Values{}
	v.Set("title", q.Title)
	if len(q.Authors) > 0 {
		v.Set("author", q.Authors[0])
	}
	v.Set("limit", "1")
	v.Set("fields", "first_publish_year")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.searchURL+"?"+v.Encode(), nil)
	if err != nil {
		return 0, fmt.Errorf("build search request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("ol search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return 0, statusErr(resp.StatusCode)
	}

	var sr olSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return 0, fmt.Errorf("decode search: %w", err)
	}
	if len(sr.Docs) == 0 {
		return 0, ErrNotFound
	}
	y := sr.Docs[0].FirstPublishYear
	if y < 1000 || y > 2100 {
		return 0, ErrNotFound
	}
	return y, nil
}

// FetchAnnotation — два запроса:
//  1. /search.json → берём docs[0].key (например "/works/OL12345W");
//  2. GET {workKey}.json → поле description.
//
// description в OL — иногда string ("текст"), иногда object
// ({"type":"/type/text","value":"текст"}). Json.RawMessage + декодер
// разруливает оба.
func (p *OpenLibraryProvider) FetchAnnotation(ctx context.Context, q BookQuery) (string, error) {
	if q.Title == "" {
		return "", ErrNotFound
	}

	v := url.Values{}
	v.Set("title", q.Title)
	if len(q.Authors) > 0 {
		v.Set("author", q.Authors[0])
	}
	v.Set("limit", "1")
	v.Set("fields", "key,cover_i")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.searchURL+"?"+v.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("build search request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ol search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", statusErr(resp.StatusCode)
	}

	var sr olSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return "", fmt.Errorf("decode search: %w", err)
	}
	if len(sr.Docs) == 0 || sr.Docs[0].Key == "" {
		return "", ErrNotFound
	}

	workKey := sr.Docs[0].Key // "/works/OLxxxW"
	workReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		strings.TrimRight(p.workBaseURL(), "/")+workKey+".json", nil)
	if err != nil {
		return "", fmt.Errorf("build work request: %w", err)
	}
	workReq.Header.Set("Accept", "application/json")
	workResp, err := p.httpClient.Do(workReq)
	if err != nil {
		return "", fmt.Errorf("ol work: %w", err)
	}
	defer func() { _ = workResp.Body.Close() }()
	if workResp.StatusCode != http.StatusOK {
		return "", statusErr(workResp.StatusCode)
	}

	var work struct {
		Description any `json:"description"` // string или {type, value}
	}
	if err := json.NewDecoder(workResp.Body).Decode(&work); err != nil {
		return "", fmt.Errorf("decode work: %w", err)
	}
	desc := extractOLDescription(work.Description)
	if desc == "" {
		return "", ErrNotFound
	}
	return desc, nil
}

// workBaseURL — корень для /works/{OLID}.json. По умолчанию совпадает
// с openlibrary.org; в тестах подменяется через WithEndpoints.
func (p *OpenLibraryProvider) workBaseURL() string {
	// searchURL = "https://openlibrary.org/search.json" → срезаем
	// "/search.json" и получаем "https://openlibrary.org".
	if idx := strings.LastIndex(p.searchURL, "/search.json"); idx >= 0 {
		return p.searchURL[:idx]
	}
	return "https://openlibrary.org"
}

func extractOLDescription(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case map[string]any:
		if s, ok := x["value"].(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// ── Авторы: FetchAuthorBio / FetchAuthorPhoto ───────────────────
//
// Алгоритм:
//   1. GET /search/authors.json?q={full_name}&limit=1 → берём первый
//      docs[0].key (OLID типа "OL12345A").
//   2. GET /authors/{OLID}.json → bio (string или {value: string}) +
//      photos ([id1, id2, ...]).
//   3. Для фото: GET covers.openlibrary.org/a/id/{photo_id}-L.jpg.
//
// Hit rate для русских авторов в OL средненький — каталог
// англоцентричный. Но как fallback после Wikipedia работает: иногда
// у автора есть страница в OL без Wikipedia.

// authorSearch — общий шаг для bio и photo: ищем автора, возвращаем
// его OLID + parsed details.
func (p *OpenLibraryProvider) authorSearch(ctx context.Context, q AuthorQuery) (*olAuthor, error) {
	if q.FullName == "" {
		return nil, ErrNotFound
	}

	base := p.workBaseURL()
	v := url.Values{}
	v.Set("q", q.FullName)
	v.Set("limit", "1")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/search/authors.json?"+v.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build author search: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ol author search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, statusErr(resp.StatusCode)
	}

	var sr olAuthorSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("decode author search: %w", err)
	}
	if len(sr.Docs) == 0 || sr.Docs[0].Key == "" {
		return nil, ErrNotFound
	}
	// Гейт по имени: OL-поиск тоже может вернуть однофамильца. Принимаем только
	// если совпадает и имя (см. authorNameMatches) — иначе лучше пусто.
	if !authorNameMatches(q, sr.Docs[0].Name) {
		return nil, ErrNotFound
	}

	// Key может быть и просто "OL12345A", и "/authors/OL12345A". Нормализуем.
	olid := strings.TrimPrefix(sr.Docs[0].Key, "/authors/")

	detailReq, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/authors/"+olid+".json", nil)
	if err != nil {
		return nil, fmt.Errorf("build author detail: %w", err)
	}
	detailReq.Header.Set("Accept", "application/json")
	detailResp, err := p.httpClient.Do(detailReq)
	if err != nil {
		return nil, fmt.Errorf("ol author detail: %w", err)
	}
	defer func() { _ = detailResp.Body.Close() }()
	if detailResp.StatusCode != http.StatusOK {
		return nil, statusErr(detailResp.StatusCode)
	}

	var detail olAuthor
	if err := json.NewDecoder(detailResp.Body).Decode(&detail); err != nil {
		return nil, fmt.Errorf("decode author detail: %w", err)
	}
	detail.OLID = olid

	// Слой 2 (опционально): профессия P106. Имя-гейт пропускает однофамильца с
	// тем же ФИО, но другой профессией; QID берём бесплатно из remote_ids.wikidata
	// (доп. запрос не нужен). Отвергаем ТОЛЬКО явного не-писателя; нет QID /
	// unknown / ошибка сети — оставляем (precision-preserving, как на wiki-пути).
	// ⚠️ Важно для цепочки провайдеров [wikipedia, openlibrary]: если wiki-гейт
	// отверг однофамильца (ErrNotFound), enricher идёт к OL — без этого гейта OL
	// отдал бы того же не-писателя, и wiki-отказ «протёк» бы сюда.
	if p.occupationGate != nil && detail.RemoteIDs.Wikidata != "" {
		if v, err := p.occupationGate(ctx, detail.RemoteIDs.Wikidata); err == nil && v == OccupationNonWriter {
			return nil, ErrNotFound
		}
	}
	return &detail, nil
}

// FetchAuthorBio — bio из /authors/{OLID}.json.
func (p *OpenLibraryProvider) FetchAuthorBio(ctx context.Context, q AuthorQuery) (string, error) {
	a, err := p.authorSearch(ctx, q)
	if err != nil {
		return "", err
	}
	bio := extractOLDescription(a.Bio)
	if bio == "" {
		return "", ErrNotFound
	}
	return bio, nil
}

// FetchAuthorPhoto — первое фото из photos[] автора.
func (p *OpenLibraryProvider) FetchAuthorPhoto(ctx context.Context, q AuthorQuery) (*CoverImage, error) {
	a, err := p.authorSearch(ctx, q)
	if err != nil {
		return nil, err
	}
	// photos[i] = -1 у OL означает "удалено/нет", фильтруем.
	var photoID int64
	for _, id := range a.Photos {
		if id > 0 {
			photoID = id
			break
		}
	}
	if photoID == 0 {
		return nil, ErrNotFound
	}

	imgReq, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/a/id/%d-L.jpg", p.coverURL, photoID), nil)
	if err != nil {
		return nil, fmt.Errorf("build photo request: %w", err)
	}
	imgResp, err := p.httpClient.Do(imgReq)
	if err != nil {
		return nil, fmt.Errorf("ol photo: %w", err)
	}
	if imgResp.StatusCode != http.StatusOK {
		_ = imgResp.Body.Close()
		return nil, statusErr(imgResp.StatusCode)
	}
	mime := imgResp.Header.Get("Content-Type")
	if mime == "" || !strings.HasPrefix(mime, "image/") {
		mime = "image/jpeg"
	}
	return &CoverImage{
		Reader:   imgResp.Body,
		Mime:     mime,
		SourceID: fmt.Sprintf("ol:author-photo:%d", photoID),
	}, nil
}

type olAuthorSearchResponse struct {
	Docs []struct {
		Key  string `json:"key"`  // "/authors/OL12345A" или "OL12345A"
		Name string `json:"name"` // "Lev Tolstoy"
	} `json:"docs"`
}

type olAuthor struct {
	OLID      string  `json:"-"` // заполняем сами после search
	Bio       any     `json:"bio"`
	Photos    []int64 `json:"photos"`
	RemoteIDs struct {
		// Wikidata QID автора ("Q7243") — зацепка для слоя 2 (P106). У OL это
		// одно из многих remote_ids; пустая строка = OL не слинковал автора с
		// Wikidata (тогда гейт не зовём).
		Wikidata string `json:"wikidata"`
	} `json:"remote_ids"`
}
