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

// YearBackfiller — фоновое дозаполнение written_year из ВНЕШНИХ источников
// (OpenLibrary first_publish_year → Wikidata P577) для книг, у которых год
// не извлёкся локально из fb2. В отличие от прогрева обложек ходит в сеть,
// поэтому: opt-in (выключен по умолчанию), низкая конкуренция, per-source
// rate-limit и per-source учёт попыток (book_year_lookups), чтобы не долбить
// один источник повторно.
//
// Кандидаты: written_year IS NULL AND year_local_scanned_at IS NOT NULL —
// локальная fb2-фаза уже отработала, года нет → пробуем внешние.
type YearBackfiller struct {
	pool     *pgxpool.Pool
	ol       YearProvider // nil → источник недоступен
	wd       YearProvider // nil → источник недоступен
	logger   *slog.Logger
	cfg      YearBackfillConfig
	olGate   *rateGate
	wdGate   *rateGate
	resyncer YearResyncer // nil → без авто-ресинка Meili-года

	yearChanged atomic.Int64 // сколько книг получили written_year за проход
}

// YearBackfillConfig — рантайм-параметры воркера (зеркало
// settings.YearEnrichmentConfig; передаётся значениями, без зависимости
// metadata→settings).
type YearBackfillConfig struct {
	OpenLibrary       bool
	Wikidata          bool
	WholeCollection   bool
	OpenLibraryRPM    int
	WikidataRPM       int
	NotFoundRetryDays int
	ErrorRetryHours   int
}

const (
	yearBackfillBatchSize      = 100
	yearBackfillWorkers        = 2
	yearBackfillRescanInterval = 30 * time.Minute
	yearBackfillTaskTimeout    = 60 * time.Second
)

// NewYearBackfiller строит воркер с per-source rate-gate'ами по cfg.
func NewYearBackfiller(pool *pgxpool.Pool, ol, wd YearProvider, cfg YearBackfillConfig, resyncer YearResyncer, logger *slog.Logger) *YearBackfiller {
	if logger == nil {
		logger = slog.Default()
	}
	b := &YearBackfiller{
		pool: pool, ol: ol, wd: wd, cfg: cfg, resyncer: resyncer, logger: logger,
		olGate: &rateGate{}, wdGate: &rateGate{},
	}
	b.olGate.setRPM(cfg.OpenLibraryRPM)
	b.wdGate.setRPM(cfg.WikidataRPM)
	return b
}

