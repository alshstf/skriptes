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
}

// DefaultCoverConfig — безопасные дефолты. Мастер выключен; при включении
// синкаются все три типа (как раньше прогрев делал всё разом); интенсивность
// средняя; кэш ограничен, есть пол свободного места.
func DefaultCoverConfig() CoverConfig {
	return CoverConfig{
		CacheMaxMB:      8192,
		CacheMinFreeMB:  1024,
		Prewarm:         false,
		SyncCovers:      true,
		SyncAnnotations: true,
		SyncYears:       true,
		Intensity:       IntensityMedium,
	}
}

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
		OpenLibraryRPM:    60,
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
		Enabled:           false,
		OpenLibrary:       true,
		GoogleBooks:       true,
		WholeCollection:   false,
		OpenLibraryRPM:    60,
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
