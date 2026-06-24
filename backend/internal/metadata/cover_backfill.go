package metadata

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CoverBackfiller — фоновое дозаполнение cover_path из ВНЕШНИХ источников
// (OpenLibrary → Google Books) для книг, у которых обложка не извлеклась
// локально из fb2. Зеркало YearBackfiller: opt-in, низкая конкуренция,
// per-source rate-limit и per-source учёт попыток (book_cover_lookups).
//
// Режим охвата (WholeCollection):
//   - false (фолбэк, дефолт): кандидаты — cover_path IS NULL И локальная
//     fb2-фаза уже прошла (metadata_fetched_at IS NOT NULL). Внешние источники
//     добирают только то, чего не дал локальный проход.
//   - true (вся коллекция): кандидаты — все cover_path IS NULL, даже если
//     fb2-проход книги не касался. Очень долго (тысячи запросов к OL/GB),
//     opt-in за дисклеймером.
//
// Сохранение обложки делает Enricher.FetchCoverFrom (он владеет кэшем
// /cache/covers и пишет cover_path); воркер только выбирает кандидатов,
// соблюдает rate-limit и ведёт per-source учёт.
type CoverBackfiller struct {
	pool     *pgxpool.Pool
	enricher *Enricher     // владеет кэшем обложек + FetchCoverFrom
	ol       CoverProvider // nil → источник недоступен
	gb       CoverProvider // nil → источник недоступен
	logger   *slog.Logger
	cfg      CoverBackfillConfig
	olGate   *rateGate
	gbGate   *rateGate

	coversFound atomic.Int64 // сколько обложек добавлено за проход (для логов)
}

// CoverBackfillConfig — рантайм-параметры воркера (зеркало
// settings.CoverEnrichmentConfig; передаётся значениями, без зависимости
// metadata→settings).
type CoverBackfillConfig struct {
	OpenLibrary       bool
	GoogleBooks       bool
	WholeCollection   bool
	OpenLibraryRPM    int
	GoogleBooksRPM    int
	NotFoundRetryDays int
	ErrorRetryHours   int
}

const (
	coverBackfillBatchSize      = 100
	coverBackfillWorkers        = 2
	coverBackfillRescanInterval = 30 * time.Minute
	coverBackfillTaskTimeout    = 60 * time.Second
	// olCoverRPMCap — потолок RPM для OpenLibrary по ДОКУМЕНТИРОВАННОМУ лимиту
	// covers API: «100 requests/IP per 5 minutes» (= 20/мин, сверх → 403 Forbidden,
	// «do not crawl our cover API»). Держим запас (18) и КЛАМПИМ конфиг — иначе
	// дефолт 60 RPM ловил бы 403/блок.
	olCoverRPMCap = 18
)

// NewCoverBackfiller строит воркер с per-source rate-gate'ами по cfg.
func NewCoverBackfiller(pool *pgxpool.Pool, enricher *Enricher, ol, gb CoverProvider, cfg CoverBackfillConfig, logger *slog.Logger) *CoverBackfiller {
	if logger == nil {
		logger = slog.Default()
	}
	b := &CoverBackfiller{
		pool: pool, enricher: enricher, ol: ol, gb: gb, cfg: cfg, logger: logger,
		olGate: &rateGate{}, gbGate: &rateGate{},
	}
	// OL covers — клампим к olCoverRPMCap (док-лимит 20/мин); 0/«без лимита» тоже
	// прижимаем, чтобы не словить 403. GB — как в конфиге (его лимит — дневная
	// квота проекта, не RPM).
	olRPM := cfg.OpenLibraryRPM
	if olRPM <= 0 || olRPM > olCoverRPMCap {
		olRPM = olCoverRPMCap
	}
	b.olGate.setRPM(olRPM)
	b.gbGate.setRPM(cfg.GoogleBooksRPM)
	return b
}

