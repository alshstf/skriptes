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
