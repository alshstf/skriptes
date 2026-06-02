package metadata

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AdaptationBackfiller — фоновое дозаполнение экранизаций книг из Wikidata
// (SPARQL) для книг, у которых их ещё не искали. External-only, режим простой:
// включил воркер → проходит по всей коллекции книг с `adaptations_fetched_at IS
// NULL` (lazy-путь /adaptations ставит этот же маркер). Маркер single-shot —
// ретрая по TTL нет (как и у lazy); см. план B2.
//
// Запись делает Enricher.EnsureAdaptations (SPARQL-резолв книги → запись в
// book_adaptations + проставление adaptations_fetched_at, в т.ч. на промахе);
// воркер только выбирает кандидатов и соблюдает rate-limit.
type AdaptationBackfiller struct {
	pool     *pgxpool.Pool
	enricher *Enricher
	logger   *slog.Logger
	gate     *rateGate

	done atomic.Int64 // сколько книг обработано за проход (для логов)
}

const (
	adaptationBackfillBatchSize      = 100
	adaptationBackfillWorkers        = 2
	adaptationBackfillRescanInterval = 30 * time.Minute
	adaptationBackfillTaskTimeout    = 90 * time.Second // SPARQL медленный
)

// NewAdaptationBackfiller строит воркер с rate-gate по rpm (книг в минуту).
func NewAdaptationBackfiller(pool *pgxpool.Pool, enricher *Enricher, rpm int, logger *slog.Logger) *AdaptationBackfiller {
	if logger == nil {
		logger = slog.Default()
	}
	b := &AdaptationBackfiller{pool: pool, enricher: enricher, logger: logger, gate: &rateGate{}}
	b.gate.setRPM(rpm)
	return b
}