// Run — долгоживущий цикл: дозаполнить все pending-книги, поспать, пересканить.
// Блокирующий; вызывать в горутине.
func (b *CoverBackfiller) Run(ctx context.Context) {
	if b.pool == nil || b.enricher == nil || (b.ol == nil && b.gb == nil) {
		return
	}
	b.logger.Info("cover backfill: started", "workers", coverBackfillWorkers, "whole_collection", b.cfg.WholeCollection)
	for {
		n := b.drain(ctx)
		if ctx.Err() != nil {
			return
		}
		if n > 0 {
			b.logger.Info("cover backfill: pass complete", "processed", n, "covers_found", b.coversFound.Load())
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(coverBackfillRescanInterval):
		}
	}
}

type coverCandidate struct {
	id      int64
	title   string
	lang    string
	authors []string
}

// candidateCond — SQL-условие выбора кандидатов по режиму охвата.
func (b *CoverBackfiller) candidateCond() string {
	if b.cfg.WholeCollection {
		return "b.cover_path IS NULL"
	}
	return "b.cover_path IS NULL AND b.metadata_fetched_at IS NOT NULL"
}

func (b *CoverBackfiller) drain(ctx context.Context) int {
	b.coversFound.Store(0)
	total := 0
	var cursor int64
	for ctx.Err() == nil {
		batch, err := b.fetchBatch(ctx, cursor, coverBackfillBatchSize)
		if err != nil {
			b.logger.Warn("cover backfill: fetch batch failed", "err", err)
			break
		}
		if len(batch) == 0 {
			break
		}
		b.processBatch(ctx, batch)
		total += len(batch)
		cursor = batch[len(batch)-1].id
	}
	return total
}

