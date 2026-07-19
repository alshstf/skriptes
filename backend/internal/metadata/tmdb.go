package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// TMDBPosterProvider — постеры экранизаций из The Movie Database (TMDB).
//
// Почему TMDB: Wikidata-источник адаптаций даёт картинку только через P18
// (Commons), а постеры фильмов — копирайтный контент, Commons их почти не
// хостит (на реальном проде P18 есть лишь у ~16% экранизаций; у «Разум и
// чувства» 1995 и «Ежика» 2010 — нет вовсе). TMDB — тот же источник, откуда
// постеры берёт агент метаданных Plex: покрытие постеров близко к полному.
//
// Резолв БЕЗ поиска: TMDB-id приходит бесплатно из того же SPARQL-ответа
// адаптаций (P4947 «TMDb film ID» / P4983 «TMDb TV series ID») — ложных
// матчей по названию нет by design.
//
// API: GET /3/movie/{id} | /3/tv/{id} с ключом (v3 api_key). Ключ бесплатный
// (регистрация на themoviedb.org), без ключа API отдаёт 401. Условие TMDB —
// атрибуция «This product uses the TMDB API but is not endorsed or certified
// by TMDB» (см. README). Rate-limit TMDB (~50 rps) на порядки выше темпа
// воркера экранизаций — отдельный gate не нужен.
type TMDBPosterProvider struct {
	apiKey     string
	baseURL    string // https://api.themoviedb.org
	imageBase  string // https://image.tmdb.org
	httpClient *http.Client
	gate       *rateGate // собственный потолок TMDB — см. tmdbRPM
}

// tmdbRPM — жёсткий внутренний потолок вызовов TMDB API (зеркало clampOLRPM у
// OpenLibrary: не настраивается из админки осознанно). Гейт живёт В ПРОВАЙДЕРЕ,
// поэтому его проходят ВСЕ пути — первичный фетч воркера, lazy при открытии
// карточки и авто-перепроверка постер-дыр: сколько бы ни накрутили RPM воркера
// «Экранизации» (книг/мин × несколько адаптаций на книгу), суммарный темп к
// TMDB не превысит этот предел. 600/мин = 10 req/s — в 5 раз ниже
// документированного лимита TMDB (~50 req/s, developer.themoviedb.org/docs/
// rate-limiting) и с запасом выше любого нашего штатного темпа.
const tmdbRPM = 600

// NewTMDBPosterProvider создаёт провайдер. Пустой ключ допустим на уровне
// типа, но main не конструирует провайдер без ключа (без него TMDB → 401).
func NewTMDBPosterProvider(apiKey string) *TMDBPosterProvider {
	p := &TMDBPosterProvider{
		apiKey:     apiKey,
		baseURL:    "https://api.themoviedb.org",
		imageBase:  "https://image.tmdb.org",
		httpClient: &http.Client{Timeout: 15 * time.Second},
		gate:       &rateGate{},
	}
	p.gate.setRPM(tmdbRPM)
	return p
}

// WithBaseURLs — для тестов: подменить API и image-хосты на httptest.
func (p *TMDBPosterProvider) WithBaseURLs(api, image string) *TMDBPosterProvider {
	if api != "" {
		p.baseURL = api
	}
	if image != "" {
		p.imageBase = image
	}
	return p
}

// PosterURL — URL постера по TMDB-id фильма ИЛИ сериала (первый непустой).
// Возвращает:
//   - url, nil — постер есть;
//   - "", ErrNotFound — фильм неизвестен TMDB (404) или постера честно нет
//     (poster_path null) — перепрашивать бессмысленно;
//   - "", ErrUpstream — транзиент (429/5xx/сеть/битый ключ) — попытку не
//     считать окончательной;
//   - "", nil — id не передан (у адаптации нет P4947/P4983).
//
// w342 — достаточно для карточной сетки (плитка ~200px, retina ×2 покрыта).
func (p *TMDBPosterProvider) PosterURL(ctx context.Context, movieID, tvID string) (string, error) {
	var path string
	switch {
	case movieID != "":
		path = "/3/movie/" + movieID
	case tvID != "":
		path = "/3/tv/" + tvID
	default:
		return "", nil
	}
	// Собственный потолок TMDB (tmdbRPM) — до сети, на всех путях вызова.
	if err := p.gate.wait(ctx); err != nil {
		return "", fmt.Errorf("%w: tmdb gate: %v", ErrUpstream, err)
	}

	// TMDB выдаёт ДВА креденшала: короткий v3 «API Key» (query-параметр
	// api_key) и длинный v4 «API Read Access Token» (JWT, всегда начинается с
	// "eyJ", идёт Bearer-заголовком). Принимаем ЛЮБОЙ — перепутать их на
	// странице настроек TMDB слишком легко, а v4-токен там заметнее.
	u := p.baseURL + path
	bearer := strings.HasPrefix(p.apiKey, "eyJ")
	if !bearer {
		u += "?api_key=" + p.apiKey
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", fmt.Errorf("build tmdb request: %w", err)
	}
	if bearer {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", wdUserAgent)

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: tmdb: %v", ErrUpstream, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", statusErr(resp.StatusCode)
	}

	var body struct {
		PosterPath string `json:"poster_path"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("%w: decode tmdb: %v", ErrUpstream, err)
	}
	if body.PosterPath == "" {
		return "", ErrNotFound
	}
	return p.imageBase + "/t/p/w342" + body.PosterPath, nil
}
