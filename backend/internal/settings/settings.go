// Package settings — рантайм-настройки приложения, хранящиеся в таблице
// app_settings (generic key/value JSONB) и редактируемые из админки без
// рестарта. Дефолты живут в коде; в БД лежат только оверрайды.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const coverKey = "cover_cache"

// CoverConfig — рантайм-настройки фоновой ОБРАБОТКИ КОЛЛЕКЦИИ (парсинг fb2:
// обложки + аннотации + года) и кэша обложек. Ключ app_settings остался
// `cover_cache` ради совместимости; исторически тут жил только прогрев обложек,
// теперь — вся локальная джоба.
//
//	Prewarm         — МАСТЕР-тумблер: вкл/выкл фоновую обработку коллекции.
//	SyncCovers      — извлекать обложки из fb2 (под ним — лимиты кэша).
//	SyncAnnotations — извлекать аннотации из fb2.
//	SyncYears       — извлекать года написания/издания из fb2.
//	Intensity       — нагрузка на IO: "low" | "medium" | "high"
//	                  (число воркеров + пауза между книгами; для HDD vs NVMe).
//	CacheMaxMB      — бюджет дискового кэша обложек (LRU-эвикция); 0 = без лимита.
//	CacheMinFreeMB  — пол свободного места: ниже него обложки не пишутся.
type CoverConfig struct {
	CacheMaxMB      int    `json:"cache_max_mb"`
	CacheMinFreeMB  int    `json:"cache_min_free_mb"`
	Prewarm         bool   `json:"prewarm"`
	SyncCovers      bool   `json:"sync_covers"`
	SyncAnnotations bool   `json:"sync_annotations"`
	SyncYears       bool   `json:"sync_years"`
	Intensity       string `json:"intensity"`
	// Бюджеты НЕрегенерируемых бакетов (постеры экранизаций, фото авторов) —
	// отдельно от обложек книг: их «Очистить кэш обложек»/LRU не трогают.
	// 0 = без лимита (дефолт): эвиктить нерегенерируемое вредно, а объём мал.
	PosterCacheMaxMB int `json:"poster_cache_max_mb"`
	PhotoCacheMaxMB  int `json:"photo_cache_max_mb"`
}

// DefaultCoverConfig — безопасные дефолты. Мастер выключен; при включении
// синкаются все три типа (как раньше прогрев делал всё разом); интенсивность
// средняя; кэш ограничен, есть пол свободного места.
func DefaultCoverConfig() CoverConfig {
	return CoverConfig{
		CacheMaxMB:       8192,
		CacheMinFreeMB:   1024,
		Prewarm:          false,
		SyncCovers:       true,
		SyncAnnotations:  true,
		SyncYears:        true,
		Intensity:        IntensityMedium,
		PosterCacheMaxMB: 0,
		PhotoCacheMaxMB:  0,
	}
}

// PosterCacheMaxBytes / PhotoCacheMaxBytes — лимиты бакетов в байтах (0 = без).
func (c CoverConfig) PosterCacheMaxBytes() int64 { return int64(c.PosterCacheMaxMB) << 20 }
func (c CoverConfig) PhotoCacheMaxBytes() int64  { return int64(c.PhotoCacheMaxMB) << 20 }

// Пресеты интенсивности обработки коллекции.
const (
	IntensityLow    = "low"
	IntensityMedium = "medium"
	IntensityHigh   = "high"
)

// IntensityWorkers — число параллельных воркеров прогрева для пресета.
func (c CoverConfig) IntensityWorkers() int {
	switch c.Intensity {
	case IntensityLow:
		return 1
	case IntensityHigh:
		return 6
	default: // medium и любое неизвестное
		return 2
	}
}

// IntensityDelay — пауза между книгами (троттлинг IO). На low — заметная,
// чтобы не душить медленный диск; на medium/high — без паузы.
func (c CoverConfig) IntensityDelay() time.Duration {
	if c.Intensity == IntensityLow {
		return 250 * time.Millisecond
	}
	return 0
}

