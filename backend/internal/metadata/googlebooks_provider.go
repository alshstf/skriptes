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

// GoogleBooksProvider — обложки/рейтинг/work-key через
// https://www.googleapis.com/books/v1/volumes.
//
// API-ключ ОБЯЗАТЕЛЕН для public-data запросов: без него GB отдаёт 429 «quota
// exceeded» по общей анонимной квоте (быстро исчерпывается). Ключ — из env
// SKRIPTES_GOOGLE_BOOKS_API_KEY (Google Cloud Console → Books API → API key),
// прокидывается через WithAPIKey; квота считается на проект (free ~1000/день).
type GoogleBooksProvider struct {
	httpClient *http.Client
	apiURL     string // override для тестов
	apiKey     string // SKRIPTES_GOOGLE_BOOKS_API_KEY; пусто = анонимно (429-prone)
	country    string // ISO 3166-1 alpha-2 для параметра country= (дефолт US)
}

// defaultGBCountry — страна по умолчанию для параметра country=. Для серверного/
// облачного деплоя country ОБЯЗАТЕЛЕН: без него GB не может геолоцировать IP и
// отдаёт "Cannot determine user location for geographically restricted operation"
// (пусто/ошибка) — недокументированное поведение, разбор:
// https://groups.google.com/g/google-appengine/c/C-IoRG7Z7VI. US даёт самую полную
// выдачу (в т.ч. чаще присутствует averageRating — рынок Google Play Books).
const defaultGBCountry = "US"

func NewGoogleBooksProvider(httpClient *http.Client) *GoogleBooksProvider {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	return &GoogleBooksProvider{
		httpClient: httpClient,
		apiURL:     "https://www.googleapis.com/books/v1/volumes",
		country:    defaultGBCountry,
	}
}

// WithEndpoint — для тестов.
func (p *GoogleBooksProvider) WithEndpoint(apiURL string) *GoogleBooksProvider {
	p.apiURL = apiURL
	return p
}

// WithAPIKey задаёт ключ Google Books API (параметр key= ко всем запросам).
// Пусто — без ключа (анонимная квота, упирается в 429).
func (p *GoogleBooksProvider) WithAPIKey(key string) *GoogleBooksProvider {
	p.apiKey = key
	return p
}

// WithCountry переопределяет ISO 3166-1 alpha-2 код для country= (пусто —
// оставить дефолт US; НЕ снимает параметр, т.к. без него GB ломается в облаке).
func (p *GoogleBooksProvider) WithCountry(code string) *GoogleBooksProvider {
	if code != "" {
		p.country = code
	}
	return p
}

// addParams доклеивает общие query-параметры ко ВСЕМ вызовам GB API: key= (если
// задан) и country= (обязателен для серверного деплоя). projection не трогаем —
// дефолт full (в lite нет averageRating/ratingsCount).
func (p *GoogleBooksProvider) addParams(v url.Values) {
	if p.apiKey != "" {
		v.Set("key", p.apiKey)
	}
	if p.country != "" {
		v.Set("country", p.country)
	}
}

func (p *GoogleBooksProvider) Name() string { return "googlebooks" }

func (p *GoogleBooksProvider) FetchCover(ctx context.Context, q BookQuery) (*CoverImage, error) {
	if q.Title == "" {
		return nil, ErrNotFound
	}

	var query strings.Builder
	query.WriteString(`intitle:"`)
	query.WriteString(q.Title)
	query.WriteString(`"`)
	if len(q.Authors) > 0 {
		query.WriteString(` inauthor:"`)
		query.WriteString(q.Authors[0])
		query.WriteString(`"`)
	}

	v := url.Values{}
	v.Set("q", query.String())
	v.Set("maxResults", "1")
	p.addParams(v)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.apiURL+"?"+v.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("gb search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, statusErr(resp.StatusCode)
	}

	var sr gbSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return nil, fmt.Errorf("decode search: %w", err)
	}
	if len(sr.Items) == 0 {
		return nil, ErrNotFound
	}

	thumb := pickGoogleBooksThumbnail(sr.Items[0].VolumeInfo.ImageLinks)
	if thumb == "" {
		return nil, ErrNotFound
	}

	// Google часто возвращает http-thumbnail; не переписываем в https
	// принудительно: картинку мы кэшируем у себя и потом отдаём по
	// HTTPS клиенту через /api/covers/. Mixed content на стороне
	// браузера тут не релевантен.
	coverReq, err := http.NewRequestWithContext(ctx, http.MethodGet, thumb, nil)
	if err != nil {
		return nil, fmt.Errorf("build cover request: %w", err)
	}
	coverResp, err := p.httpClient.Do(coverReq)
	if err != nil {
		return nil, fmt.Errorf("gb cover: %w", err)
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
		Reader:   coverResp.Body,
		Mime:     mime,
		SourceID: fmt.Sprintf("gb:%s", sr.Items[0].ID),
	}, nil
}

