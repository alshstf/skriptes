package metadata

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AuthorBackfiller — фоновое дозаполнение биографий и фото авторов из внешних
// источников (Wikipedia → OpenLibrary) для авторов, которых ещё не обогащали.
// В отличие от года/обложек у этих данных НЕТ fb2-источника, поэтому нет
// fallback-vs-whole режима: воркер просто проходит по всей коллекции авторов,
// у которых `metadata_fetched_at IS NULL` (lazy-путь ставит этот же маркер при
// открытии карточки автора). Маркер single-shot — ретрая по TTL нет (как и у
// lazy-пути); см. план B2.
//
// Сохранение делает Enricher (EnsureAuthorBio + EnsureAuthorPhoto, со своими
// inflight-локами и записью в authors); воркер только выбирает кандидатов,
// соблюдает rate-limit и в конце гарантированно ставит metadata_fetched_at.
type AuthorBackfiller struct {
	pool     *pgxpool.Pool
	enricher *Enricher
	logger   *slog.Logger
	gate     *rateGate

	done atomic.Int64 // сколько авторов обработано за проход (для логов)
}

const (
	authorBackfillBatchSize      = 100
	authorBackfillWorkers        = 2
	authorBackfillRescanInterval = 30 * time.Minute
	authorBackfillTaskTimeout    = 60 * time.Second
)

// NewAuthorBackfiller строит воркер с rate-gate по rpm (авторов в минуту).
func NewAuthorBackfiller(pool *pgxpool.Pool, enricher *Enricher, rpm int, logger *slog.Logger) *AuthorBackfiller {
	if logger == nil {
		logger = slog.Default()
	}
	b := &AuthorBackfiller{pool: pool, enricher: enricher, logger: logger, gate: &rateGate{}}
	b.gate.setRPM(rpm)
	return b
}

