package metadata

import (
	"context"
	"encoding/json"
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
		return nil, ErrNotFound
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
		return nil, ErrNotFound
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

type olSearchResponse struct {
	Docs []struct {
		CoverI int64  `json:"cover_i"`
		Key    string `json:"key"` // "/works/OL12345W"
	} `json:"docs"`
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
		return "", ErrNotFound
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
		return "", ErrNotFound
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
		return nil, ErrNotFound
	}

	var sr olAuthorSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("decode author search: %w", err)
	}
	if len(sr.Docs) == 0 || sr.Docs[0].Key == "" {
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
		return nil, ErrNotFound
	}

	var detail olAuthor
	if err := json.NewDecoder(detailResp.Body).Decode(&detail); err != nil {
		return nil, fmt.Errorf("decode author detail: %w", err)
	}
	detail.OLID = olid
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
		return nil, ErrNotFound
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
	OLID   string  `json:"-"` // заполняем сами после search
	Bio    any     `json:"bio"`
	Photos []int64 `json:"photos"`
}