type gbSearchResponse struct {
	Items []struct {
		ID         string `json:"id"`
		VolumeInfo struct {
			ImageLinks    map[string]string `json:"imageLinks"`
			Description   string            `json:"description"`
			AverageRating float64           `json:"averageRating"` // шкала 1–5
			RatingsCount  int               `json:"ratingsCount"`
		} `json:"volumeInfo"`
	} `json:"items"`
}

// FetchRating — внешний рейтинг через /volumes: volumeInfo.averageRating (1–5)
// + ratingsCount. Запрос ISBN-first (точнее), иначе по названию+автору. Первый
// item не всегда несёт рейтинг — берём первый с averageRating>0.
func (p *GoogleBooksProvider) FetchRating(ctx context.Context, q WorkQuery) (RatingResult, error) {
	query := gbRatingQuery(q)
	if query == "" {
		return RatingResult{}, ErrNotFound
	}
	v := url.Values{}
	v.Set("q", query)
	v.Set("maxResults", "5")
	p.addParams(v) // key + country; без ключа → аноним 429, без country → geo-ошибка в облаке
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.apiURL+"?"+v.Encode(), nil)
	if err != nil {
		return RatingResult{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return RatingResult{}, fmt.Errorf("gb rating search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return RatingResult{}, statusErr(resp.StatusCode)
	}
	var sr gbSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return RatingResult{}, fmt.Errorf("decode rating search: %w", err)
	}
	for _, it := range sr.Items {
		if it.VolumeInfo.AverageRating > 0 {
			return RatingResult{Average: it.VolumeInfo.AverageRating, Count: it.VolumeInfo.RatingsCount}, nil
		}
	}
	return RatingResult{}, ErrNotFound
}

// gbRatingQuery — строка q для поиска рейтинга: ISBN (точнее) → иначе
// intitle/inauthor.
func gbRatingQuery(q WorkQuery) string {
	if isbn := normalizeISBN(q.ISBN); isbn != "" {
		return "isbn:" + isbn
	}
	title := q.Title
	if title == "" {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(`intitle:"`)
	sb.WriteString(title)
	sb.WriteString(`"`)
	if len(q.Authors) > 0 {
		sb.WriteString(` inauthor:"`)
		sb.WriteString(q.Authors[0])
		sb.WriteString(`"`)
	}
	return sb.String()
}

// FetchAnnotation — один запрос на /volumes; description в первом
// item'е. Google присылает plain-text c \n внутри, ничего экранировать
// не нужно.
func (p *GoogleBooksProvider) FetchAnnotation(ctx context.Context, q BookQuery) (string, error) {
	if q.Title == "" {
		return "", ErrNotFound
	}

	var query strings.Builder
	query.WriteString(`intitle:"`)
	query.WriteString(q.Title)
	query.WriteString(`"`)
	if len(q.Authors) > 0 {
		query.WriteString(` inauthor:"`)
		query.WriteString(q.Authors[0])
		query.WriteString(`"`)
	}

	v := url.Values{}
	v.Set("q", query.String())
	v.Set("maxResults", "1")
	p.addParams(v)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.apiURL+"?"+v.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gb search: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", statusErr(resp.StatusCode)
	}

	var sr gbSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return "", fmt.Errorf("decode search: %w", err)
	}
	if len(sr.Items) == 0 {
		return "", ErrNotFound
	}
	desc := strings.TrimSpace(sr.Items[0].VolumeInfo.Description)
	if desc == "" {
		return "", ErrNotFound
	}
	return desc, nil
}

// pickGoogleBooksThumbnail — выбирает самую большую доступную картинку.
// Google возвращает разные размеры (smallThumbnail/thumbnail/small/...);
// чем больше — тем лучше выглядит на сетчатке.
func pickGoogleBooksThumbnail(links map[string]string) string {
	// Порядок убывания качества.
	for _, key := range []string{"extraLarge", "large", "medium", "small", "thumbnail", "smallThumbnail"} {
		if v, ok := links[key]; ok && v != "" {
			return v
		}
	}
	return ""
}