// MinFreeBytes — порог свободного места в байтах.
func (c CoverConfig) MinFreeBytes() int64 { return int64(c.CacheMinFreeMB) << 20 }

// EffectiveCacheMaxBytes — фактический лимит кэша в байтах.
//
// При включённом прогреве возвращает 0 («без лимита», full-store): прогрев
// заполняет обложки всей коллекции, и LRU-эвикция по бюджету привела бы к
// бесконечной мясорубке (записал → вытеснил → записал) на коллекции
// больше бюджета. В режиме прогрева рост ограничивает только порог
// свободного места. Без прогрева — заданный бюджет (bounded LRU).
func (c CoverConfig) EffectiveCacheMaxBytes() int64 {
	if c.Prewarm {
		return 0
	}
	return int64(c.CacheMaxMB) << 20
}

// Store — доступ к app_settings.
type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Cover читает настройки кэша обложек; если оверрайда в БД нет — отдаёт
// дефолты. Поля, отсутствующие в JSON, остаются дефолтными (мердж поверх
// DefaultCoverConfig через json.Unmarshal в уже-заполненную структуру).
func (s *Store) Cover(ctx context.Context) (CoverConfig, error) {
	cfg := DefaultCoverConfig()
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, coverKey).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read cover settings: %w", err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return DefaultCoverConfig(), fmt.Errorf("decode cover settings: %w", err)
	}
	return cfg, nil
}

// SetCover сохраняет настройки кэша обложек (upsert).
func (s *Store) SetCover(ctx context.Context, cfg CoverConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode cover settings: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, coverKey, raw)
	if err != nil {
		return fmt.Errorf("save cover settings: %w", err)
	}
	return nil
}

const yearEnrichmentKey = "year_enrichment"

// YearEnrichmentConfig — настройки фонового дозаполнения written_year из
// внешних источников (OpenLibrary, Wikidata). Воркер opt-in (Enabled=false
// по умолчанию): он ходит в публичные API, поэтому включается осознанно из
// админки.
//
//	OpenLibrary / Wikidata — какие источники опрашивать (можно отключить
//	                         шумящий). Порядок приоритета фиксирован в коде:
//	                         OpenLibrary (first_publish_year) → Wikidata (P577).
//	WholeCollection         — режим охвата. false (дефолт) = фолбэк: дозаполнять
//	                         только книги, у которых локальная fb2-фаза уже
//	                         прошла, но год не дала (year_local_scanned_at NOT
//	                         NULL). true = вся коллекция: спрашивать внешние и для
//	                         книг, которых fb2-проход не касался (очень долго,
//	                         opt-in за дисклеймером).
//	*RPM                    — лимит запросов в минуту на источник (вежливость
//	                         к публичным API; OL ~мягко, Wikidata строже).
//	NotFoundRetryDays       — через сколько перепроверять источник, ранее
//	                         вернувший not_found (данные со временем дополняются).
//	ErrorRetryHours         — через сколько ретраить источник после ошибки
//	                         (транзиентные 429/таймауты — быстрее, чем not_found).
type YearEnrichmentConfig struct {
	Enabled           bool `json:"enabled"`
	OpenLibrary       bool `json:"openlibrary"`
	Wikidata          bool `json:"wikidata"`
	WholeCollection   bool `json:"whole_collection"`
	OpenLibraryRPM    int  `json:"openlibrary_rpm"`
	WikidataRPM       int  `json:"wikidata_rpm"`
	NotFoundRetryDays int  `json:"not_found_retry_days"`
	ErrorRetryHours   int  `json:"error_retry_hours"`
}