// Run — долгоживущий цикл: дозаполнить все pending-книги, поспать, пересканить
// (новые книги / истёкшие TTL). Блокирующий; вызывать в горутине.
func (b *YearBackfiller) Run(ctx context.Context) {
	if b.pool == nil || (b.ol == nil && b.wd == nil) {
		return
	}
	b.logger.Info("year backfill: started", "workers", yearBackfillWorkers)
	for {
		n := b.drain(ctx)
		if ctx.Err() != nil {
			return
		}
		if n > 0 {
			b.logger.Info("year backfill: pass complete", "processed", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(yearBackfillRescanInterval):
		}
	}
}

type yearCandidate struct {
	id      int64
	title   string
	lang    string
	authors []string
}

func (b *YearBackfiller) drain(ctx context.Context) int {
	b.yearChanged.Store(0)
	total := 0
	var cursor int64
	for ctx.Err() == nil {
		batch, err := b.fetchBatch(ctx, cursor, yearBackfillBatchSize)
		if err != nil {
			b.logger.Warn("year backfill: fetch batch failed", "err", err)
			break
		}
		if len(batch) == 0 {
			break
		}
		b.processBatch(ctx, batch)
		total += len(batch)
		cursor = batch[len(batch)-1].id
	}
	// Авто-синк Meili-поля year, если за проход год у книг появился.
	if b.resyncer != nil && b.yearChanged.Load() > 0 && ctx.Err() == nil {
		if n, err := b.resyncer.ResyncYears(ctx); err != nil {
			b.logger.Warn("year backfill: resync years failed", "err", err)
		} else {
			b.logger.Info("year backfill: years resynced to meili", "changed", b.yearChanged.Load(), "synced", n)
		}
	}
	return total
}

// candidateCond — SQL-условие выбора кандидатов по режиму охвата.
//   - фолбэк (дефолт): локальная fb2-фаза прошла (year_local_scanned_at NOT
//     NULL), но года нет — добираем внешними;
//   - вся коллекция: все книги без written_year, даже не тронутые fb2-проходом.
func (b *YearBackfiller) candidateCond() string {
	if b.cfg.WholeCollection {
		return "b.written_year IS NULL"
	}
	return "b.written_year IS NULL AND b.year_local_scanned_at IS NOT NULL"
}

func (b *YearBackfiller) fetchBatch(ctx context.Context, afterID int64, limit int) ([]yearCandidate, error) {
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
	out := make([]yearCandidate, 0, limit)
	for rows.Next() {
		var c yearCandidate
		if err := rows.Scan(&c.id, &c.title, &c.lang, &c.authors); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (b *YearBackfiller) processBatch(ctx context.Context, batch []yearCandidate) {
	sem := make(chan struct{}, yearBackfillWorkers)
	var wg sync.WaitGroup
	for _, c := range batch {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(c yearCandidate) {
			defer wg.Done()
			defer func() { <-sem }()
			b.processOne(ctx, c)
		}(c)
	}
	wg.Wait()
}

type yearSource struct {
	provider YearProvider
	gate     *rateGate
}

// sources — включённые внешние источники в порядке приоритета:
// OpenLibrary (first_publish_year — год первого издания) → Wikidata (P577).
func (b *YearBackfiller) sources() []yearSource {
	var out []yearSource
	if b.cfg.OpenLibrary && b.ol != nil {
		out = append(out, yearSource{b.ol, b.olGate})
	}
	if b.cfg.Wikidata && b.wd != nil {
		out = append(out, yearSource{b.wd, b.wdGate})
	}
	return out
}

func (b *YearBackfiller) processOne(ctx context.Context, bk yearCandidate) {
	lookups, err := b.loadLookups(ctx, bk.id)
	if err != nil {
		b.logger.Warn("year backfill: load lookups failed", "book_id", bk.id, "err", err)
		return
	}
	now := time.Now()
	q := BookQuery{ID: bk.id, Title: bk.title, Authors: bk.authors, Lang: bk.lang}

	for _, src := range b.sources() {
		name := src.provider.Name()
		if !b.isDue(lookups[name], now) {
			continue
		}
		taskCtx, cancel := context.WithTimeout(ctx, yearBackfillTaskTimeout)
		if werr := src.gate.wait(taskCtx); werr != nil {
			cancel()
			return // воркер останавливают — выходим, ничего не помечая
		}
		year, ferr := src.provider.FetchYear(taskCtx, q)
		cancel()

		switch {
		case ferr == nil && year > 0:
			if err := b.writeFound(ctx, bk.id, name, year); err != nil {
				b.logger.Warn("year backfill: write found failed", "book_id", bk.id, "err", err)
			} else {
				b.logger.Info("year backfill: year found", "source", name, "book_id", bk.id, "year", year)
			}
			return // год есть — остальные источники не нужны
		case errors.Is(ferr, ErrNotFound):
			b.upsertLookup(ctx, bk.id, name, "not_found", 0)
		case ctx.Err() != nil:
			return // отмена воркера, не записываем как ошибку источника
		default:
			b.logger.Info("year backfill: provider error", "source", name, "book_id", bk.id, "err", ferr)
			b.upsertLookup(ctx, bk.id, name, "error", 0)
		}
	}
}

type lookupRow struct {
	outcome   string
	checkedAt time.Time
}

func (b *YearBackfiller) loadLookups(ctx context.Context, bookID int64) (map[string]lookupRow, error) {
	rows, err := b.pool.Query(ctx,
		`SELECT source, outcome, checked_at FROM book_year_lookups WHERE book_id = $1`, bookID)
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
func (b *YearBackfiller) isDue(l lookupRow, now time.Time) bool {
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

func (b *YearBackfiller) writeFound(ctx context.Context, bookID int64, source string, year int) error {
	if _, err := b.pool.Exec(ctx, `
		UPDATE books SET
			written_year = COALESCE(written_year, $2::smallint),
			written_year_source = CASE WHEN written_year IS NULL THEN $3 ELSE written_year_source END
		WHERE id = $1
	`, bookID, year, source); err != nil {
		return err
	}
	b.upsertLookup(ctx, bookID, source, "found", year)
	b.yearChanged.Add(1)
	return nil
}

func (b *YearBackfiller) upsertLookup(ctx context.Context, bookID int64, source, outcome string, year int) {
	var yptr *int
	if year > 0 {
		yptr = &year
	}
	if _, err := b.pool.Exec(ctx, `
		INSERT INTO book_year_lookups (book_id, source, outcome, year, checked_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (book_id, source)
		DO UPDATE SET outcome = EXCLUDED.outcome, year = EXCLUDED.year, checked_at = now()
	`, bookID, source, outcome, yptr); err != nil {
		b.logger.Warn("year backfill: upsert lookup failed", "book_id", bookID, "source", source, "err", err)
	}
}

// ── rate-gate: минимальный интервал между вызовами одного источника ──
//
// Без сторонних зависимостей (x/time/rate в проекте нет). Резервирует слоты
// последовательно: конкурентные wait() выстраиваются в очередь по last+interval,
// сон — вне мьютекса, отменяется по ctx.
type rateGate struct {
	mu       sync.Mutex
	last     time.Time
	interval time.Duration
}

func (g *rateGate) setRPM(rpm int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if rpm <= 0 {
		g.interval = 0
		return
	}
	g.interval = time.Minute / time.Duration(rpm)
}

func (g *rateGate) wait(ctx context.Context) error {
	g.mu.Lock()
	if g.interval <= 0 {
		g.last = time.Now()
		g.mu.Unlock()
		return nil
	}
	now := time.Now()
	next := g.last.Add(g.interval)
	if !now.Before(next) {
		g.last = now
		g.mu.Unlock()
		return nil
	}
	g.last = next
	wait := next.Sub(now)
	g.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
		return nil
	}
}

// ── Controller: рантайм-управление воркером (зеркало PrewarmController) ──

// YearBackfillStatus — состояние воркера для админ-UI.
type YearBackfillStatus struct {
	Running bool   `json:"year_backfill_running"`
	Mode    string `json:"year_backfill_mode"` // "off" | "continuous" | "once"
}

// YearCoverage — покрытие written_year по источникам (для админ-статистики).
type YearCoverage struct {
	Total    int            `json:"total"`
	WithYear int            `json:"with_year"`
	BySource map[string]int `json:"by_source"`
}

type YearBackfillController struct {
	pool     *pgxpool.Pool
	ol       YearProvider
	wd       YearProvider
	resyncer YearResyncer
	logger   *slog.Logger

	mu         sync.Mutex
	cfg        YearBackfillConfig
	contCancel context.CancelFunc
	onceCancel context.CancelFunc
}

func NewYearBackfillController(pool *pgxpool.Pool, ol, wd YearProvider, cfg YearBackfillConfig, resyncer YearResyncer, logger *slog.Logger) *YearBackfillController {
	if logger == nil {
		logger = slog.Default()
	}
	return &YearBackfillController{pool: pool, ol: ol, wd: wd, resyncer: resyncer, cfg: cfg, logger: logger}
}

func (c *YearBackfillController) ready() bool {
	return c.pool != nil && (c.ol != nil || c.wd != nil)
}

func (c *YearBackfillController) Status() YearBackfillStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.onceCancel != nil:
		return YearBackfillStatus{Running: true, Mode: "once"}
	case c.contCancel != nil:
		return YearBackfillStatus{Running: true, Mode: "continuous"}
	default:
		return YearBackfillStatus{Running: false, Mode: "off"}
	}
}

func (c *YearBackfillController) Start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.contCancel != nil || !c.ready() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.contCancel = cancel
	b := NewYearBackfiller(c.pool, c.ol, c.wd, c.cfg, c.resyncer, c.logger)
	go b.Run(ctx)
	c.logger.Info("year backfill: continuous job started")
}

func (c *YearBackfillController) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.contCancel == nil {
		return
	}
	c.contCancel()
	c.contCancel = nil
	c.logger.Info("year backfill: continuous job stopped")
}