func (b *CoverBackfiller) fetchBatch(ctx context.Context, afterID int64, limit int) ([]coverCandidate, error) {
	q := fmt.Sprintf(`
		SELECT b.id, b.title, COALESCE(b.lang, ''),
		       COALESCE(
		           array_agg(TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name)))
		           FILTER (WHERE a.id IS NOT NULL),
		           '{}'
		       ) AS authors
		FROM books b
		LEFT JOIN book_authors ba ON ba.book_id = b.id
		LEFT JOIN authors a       ON a.id = ba.author_id
		WHERE b.deleted = false
		  AND %s
		  AND b.id > $1
		GROUP BY b.id
		ORDER BY b.id
		LIMIT $2
	`, b.candidateCond())
	rows, err := b.pool.Query(ctx, q, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]coverCandidate, 0, limit)
	for rows.Next() {
		var c coverCandidate
		if err := rows.Scan(&c.id, &c.title, &c.lang, &c.authors); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (b *CoverBackfiller) processBatch(ctx context.Context, batch []coverCandidate) {
	sem := make(chan struct{}, coverBackfillWorkers)
	var wg sync.WaitGroup
	for _, c := range batch {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(c coverCandidate) {
			defer wg.Done()
			defer func() { <-sem }()
			b.processOne(ctx, c)
		}(c)
	}
	wg.Wait()
}

type coverSource struct {
	provider CoverProvider
	gate     *rateGate
}

// sources — включённые внешние источники в порядке приоритета:
// OpenLibrary → Google Books.
func (b *CoverBackfiller) sources() []coverSource {
	var out []coverSource
	if b.cfg.OpenLibrary && b.ol != nil {
		out = append(out, coverSource{b.ol, b.olGate})
	}
	if b.cfg.GoogleBooks && b.gb != nil {
		out = append(out, coverSource{b.gb, b.gbGate})
	}
	return out
}

func (b *CoverBackfiller) processOne(ctx context.Context, bk coverCandidate) {
	lookups, err := b.loadLookups(ctx, bk.id)
	if err != nil {
		b.logger.Warn("cover backfill: load lookups failed", "book_id", bk.id, "err", err)
		return
	}
	now := time.Now()
	q := BookQuery{ID: bk.id, Title: bk.title, Authors: bk.authors, Lang: bk.lang}

	for _, src := range b.sources() {
		name := src.provider.Name()
		if !b.isDue(lookups[name], now) {
			continue
		}
		taskCtx, cancel := context.WithTimeout(ctx, coverBackfillTaskTimeout)
		if werr := src.gate.wait(taskCtx); werr != nil {
			cancel()
			return // воркер останавливают — выходим, ничего не помечая
		}
		found, ferr := b.enricher.FetchCoverFrom(taskCtx, src.provider, q)
		cancel()

		switch {
		case ferr == nil && found:
			b.upsertLookup(ctx, bk.id, name, "found")
			b.coversFound.Add(1)
			return // обложка есть — остальные источники не нужны
		case ferr == nil && !found:
			return // обложка появилась/занято (race) — выходим тихо
		case errors.Is(ferr, ErrNotFound):
			b.upsertLookup(ctx, bk.id, name, "not_found")
		case ctx.Err() != nil:
			return // отмена воркера, не записываем как ошибку источника
		default:
			b.logger.Info("cover backfill: provider error", "source", name, "book_id", bk.id, "err", ferr)
			b.upsertLookup(ctx, bk.id, name, "error")
		}
	}
}

func (b *CoverBackfiller) loadLookups(ctx context.Context, bookID int64) (map[string]lookupRow, error) {
	rows, err := b.pool.Query(ctx,
		`SELECT source, outcome, checked_at FROM book_cover_lookups WHERE book_id = $1`, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]lookupRow{}
	for rows.Next() {
		var src string
		var l lookupRow
		if err := rows.Scan(&src, &l.outcome, &l.checkedAt); err != nil {
			return nil, err
		}
		out[src] = l
	}
	return out, rows.Err()
}

// isDue — пора ли (пере)спрашивать источник: нет строки → да; found → нет;
// not_found / error → да, если старше соответствующего TTL.
func (b *CoverBackfiller) isDue(l lookupRow, now time.Time) bool {
	switch l.outcome {
	case "":
		return true // строки не было
	case "found":
		return false
	case "not_found":
		return now.Sub(l.checkedAt) >= time.Duration(b.cfg.NotFoundRetryDays)*24*time.Hour
	case "error":
		return now.Sub(l.checkedAt) >= time.Duration(b.cfg.ErrorRetryHours)*time.Hour
	default:
		return true
	}
}

func (b *CoverBackfiller) upsertLookup(ctx context.Context, bookID int64, source, outcome string) {
	if _, err := b.pool.Exec(ctx, `
		INSERT INTO book_cover_lookups (book_id, source, outcome, checked_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (book_id, source)
		DO UPDATE SET outcome = EXCLUDED.outcome, checked_at = now()
	`, bookID, source, outcome); err != nil {
		b.logger.Warn("cover backfill: upsert lookup failed", "book_id", bookID, "source", source, "err", err)
	}
}

// ── Controller: рантайм-управление воркером (зеркало YearBackfillController) ──

// CoverBackfillStatus — состояние воркера для админ-UI.
type CoverBackfillStatus struct {
	Running bool   `json:"cover_backfill_running"`
	Mode    string `json:"cover_backfill_mode"` // "off" | "continuous" | "once"
}

// CoverCoverage — покрытие обложками (для админ-статистики). BySource считается
// по book_cover_lookups (outcome='found') — то есть вклад именно внешнего
// backfill'а; fb2/lazy-обложки в разбивке не отражаются (у них нет источника).
type CoverCoverage struct {
	Total     int            `json:"total"`
	WithCover int            `json:"with_cover"`
	BySource  map[string]int `json:"by_source"`
}

type CoverBackfillController struct {
	pool     *pgxpool.Pool
	enricher *Enricher
	ol       CoverProvider
	gb       CoverProvider
	logger   *slog.Logger

	mu         sync.Mutex
	cfg        CoverBackfillConfig
	contCancel context.CancelFunc
	onceCancel context.CancelFunc
}

func NewCoverBackfillController(pool *pgxpool.Pool, enricher *Enricher, ol, gb CoverProvider, cfg CoverBackfillConfig, logger *slog.Logger) *CoverBackfillController {
	if logger == nil {
		logger = slog.Default()
	}
	return &CoverBackfillController{pool: pool, enricher: enricher, ol: ol, gb: gb, cfg: cfg, logger: logger}
}

func (c *CoverBackfillController) ready() bool {
	return c.pool != nil && c.enricher != nil && (c.ol != nil || c.gb != nil)
}

func (c *CoverBackfillController) Status() CoverBackfillStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.onceCancel != nil:
		return CoverBackfillStatus{Running: true, Mode: "once"}
	case c.contCancel != nil:
		return CoverBackfillStatus{Running: true, Mode: "continuous"}
	default:
		return CoverBackfillStatus{Running: false, Mode: "off"}
	}
}