// DefaultYearEnrichmentConfig — воркер выключен (opt-in), оба источника
// включены, режим фолбэка (не вся коллекция), вежливые rate-limit'ы и TTL.
func DefaultYearEnrichmentConfig() YearEnrichmentConfig {
	return YearEnrichmentConfig{
		Enabled:           false,
		OpenLibrary:       true,
		Wikidata:          true,
		WholeCollection:   false,
		OpenLibraryRPM:    60, // политика OL 2026-05: 1 req/s анонимно, 3 req/s с UA
		WikidataRPM:       20,
		NotFoundRetryDays: 90,
		ErrorRetryHours:   24,
	}
}

// YearEnrichment читает настройки дозаполнения года; нет оверрайда в БД —
// отдаёт дефолты (мердж поверх DefaultYearEnrichmentConfig).
func (s *Store) YearEnrichment(ctx context.Context) (YearEnrichmentConfig, error) {
	cfg := DefaultYearEnrichmentConfig()
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, yearEnrichmentKey).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read year enrichment settings: %w", err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return DefaultYearEnrichmentConfig(), fmt.Errorf("decode year enrichment settings: %w", err)
	}
	return cfg, nil
}

// SetYearEnrichment сохраняет настройки дозаполнения года (upsert).
func (s *Store) SetYearEnrichment(ctx context.Context, cfg YearEnrichmentConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode year enrichment settings: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, yearEnrichmentKey, raw)
	if err != nil {
		return fmt.Errorf("save year enrichment settings: %w", err)
	}
	return nil
}

const coverEnrichmentKey = "cover_enrichment"

// CoverEnrichmentConfig — настройки фонового дозаполнения cover_path из
// внешних источников (OpenLibrary, Google Books). Зеркало
// YearEnrichmentConfig, только источники другие (обложки берём из OL/GB, не
// из Wikidata). Воркер opt-in (Enabled=false по умолчанию): ходит в публичные
// API, включается осознанно из админки.
//
//	OpenLibrary / GoogleBooks — какие источники опрашивать. Порядок приоритета
//	                            фиксирован в коде: OpenLibrary → Google Books.
//	WholeCollection           — режим охвата. false (дефолт) = фолбэк:
//	                            дозаполнять только книги, у которых локальная
//	                            fb2-фаза уже прошла, но обложку не дала
//	                            (metadata_fetched_at NOT NULL). true = вся
//	                            коллекция: спрашивать внешние и для книг, которых
//	                            fb2-проход не касался (тысячи запросов к OL/GB,
//	                            очень долго; opt-in за дисклеймером).
//	*RPM                      — лимит запросов в минуту на источник.
//	NotFoundRetryDays         — TTL перепроверки источника после not_found.
//	ErrorRetryHours           — TTL ретрая источника после ошибки.
type CoverEnrichmentConfig struct {
	Enabled           bool `json:"enabled"`
	OpenLibrary       bool `json:"openlibrary"`
	GoogleBooks       bool `json:"googlebooks"`
	WholeCollection   bool `json:"whole_collection"`
	OpenLibraryRPM    int  `json:"openlibrary_rpm"`
	GoogleBooksRPM    int  `json:"googlebooks_rpm"`
	NotFoundRetryDays int  `json:"not_found_retry_days"`
	ErrorRetryHours   int  `json:"error_retry_hours"`
}

// DefaultCoverEnrichmentConfig — воркер выключен (opt-in), оба источника
// включены, режим фолбэка (не вся коллекция), вежливые rate-limit'ы и TTL.
func DefaultCoverEnrichmentConfig() CoverEnrichmentConfig {
	return CoverEnrichmentConfig{
		Enabled:     false,
		OpenLibrary: true,
		GoogleBooks: true,
		// WholeCollection: false — по умолчанию только пробелы.
		WholeCollection: false,
		// OL covers API: документированный лимит 100/IP/5мин (= 20/мин, сверх → 403).
		// 18 с запасом; воркер дополнительно клампит (olCoverRPMCap).
		OpenLibraryRPM:    18,
		GoogleBooksRPM:    60,
		NotFoundRetryDays: 90,
		ErrorRetryHours:   24,
	}
}