// SetEnabled — тумблер «фоновый воркер вкл/выкл».
func (c *YearBackfillController) SetEnabled(on bool) {
	if on {
		c.Start()
	} else {
		c.Stop()
	}
}

// SetConfig применяет новые параметры (источники/лимиты/TTL). Если
// непрерывный воркер запущен — перезапускает его, чтобы подхватить cfg.
func (c *YearBackfillController) SetConfig(cfg YearBackfillConfig) {
	c.mu.Lock()
	c.cfg = cfg
	running := c.contCancel != nil
	c.mu.Unlock()
	if running {
		c.Stop()
		c.Start()
	}
}

// RunOnce — один проход дозаполнения (кнопка «Запустить сейчас»).
func (c *YearBackfillController) RunOnce() {
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
		b := NewYearBackfiller(c.pool, c.ol, c.wd, cfg, c.resyncer, c.logger)
		n := b.drain(ctx)
		cancel()
		c.mu.Lock()
		c.onceCancel = nil
		c.mu.Unlock()
		c.logger.Info("year backfill: one-shot pass done", "processed", n)
	}()
}

// StopOnce — отменить идущий разовый проход.
func (c *YearBackfillController) StopOnce() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.onceCancel == nil {
		return
	}
	c.onceCancel()
	c.logger.Info("year backfill: one-shot pass stop requested")
}

// Coverage — покрытие written_year (всего книг, с годом, разбивка по источнику).
func (c *YearBackfillController) Coverage(ctx context.Context) (YearCoverage, error) {
	out := YearCoverage{BySource: map[string]int{}}
	if c.pool == nil {
		return out, nil
	}
	if err := c.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE deleted = false),
		       count(*) FILTER (WHERE deleted = false AND written_year IS NOT NULL)
		FROM books
	`).Scan(&out.Total, &out.WithYear); err != nil {
		return out, err
	}
	rows, err := c.pool.Query(ctx, `
		SELECT COALESCE(written_year_source, 'unknown'), count(*)
		FROM books
		WHERE deleted = false AND written_year IS NOT NULL
		GROUP BY written_year_source
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