func (c *CoverBackfillController) Start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.contCancel != nil || !c.ready() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.contCancel = cancel
	b := NewCoverBackfiller(c.pool, c.enricher, c.ol, c.gb, c.cfg, c.logger)
	go b.Run(ctx)
	c.logger.Info("cover backfill: continuous job started")
}

func (c *CoverBackfillController) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.contCancel == nil {
		return
	}
	c.contCancel()
	c.contCancel = nil
	c.logger.Info("cover backfill: continuous job stopped")
}

// SetEnabled — тумблер «фоновый воркер вкл/выкл».
func (c *CoverBackfillController) SetEnabled(on bool) {
	if on {
		c.Start()
	} else {
		c.Stop()
	}
}

// SetConfig применяет новые параметры (источники/режим/лимиты/TTL). Если
// непрерывный воркер запущен — перезапускает его, чтобы подхватить cfg.
func (c *CoverBackfillController) SetConfig(cfg CoverBackfillConfig) {
	c.mu.Lock()
	c.cfg = cfg
	running := c.contCancel != nil
	c.mu.Unlock()
	if running {
		c.Stop()
		c.Start()
	}
}

// RunOnce — один проход дозаполнения (кнопка «Прогнать разово»).
func (c *CoverBackfillController) RunOnce() {
	c.mu.Lock()
	if c.onceCancel != nil || c.contCancel != nil || !c.ready() {
		c.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.onceCancel = cancel
	cfg := c.cfg
	c.mu.Unlock()
	go func() {
		b := NewCoverBackfiller(c.pool, c.enricher, c.ol, c.gb, cfg, c.logger)
		n := b.drain(ctx)
		cancel()
		c.mu.Lock()
		c.onceCancel = nil
		c.mu.Unlock()
		c.logger.Info("cover backfill: one-shot pass done", "processed", n)
	}()
}

// StopOnce — отменить идущий разовый проход.
func (c *CoverBackfillController) StopOnce() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.onceCancel == nil {
		return
	}
	c.onceCancel()
	c.logger.Info("cover backfill: one-shot pass stop requested")
}

// Coverage — покрытие обложками (всего книг, с обложкой, разбивка вклада
// внешних источников по book_cover_lookups).
func (c *CoverBackfillController) Coverage(ctx context.Context) (CoverCoverage, error) {
	out := CoverCoverage{BySource: map[string]int{}}
	if c.pool == nil {
		return out, nil
	}
	if err := c.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE deleted = false),
		       count(*) FILTER (WHERE deleted = false AND cover_path IS NOT NULL)
		FROM books
	`).Scan(&out.Total, &out.WithCover); err != nil {
		return out, err
	}
	rows, err := c.pool.Query(ctx, `
		SELECT source, count(*)
		FROM book_cover_lookups
		WHERE outcome = 'found'
		GROUP BY source
	`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var src string
		var n int
		if err := rows.Scan(&src, &n); err != nil {
			return out, err
		}
		out.BySource[src] = n
	}
	return out, rows.Err()
}