// CoverEnrichment читает настройки дозаполнения обложек; нет оверрайда в БД —
// отдаёт дефолты (мердж поверх DefaultCoverEnrichmentConfig).
func (s *Store) CoverEnrichment(ctx context.Context) (CoverEnrichmentConfig, error) {
	cfg := DefaultCoverEnrichmentConfig()
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, coverEnrichmentKey).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read cover enrichment settings: %w", err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return DefaultCoverEnrichmentConfig(), fmt.Errorf("decode cover enrichment settings: %w", err)
	}
	return cfg, nil
}

// SetCoverEnrichment сохраняет настройки дозаполнения обложек (upsert).
func (s *Store) SetCoverEnrichment(ctx context.Context, cfg CoverEnrichmentConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode cover enrichment settings: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, coverEnrichmentKey, raw)
	if err != nil {
		return fmt.Errorf("save cover enrichment settings: %w", err)
	}
	return nil
}

const renownEnrichmentKey = "renown_enrichment"

// RenownConfig — настройки фонового дозаполнения счётчиков «известности» работ
// (works.fantlab_marks / ol_ratings_count / ol_want_count) из Fantlab и
// OpenLibrary — сигналы интегральной популярности (сортировка/ранжирование).
// Зеркало ExternalRatingConfig, но work-level. WholeCollection: false = только
// ядро коллекции (≥2 изданий ∪ экранизация ∪ LIBRATE), true = все работы.
// FoundRefreshDays — TTL освежения найденных счётчиков (известность растёт).
type RenownConfig struct {
	Enabled           bool `json:"enabled"`
	Fantlab           bool `json:"fantlab"`
	OpenLibrary       bool `json:"openlibrary"`
	Wikidata          bool `json:"wikidata"`
	WholeCollection   bool `json:"whole_collection"`
	FantlabRPM        int  `json:"fantlab_rpm"`
	OpenLibraryRPM    int  `json:"openlibrary_rpm"`
	WikidataRPM       int  `json:"wikidata_rpm"`
	FoundRefreshDays  int  `json:"found_refresh_days"`
	NotFoundRetryDays int  `json:"not_found_retry_days"`
	ErrorRetryHours   int  `json:"error_retry_hours"`
}

// DefaultRenownConfig — воркер выключен (opt-in), оба источника включены,
// охват «ядро коллекции», вежливые rate-limit'ы и TTL.
func DefaultRenownConfig() RenownConfig {
	return RenownConfig{
		Enabled:           false,
		Fantlab:           true,
		OpenLibrary:       true,
		Wikidata:          true,
		WholeCollection:   false,
		FantlabRPM:        30, // лимиты api.fantlab.ru не документированы — вежливо
		OpenLibraryRPM:    60, // политика OL 2026-05: 1 req/s анонимно, 3 req/s с UA
		WikidataRPM:       20, // как у года/группировки (глобальный бюджет 200/мин с UA)
		FoundRefreshDays:  180,
		NotFoundRetryDays: 90,
		ErrorRetryHours:   24,
	}
}

// Renown читает настройки дозаполнения известности; нет оверрайда в БД —
// отдаёт дефолты (мердж поверх DefaultRenownConfig).
func (s *Store) Renown(ctx context.Context) (RenownConfig, error) {
	cfg := DefaultRenownConfig()
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, renownEnrichmentKey).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read renown settings: %w", err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return DefaultRenownConfig(), fmt.Errorf("decode renown settings: %w", err)
	}
	return cfg, nil
}

// SetRenown сохраняет настройки дозаполнения известности (upsert).
func (s *Store) SetRenown(ctx context.Context, cfg RenownConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode renown settings: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, renownEnrichmentKey, raw); err != nil {
		return fmt.Errorf("save renown settings: %w", err)
	}
	return nil
}

const externalRatingEnrichmentKey = "external_rating_enrichment"