// Run — долгоживущий цикл: обработать всех pending-авторов, поспать, пересканить.
func (b *AuthorBackfiller) Run(ctx context.Context) {
	if b.pool == nil || b.enricher == nil {
		return
	}
	b.logger.Info("author backfill: started", "workers", authorBackfillWorkers)
	for {
		n := b.drain(ctx)
		if ctx.Err() != nil {
			return
		}
		if n > 0 {
			b.logger.Info("author backfill: pass complete", "processed", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(authorBackfillRescanInterval):
		}
	}
}

type authorCandidate struct {
	id         int64
	lastName   string
	firstName  string
	middleName string
	fullName   string
}

func (b *AuthorBackfiller) drain(ctx context.Context) int {
	b.done.Store(0)
	total := 0
	var cursor int64
	for ctx.Err() == nil {
		batch, err := b.fetchBatch(ctx, cursor, authorBackfillBatchSize)
		if err != nil {
			b.logger.Warn("author backfill: fetch batch failed", "err", err)
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

func (b *AuthorBackfiller) fetchBatch(ctx context.Context, afterID int64, limit int) ([]authorCandidate, error) {
	rows, err := b.pool.Query(ctx, `
		SELECT id, last_name, first_name, middle_name,
		       TRIM(CONCAT_WS(' ', last_name, first_name, middle_name)) AS full_name
		FROM authors
		WHERE metadata_fetched_at IS NULL
		  AND id > $1
		ORDER BY id
		LIMIT $2
	`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]authorCandidate, 0, limit)
	for rows.Next() {
		var c authorCandidate
		if err := rows.Scan(&c.id, &c.lastName, &c.firstName, &c.middleName, &c.fullName); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (b *AuthorBackfiller) processBatch(ctx context.Context, batch []authorCandidate) {
	sem := make(chan struct{}, authorBackfillWorkers)
	var wg sync.WaitGroup
	for _, c := range batch {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(c authorCandidate) {
			defer wg.Done()
			defer func() { <-sem }()
			b.processOne(ctx, c)
		}(c)
	}
	wg.Wait()
}

func (b *AuthorBackfiller) processOne(ctx context.Context, a authorCandidate) {
	// Один rate-слот на автора (lang оставляем пустым — WikipediaProvider
	// пробует ru-first → en, что покрывает наш каталог; как в lazy-пути).
	if err := b.gate.wait(ctx); err != nil {
		return // воркер останавливают
	}
	taskCtx, cancel := context.WithTimeout(ctx, authorBackfillTaskTimeout)
	defer cancel()
	q := AuthorQuery{
		ID:         a.id,
		LastName:   a.lastName,
		FirstName:  a.firstName,
		MiddleName: a.middleName,
		FullName:   a.fullName,
	}
	b.enricher.EnsureAuthorBio(taskCtx, q)
	b.enricher.EnsureAuthorPhoto(taskCtx, q)
	// Гарантированно помечаем «попытка была», даже если провайдеры пусты или
	// ничего не нашли — чтобы кандидат не выбирался повторно каждый проход.
	if _, err := b.pool.Exec(ctx,
		`UPDATE authors SET metadata_fetched_at = now() WHERE id = $1 AND metadata_fetched_at IS NULL`, a.id); err != nil {
		b.logger.Warn("author backfill: mark fetched_at failed", "author_id", a.id, "err", err)
	}
	b.done.Add(1)
}

// ── Controller (зеркало CoverBackfillController) ──

// AuthorBackfillStatus — состояние воркера биографий для админ-UI.
type AuthorBackfillStatus struct {
	Running bool   `json:"bios_running"`
	Mode    string `json:"bios_mode"` // "off" | "continuous" | "once"
}

// AuthorCoverage — покрытие авторов биографиями/фото (для админ-статистики).
type AuthorCoverage struct {
	Total     int `json:"total"`
	WithBio   int `json:"with_bio"`
	WithPhoto int `json:"with_photo"`
}

type AuthorBackfillController struct {
	pool     *pgxpool.Pool
	enricher *Enricher
	logger   *slog.Logger

	mu         sync.Mutex
	rpm        int
	contCancel context.CancelFunc
	onceCancel context.CancelFunc
}

func NewAuthorBackfillController(pool *pgxpool.Pool, enricher *Enricher, rpm int, logger *slog.Logger) *AuthorBackfillController {
	if logger == nil {
		logger = slog.Default()
	}
	return &AuthorBackfillController{pool: pool, enricher: enricher, rpm: rpm, logger: logger}
}

func (c *AuthorBackfillController) ready() bool { return c.pool != nil && c.enricher != nil }

func (c *AuthorBackfillController) Status() AuthorBackfillStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.onceCancel != nil:
		return AuthorBackfillStatus{Running: true, Mode: "once"}
	case c.contCancel != nil:
		return AuthorBackfillStatus{Running: true, Mode: "continuous"}
	default:
		return AuthorBackfillStatus{Running: false, Mode: "off"}
	}
}

func (c *AuthorBackfillController) Start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.contCancel != nil || !c.ready() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.contCancel = cancel
	b := NewAuthorBackfiller(c.pool, c.enricher, c.rpm, c.logger)
	go b.Run(ctx)
	c.logger.Info("author backfill: continuous job started")
}

func (c *AuthorBackfillController) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.contCancel == nil {
		return
	}
	c.contCancel()
	c.contCancel = nil
	c.logger.Info("author backfill: continuous job stopped")
}

// SetEnabled — тумблер «фоновый воркер вкл/выкл».
func (c *AuthorBackfillController) SetEnabled(on bool) {
	if on {
		c.Start()
	} else {
		c.Stop()
	}
}

// SetConfig применяет новый rpm; если непрерывный воркер идёт — перезапускает.
func (c *AuthorBackfillController) SetConfig(rpm int) {
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
func (c *AuthorBackfillController) RunOnce() {
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
		b := NewAuthorBackfiller(c.pool, c.enricher, rpm, c.logger)
		n := b.drain(ctx)
		cancel()
		c.mu.Lock()
		c.onceCancel = nil
		c.mu.Unlock()
		c.logger.Info("author backfill: one-shot pass done", "processed", n)
	}()
}

// StopOnce — отменить идущий разовый проход.
func (c *AuthorBackfillController) StopOnce() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.onceCancel == nil {
		return
	}
	c.onceCancel()
	c.logger.Info("author backfill: one-shot pass stop requested")
}

// Coverage — покрытие авторов биографиями и фото.
func (c *AuthorBackfillController) Coverage(ctx context.Context) (AuthorCoverage, error) {
	var out AuthorCoverage
	if c.pool == nil {
		return out, nil
	}
	err := c.pool.QueryRow(ctx, `
		SELECT count(*),
		       count(*) FILTER (WHERE bio IS NOT NULL AND bio <> ''),
		       count(*) FILTER (WHERE photo_path IS NOT NULL AND photo_path <> '')
		FROM authors
	`).Scan(&out.Total, &out.WithBio, &out.WithPhoto)
	return out, err
}