// Run — долгоживущий цикл: обработать все pending-книги, поспать, пересканить.
func (b *AdaptationBackfiller) Run(ctx context.Context) {
	if b.pool == nil || b.enricher == nil {
		return
	}
	b.logger.Info("adaptation backfill: started", "workers", adaptationBackfillWorkers)
	for {
		n := b.drain(ctx)
		if ctx.Err() != nil {
			return
		}
		if n > 0 {
			b.logger.Info("adaptation backfill: pass complete", "processed", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(adaptationBackfillRescanInterval):
		}
	}
}

type adaptationCandidate struct {
	id      int64
	title   string
	lang    string
	authors []string
}

func (b *AdaptationBackfiller) drain(ctx context.Context) int {
	b.done.Store(0)
	total := 0
	var cursor int64
	for ctx.Err() == nil {
		batch, err := b.fetchBatch(ctx, cursor, adaptationBackfillBatchSize)
		if err != nil {
			b.logger.Warn("adaptation backfill: fetch batch failed", "err", err)
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

func (b *AdaptationBackfiller) fetchBatch(ctx context.Context, afterID int64, limit int) ([]adaptationCandidate, error) {
	rows, err := b.pool.Query(ctx, `
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
		  AND b.adaptations_fetched_at IS NULL
		  AND b.id > $1
		GROUP BY b.id
		ORDER BY b.id
		LIMIT $2
	`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]adaptationCandidate, 0, limit)
	for rows.Next() {
		var c adaptationCandidate
		if err := rows.Scan(&c.id, &c.title, &c.lang, &c.authors); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (b *AdaptationBackfiller) processBatch(ctx context.Context, batch []adaptationCandidate) {
	sem := make(chan struct{}, adaptationBackfillWorkers)
	var wg sync.WaitGroup
	for _, c := range batch {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(c adaptationCandidate) {
			defer wg.Done()
			defer func() { <-sem }()
			b.processOne(ctx, c)
		}(c)
	}
	wg.Wait()
}

func (b *AdaptationBackfiller) processOne(ctx context.Context, bk adaptationCandidate) {
	if err := b.gate.wait(ctx); err != nil {
		return // воркер останавливают
	}
	taskCtx, cancel := context.WithTimeout(ctx, adaptationBackfillTaskTimeout)
	defer cancel()
	// Wikidata-провайдеру нужны Title + Authors (+ Lang); fb2-путь не задействован.
	q := BookQuery{ID: bk.id, Title: bk.title, Authors: bk.authors, Lang: bk.lang}
	b.enricher.EnsureAdaptations(taskCtx, q) // сам ставит adaptations_fetched_at
	b.done.Add(1)
}

// ── Controller (зеркало CoverBackfillController) ──

// AdaptationBackfillStatus — состояние воркера экранизаций для админ-UI.
type AdaptationBackfillStatus struct {
	Running bool   `json:"adaptations_running"`
	Mode    string `json:"adaptations_mode"` // "off" | "continuous" | "once"
}

// AdaptationCoverage — покрытие книг экранизациями (для админ-статистики).
type AdaptationCoverage struct {
	Total           int `json:"total"`
	WithAdaptations int `json:"with_adaptations"`
}

type AdaptationBackfillController struct {
	pool     *pgxpool.Pool
	enricher *Enricher
	logger   *slog.Logger

	mu         sync.Mutex
	rpm        int
	contCancel context.CancelFunc
	onceCancel context.CancelFunc
}

func NewAdaptationBackfillController(pool *pgxpool.Pool, enricher *Enricher, rpm int, logger *slog.Logger) *AdaptationBackfillController {
	if logger == nil {
		logger = slog.Default()
	}
	return &AdaptationBackfillController{pool: pool, enricher: enricher, rpm: rpm, logger: logger}
}

func (c *AdaptationBackfillController) ready() bool { return c.pool != nil && c.enricher != nil }

func (c *AdaptationBackfillController) Status() AdaptationBackfillStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.onceCancel != nil:
		return AdaptationBackfillStatus{Running: true, Mode: "once"}
	case c.contCancel != nil:
		return AdaptationBackfillStatus{Running: true, Mode: "continuous"}
	default:
		return AdaptationBackfillStatus{Running: false, Mode: "off"}
	}
}

func (c *AdaptationBackfillController) Start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.contCancel != nil || !c.ready() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.contCancel = cancel
	b := NewAdaptationBackfiller(c.pool, c.enricher, c.rpm, c.logger)
	go b.Run(ctx)
	c.logger.Info("adaptation backfill: continuous job started")
}

func (c *AdaptationBackfillController) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.contCancel == nil {
		return
	}
	c.contCancel()
	c.contCancel = nil
	c.logger.Info("adaptation backfill: continuous job stopped")
}

// SetEnabled — тумблер «фоновый воркер вкл/выкл».
func (c *AdaptationBackfillController) SetEnabled(on bool) {
	if on {
		c.Start()
	} else {
		c.Stop()
	}
}

// SetConfig применяет новый rpm; если непрерывный воркер идёт — перезапускает.
func (c *AdaptationBackfillController) SetConfig(rpm int) {
	c.mu.Lock()
	c.rpm = rpm
	running := c.contCancel != nil
	c.mu.Unlock()
	if running {
		c.Stop()
		c.Start()
	}
}

// RunOnce — один проход (кнопка «Прогнать разово»).
func (c *AdaptationBackfillController) RunOnce() {
	c.mu.Lock()
	if c.onceCancel != nil || c.contCancel != nil || !c.ready() {
		c.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.onceCancel = cancel
	rpm := c.rpm
	c.mu.Unlock()
	go func() {
		b := NewAdaptationBackfiller(c.pool, c.enricher, rpm, c.logger)
		n := b.drain(ctx)
		cancel()
		c.mu.Lock()
		c.onceCancel = nil
		c.mu.Unlock()
		c.logger.Info("adaptation backfill: one-shot pass done", "processed", n)
	}()
}

// StopOnce — отменить идущий разовый проход.
func (c *AdaptationBackfillController) StopOnce() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.onceCancel == nil {
		return
	}
	c.onceCancel()
	c.logger.Info("adaptation backfill: one-shot pass stop requested")
}

// Coverage — покрытие книг экранизациями (всего книг, с ≥1 экранизацией).
func (c *AdaptationBackfillController) Coverage(ctx context.Context) (AdaptationCoverage, error) {
	var out AdaptationCoverage
	if c.pool == nil {
		return out, nil
	}
	if err := c.pool.QueryRow(ctx,
		`SELECT count(*) FROM books WHERE deleted = false`).Scan(&out.Total); err != nil {
		return out, err
	}
	err := c.pool.QueryRow(ctx, `
		SELECT count(DISTINCT ba.book_id)
		FROM book_adaptations ba
		JOIN books b ON b.id = ba.book_id AND b.deleted = false
	`).Scan(&out.WithAdaptations)
	return out, err
}