// ExternalRatingConfig — настройки фонового дозаполнения books.external_rating
// из внешних источников (Google Books, OpenLibrary). Зеркало
// CoverEnrichmentConfig. Воркер opt-in (Enabled=false): ходит в публичные API,
// включается осознанно из админки. Источники выбираются независимо (можно
// оставить только Google Books). WholeCollection: false = только пробелы (книги
// без любого рейтинга), true = вся коллекция (даже книги с LIBRATE).
type ExternalRatingConfig struct {
	Enabled           bool `json:"enabled"`
	GoogleBooks       bool `json:"googlebooks"`
	OpenLibrary       bool `json:"openlibrary"`
	WholeCollection   bool `json:"whole_collection"`
	GoogleBooksRPM    int  `json:"googlebooks_rpm"`
	OpenLibraryRPM    int  `json:"openlibrary_rpm"`
	NotFoundRetryDays int  `json:"not_found_retry_days"`
	ErrorRetryHours   int  `json:"error_retry_hours"`
}

// DefaultExternalRatingConfig — воркер выключен (opt-in), оба источника включены,
// режим «только пробелы», вежливые rate-limit'ы и TTL.
func DefaultExternalRatingConfig() ExternalRatingConfig {
	return ExternalRatingConfig{
		Enabled:           false,
		GoogleBooks:       true,
		OpenLibrary:       true,
		WholeCollection:   false,
		GoogleBooksRPM:    60,
		OpenLibraryRPM:    60, // политика OL 2026-05: 1 req/s анонимно, 3 req/s с UA
		NotFoundRetryDays: 90,
		ErrorRetryHours:   24,
	}
}

// ExternalRating читает настройки дозаполнения рейтинга; нет оверрайда в БД —
// отдаёт дефолты (мердж поверх DefaultExternalRatingConfig).
func (s *Store) ExternalRating(ctx context.Context) (ExternalRatingConfig, error) {
	cfg := DefaultExternalRatingConfig()
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, externalRatingEnrichmentKey).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read external rating settings: %w", err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return DefaultExternalRatingConfig(), fmt.Errorf("decode external rating settings: %w", err)
	}
	return cfg, nil
}

// SetExternalRating сохраняет настройки дозаполнения рейтинга (upsert).
func (s *Store) SetExternalRating(ctx context.Context, cfg ExternalRatingConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode external rating settings: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, externalRatingEnrichmentKey, raw)
	if err != nil {
		return fmt.Errorf("save external rating settings: %w", err)
	}
	return nil
}

const workGroupingKey = "work_grouping"

// WorkGroupingConfig — настройки фоновой ГРУППИРОВКИ изданий в логические книги
// (works). Зеркало YearEnrichmentConfig.
//
// Tier-1 (внутриязыковой дедуп + межъязыковой через <src-title-info>) —
// локальный, без сети, идёт всегда когда воркер включён. Tier-2 (резолв
// внешнего Work ID: OpenLibrary Work / Wikidata) ходит в публичные API,
// поэтому source-флаги гейтят ТОЛЬКО его; мастер-тумблер Enabled включает
// джобу целиком (opt-in, по умолчанию выключен).
//
//	OpenLibrary / Wikidata — внешние источники Work ID (Tier-2). Оба off →
//	                         работает только Tier-1 (без сети).
//	WholeCollection         — режим охвата. false (дефолт) = только книги,
//	                         у которых уже прошёл локальный edition-скан
//	                         (edition_meta_scanned_at NOT NULL — есть src-ключи).
//	                         true = все непросканированные (work_scanned_at NULL).
//	*RPM / *RetryDays/Hours — вежливость к внешним API + TTL перепроверки
//	                         (book_work_lookups), как у года/обложек.
type WorkGroupingConfig struct {
	Enabled           bool `json:"enabled"`
	OpenLibrary       bool `json:"openlibrary"`
	Wikidata          bool `json:"wikidata"`
	WholeCollection   bool `json:"whole_collection"`
	OpenLibraryRPM    int  `json:"openlibrary_rpm"`
	WikidataRPM       int  `json:"wikidata_rpm"`
	NotFoundRetryDays int  `json:"not_found_retry_days"`
	ErrorRetryHours   int  `json:"error_retry_hours"`
}

