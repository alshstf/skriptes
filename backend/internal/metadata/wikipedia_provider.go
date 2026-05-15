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

// WikipediaProvider — био+фото авторов через Wikipedia REST API.
//
// Алгоритм:
//  1. opensearch на ru.wikipedia.org / en.wikipedia.org с полным именем
//     → получаем точное название страницы (например "Достоевский,_Фёдор_Михайлович").
//  2. /api/rest_v1/page/summary/{title} → JSON c "extract" (био) и
//     "thumbnail.source" (URL картинки).
//
// Сначала пробуем язык q.Lang (с дефолтом ru), потом — английский.
// Это даёт нормальный хит-rate для русских и переводных авторов.
type WikipediaProvider struct {
	httpClient *http.Client
	apiRoot    string // override для тестов; продакшен — пустая (используем https://{lang}.wikipedia.org)
}

func NewWikipediaProvider(httpClient *http.Client) *WikipediaProvider {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &WikipediaProvider{httpClient: httpClient}
}

// WithAPIRoot переопределяет корень API (для httptest-серверов).
// Формат: "http://127.0.0.1:1234" — без trailing slash; провайдер
// сам добавит "/w/api.php" и "/api/rest_v1/page/summary/...".
//
// При непустом apiRoot язык игнорируется — мы пользуемся одним
// сервером для всех "языков" в тестах.
func (p *WikipediaProvider) WithAPIRoot(root string) *WikipediaProvider {
	p.apiRoot = root
	return p
}

func (p *WikipediaProvider) Name() string { return "wikipedia" }

func (p *WikipediaProvider) FetchAuthorBio(ctx context.Context, q AuthorQuery) (string, error) {
	for _, lang := range p.langs(q.Lang) {
		s, err := p.summary(ctx, lang, q.FullName)
		if err != nil {
			continue
		}
		if s.Extract != "" {
			return strings.TrimSpace(s.Extract), nil
		}
	}
	return "", ErrNotFound
}

func (p *WikipediaProvider) FetchAuthorPhoto(ctx context.Context, q AuthorQuery) (*CoverImage, error) {
	for _, lang := range p.langs(q.Lang) {
		s, err := p.summary(ctx, lang, q.FullName)
		if err != nil {
			continue
		}
		if s.Thumbnail.Source == "" {
			continue
		}
		img, err := p.downloadImage(ctx, s.Thumbnail.Source)
		if err != nil {
			continue
		}
		return img, nil
	}
	return nil, ErrNotFound
}

// summary — opensearch для точного титла + summary endpoint.
func (p *WikipediaProvider) summary(ctx context.Context, lang, name string) (*wikiSummary, error) {
	title, err := p.resolveTitle(ctx, lang, name)
	if err != nil {
		return nil, err
	}
	if title == "" {
		return nil, ErrNotFound
	}

	summaryURL := p.baseURL(lang) + "/api/rest_v1/page/summary/" + url.PathEscape(title)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, summaryURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build summary request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wikipedia summary: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, ErrNotFound
	}
	var s wikiSummary
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return nil, fmt.Errorf("decode summary: %w", err)
	}
	// disambiguation-страницы (type="disambiguation") нам бесполезны —
	// extract там обычно общий типа "может означать...".
	if s.Type == "disambiguation" {
		return nil, ErrNotFound
	}
	return &s, nil
}

// resolveTitle — через opensearch получает первый match.
func (p *WikipediaProvider) resolveTitle(ctx context.Context, lang, name string) (string, error) {
	v := url.Values{}
	v.Set("action", "opensearch")
	v.Set("search", name)
	v.Set("limit", "1")
	v.Set("namespace", "0") // только статьи, не категории
	v.Set("format", "json")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL(lang)+"/w/api.php?"+v.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("build opensearch: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("wikipedia opensearch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", ErrNotFound
	}
	// opensearch отдаёт массив [string, []string, []string, []string]:
	// [запрос, [титулы], [сниппеты], [ссылки]]. Декодим как json.RawMessage'ы.
	var arr []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&arr); err != nil {
		return "", fmt.Errorf("decode opensearch: %w", err)
	}
	if len(arr) < 2 {
		return "", ErrNotFound
	}
	var titles []string
	if err := json.Unmarshal(arr[1], &titles); err != nil {
		return "", fmt.Errorf("decode titles: %w", err)
	}
	if len(titles) == 0 {
		return "", ErrNotFound
	}
	return titles[0], nil
}

func (p *WikipediaProvider) downloadImage(ctx context.Context, src string) (*CoverImage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return nil, fmt.Errorf("build image request: %w", err)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("wikipedia image: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, ErrNotFound
	}
	mime := resp.Header.Get("Content-Type")
	if mime == "" || !strings.HasPrefix(mime, "image/") {
		mime = "image/jpeg"
	}
	return &CoverImage{
		Reader:   resp.Body,
		Mime:     mime,
		SourceID: "wp:" + src,
	}, nil
}

// langs — в каком порядке пробовать языковые Wikipedia.
// Логика: сначала "родной" (q.Lang), потом противоположный из ru/en.
// Если q.Lang пустой — ru приоритетнее (наш каталог преимущественно
// русскоязычный).
func (p *WikipediaProvider) langs(qLang string) []string {
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

// baseURL — корень API для конкретного языка. При apiRoot != "" (тест)
// возвращаем его как есть.
func (p *WikipediaProvider) baseURL(lang string) string {
	if p.apiRoot != "" {
		return p.apiRoot
	}
	return "https://" + lang + ".wikipedia.org"
}

type wikiSummary struct {
	Title     string `json:"title"`
	Type      string `json:"type"` // "standard" / "disambiguation" / ...
	Extract   string `json:"extract"`
	Thumbnail struct {
		Source string `json:"source"`
	} `json:"thumbnail"`
}