// DefaultWorkGroupingConfig — воркер выключен (opt-in), оба внешних источника
// включены (но работают лишь при Enabled), режим фолбэка, вежливые лимиты/TTL.
func DefaultWorkGroupingConfig() WorkGroupingConfig {
	return WorkGroupingConfig{
		Enabled:           false,
		OpenLibrary:       true,
		Wikidata:          true,
		WholeCollection:   false,
		OpenLibraryRPM:    60, // политика OL 2026-05: 1 req/s анонимно, 3 req/s с UA
		WikidataRPM:       20,
		NotFoundRetryDays: 90,
		ErrorRetryHours:   24,
	}
}

// WorkGrouping читает настройки группировки; нет оверрайда в БД — дефолты
// (мердж поверх DefaultWorkGroupingConfig).
func (s *Store) WorkGrouping(ctx context.Context) (WorkGroupingConfig, error) {
	cfg := DefaultWorkGroupingConfig()
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, workGroupingKey).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read work grouping settings: %w", err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return DefaultWorkGroupingConfig(), fmt.Errorf("decode work grouping settings: %w", err)
	}
	return cfg, nil
}

// SetWorkGrouping сохраняет настройки группировки (upsert).
func (s *Store) SetWorkGrouping(ctx context.Context, cfg WorkGroupingConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode work grouping settings: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, workGroupingKey, raw)
	if err != nil {
		return fmt.Errorf("save work grouping settings: %w", err)
	}
	return nil
}

const bioAdaptationKey = "bio_adaptation_enrichment"

// BioAdaptationConfig — настройки фонового дозаполнения «людей и экранизаций» из
// внешних источников: биографии + фото авторов (Wikipedia/OpenLibrary) и
// экранизации книг (Wikidata). У этих данных НЕТ fb2-источника, поэтому режим
// проще, чем у года/обложек: включил воркер → проходит по всей коллекции (нет
// fallback-vs-whole тумблера). Оба воркера opt-in (по умолчанию выключены —
// ходят в публичные API).
//
//	Bios            — фоновый проход по авторам без bio/photo.
//	Adaptations     — фоновый проход по книгам без экранизаций.
//	BiosRPM         — лимит запросов/мин воркера биографий (Wikipedia + OL).
//	AdaptationsRPM  — лимит запросов/мин воркера экранизаций (Wikidata SPARQL —
//	                  тяжелее, держим ниже).
type BioAdaptationConfig struct {
	Bios           bool `json:"bios"`
	Adaptations    bool `json:"adaptations"`
	BiosRPM        int  `json:"bios_rpm"`
	AdaptationsRPM int  `json:"adaptations_rpm"`
}

// DefaultBioAdaptationConfig — оба воркера выключены (opt-in), вежливые лимиты.
func DefaultBioAdaptationConfig() BioAdaptationConfig {
	return BioAdaptationConfig{
		Bios:           false,
		Adaptations:    false,
		BiosRPM:        30,
		AdaptationsRPM: 20,
	}
}

// BioAdaptation читает настройки; нет оверрайда в БД — дефолты (мердж поверх
// DefaultBioAdaptationConfig).
func (s *Store) BioAdaptation(ctx context.Context) (BioAdaptationConfig, error) {
	cfg := DefaultBioAdaptationConfig()
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, bioAdaptationKey).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read bio/adaptation settings: %w", err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return DefaultBioAdaptationConfig(), fmt.Errorf("decode bio/adaptation settings: %w", err)
	}
	return cfg, nil
}

// SetBioAdaptation сохраняет настройки (upsert).
func (s *Store) SetBioAdaptation(ctx context.Context, cfg BioAdaptationConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode bio/adaptation settings: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, bioAdaptationKey, raw)
	if err != nil {
		return fmt.Errorf("save bio/adaptation settings: %w", err)
	}
	return nil
}
