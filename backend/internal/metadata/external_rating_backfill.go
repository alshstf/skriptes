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

// ExternalRatingBackfiller — фоновое дозаполнение books.external_rating из
// ВНЕШНИХ источников (Google Books, OpenLibrary) для книг без рейтинга. Зеркало
// CoverBackfiller: opt-in, низкая конкуренция, per-source rate-limit и учёт
// попыток (book_external_rating_lookups).
//
// В отличие от обложек: рейтинга нет кэша — пишем прямо в books тремя полями
// (external_rating / _source / _count). Из включённых источников за один проход
// берём результат с бОльшим числом голосов (надёжнее).
//
// Режим охвата (WholeCollection):
//   - false (фолбэк, дефолт): кандидаты — у книги НЕТ никакого внешнего рейтинга
//     (rating IS NULL И external_rating IS NULL), т.е. заполняем пробелы, где на
//     UI сейчас ничего не показывается.
//   - true (вся коллекция): кандидаты — все external_rating IS NULL, даже если
//     есть LIBRATE (на показ LIBRATE приоритетнее, но web-данные накопятся).
type ExternalRatingBackfiller struct {
	pool   *pgxpool.Pool
	gb     RatingProvider // nil → источник недоступен
	ol     RatingProvider // nil → источник недоступен
	logger *slog.Logger
	cfg    ExternalRatingBackfillConfig
	gbGate *rateGate
	olGate *rateGate

	found atomic.Int64 // сколько рейтингов добавлено за проход (для логов)
}

// ExternalRatingBackfillConfig — рантайм-параметры воркера (зеркало
// settings.ExternalRatingConfig; передаётся значениями).
type ExternalRatingBackfillConfig struct {
	GoogleBooks       bool
	OpenLibrary       bool
	WholeCollection   bool
	GoogleBooksRPM    int
	OpenLibraryRPM    int
	NotFoundRetryDays int
	ErrorRetryHours   int
}

const (
	extRatingBatchSize      = 100
	extRatingWorkers        = 2
	extRatingRescanInterval = 30 * time.Minute
	extRatingTaskTimeout    = 60 * time.Second
)

// olRPMCap — потолок RPM для OpenLibrary по ДОКУМЕНТИРОВАННОЙ политике. С 2026-05
// официальная страница API даёт 1 req/s анонимно и 3 req/s с идентифицирующим
// User-Agent (наш стоит с 1.3.6); старый лимит «100/5мин» остался только у
// covers.openlibrary.org (там свой olCoverRPMCap=18). Держим консервативные
// 60/мин (1 req/s) и КЛАМПИМ для всех OL-воркеров данных/поиска
// (рейтинг/год/группировка/известность).
const olRPMCap = 60

// clampOLRPM прижимает сконфигурированный OL-RPM к olRPMCap (0/без-лимита → cap;
// меньше — оставляем, админ может быть ещё вежливее).
func clampOLRPM(rpm int) int {
	if rpm <= 0 || rpm > olRPMCap {
		return olRPMCap
	}
	return rpm
}

// NewExternalRatingBackfiller строит воркер с per-source rate-gate'ами по cfg.
func NewExternalRatingBackfiller(pool *pgxpool.Pool, gb, ol RatingProvider, cfg ExternalRatingBackfillConfig, logger *slog.Logger) *ExternalRatingBackfiller {
	if logger == nil {
		logger = slog.Default()
	}
	b := &ExternalRatingBackfiller{
		pool: pool, gb: gb, ol: ol, cfg: cfg, logger: logger,
		gbGate: &rateGate{}, olGate: &rateGate{},
	}
	b.gbGate.setRPM(cfg.GoogleBooksRPM)
	b.olGate.setRPM(clampOLRPM(cfg.OpenLibraryRPM))
	return b
}

// Run — долгоживущий цикл: дозаполнить pending-книги, поспать, пересканить.
func (b *ExternalRatingBackfiller) Run(ctx context.Context) {
	if b.pool == nil || (b.gb == nil && b.ol == nil) {
		return
	}
	b.logger.Info("external rating backfill: started", "workers", extRatingWorkers, "whole_collection", b.cfg.WholeCollection)
	for {
		n := b.drain(ctx)
		if ctx.Err() != nil {
			return
		}
		if n > 0 {
			b.logger.Info("external rating backfill: pass complete", "processed", n, "ratings_found", b.found.Load())
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(extRatingRescanInterval):
		}
	}
}

type extRatingCandidate struct {
	id            int64
	title         string
	lang          string
	isbn          string
	authors       []string
	lastName      string
	firstName     string
	srcTitle      string
	srcAuthorNorm string
	srcLang       string
}

// candidateCond — SQL-условие выбора кандидатов по режиму охвата.
func (b *ExternalRatingBackfiller) candidateCond() string {
	if b.cfg.WholeCollection {
		return "b.external_rating IS NULL"
	}
	return "b.rating IS NULL AND b.external_rating IS NULL"
}

func (b *ExternalRatingBackfiller) drain(ctx context.Context) int {
	b.found.Store(0)
	total := 0
	var cursor int64
	for ctx.Err() == nil {
		batch, err := b.fetchBatch(ctx, cursor, extRatingBatchSize)
		if err != nil {
			b.logger.Warn("external rating backfill: fetch batch failed", "err", err)
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

func (b *ExternalRatingBackfiller) fetchBatch(ctx context.Context, afterID int64, limit int) ([]extRatingCandidate, error) {
	q := fmt.Sprintf(`
		SELECT b.id, b.title, COALESCE(b.lang, ''), COALESCE(b.isbn, ''),
		       COALESCE(b.src_title, ''), COALESCE(b.src_author_normalized::text, ''), COALESCE(b.src_lang, ''),
		       COALESCE(
		           array_agg(TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name)))
		           FILTER (WHERE a.id IS NOT NULL),
		           '{}'
		       ) AS authors,
		       COALESCE((SELECT a2.last_name  FROM book_authors ba2 JOIN authors a2 ON a2.id = ba2.author_id
		                 WHERE ba2.book_id = b.id ORDER BY ba2.position, a2.id LIMIT 1), ''),
		       COALESCE((SELECT a2.first_name FROM book_authors ba2 JOIN authors a2 ON a2.id = ba2.author_id
		                 WHERE ba2.book_id = b.id ORDER BY ba2.position, a2.id LIMIT 1), '')
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
	out := make([]extRatingCandidate, 0, limit)
	for rows.Next() {
		var c extRatingCandidate
		if err := rows.Scan(&c.id, &c.title, &c.lang, &c.isbn,
			&c.srcTitle, &c.srcAuthorNorm, &c.srcLang,
			&c.authors, &c.lastName, &c.firstName); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (b *ExternalRatingBackfiller) processBatch(ctx context.Context, batch []extRatingCandidate) {
	sem := make(chan struct{}, extRatingWorkers)
	var wg sync.WaitGroup
	for _, c := range batch {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(c extRatingCandidate) {
			defer wg.Done()
			defer func() { <-sem }()
			b.processOne(ctx, c)
		}(c)
	}
	wg.Wait()
}

type ratingSource struct {
	provider RatingProvider
	gate     *rateGate
}

// sources — включённые источники в порядке приоритета: Google Books →
// OpenLibrary (но итоговый выбор — по числу голосов, не по порядку).
func (b *ExternalRatingBackfiller) sources() []ratingSource {
	var out []ratingSource
	if b.cfg.GoogleBooks && b.gb != nil {
		out = append(out, ratingSource{b.gb, b.gbGate})
	}
	if b.cfg.OpenLibrary && b.ol != nil {
		out = append(out, ratingSource{b.ol, b.olGate})
	}
	return out
}

func (b *ExternalRatingBackfiller) processOne(ctx context.Context, bk extRatingCandidate) {
	lookups, err := b.loadLookups(ctx, bk.id)
	if err != nil {
		b.logger.Warn("external rating backfill: load lookups failed", "book_id", bk.id, "err", err)
		return
	}
	now := time.Now()
	// Для переводных книг (есть src_title) Title/Authors/Lang берём из ОРИГИНАЛА
	// (OL/GB ищут по нему). ISBN (язык-агностичный, точный) и last/first name
	// (гейт authorNameMatches сам транслитерирует) — как есть.
	title, authors, lang := externalTitleAuthorLang(externalQueryFields{
		id: bk.id, title: bk.title, lang: bk.lang, authors: bk.authors,
		srcTitle: bk.srcTitle, srcAuthorNorm: bk.srcAuthorNorm, srcLang: bk.srcLang,
	})
	q := WorkQuery{
		BookID:    bk.id,
		Title:     title,
		ISBN:      bk.isbn,
		Lang:      lang,
		Authors:   authors,
		LastName:  bk.lastName,
		FirstName: bk.firstName,
	}

	var best RatingResult
	var bestSource string
	for _, src := range b.sources() {
		name := src.provider.Name()
		if !b.isDue(lookups[name], now) {
			continue
		}
		taskCtx, cancel := context.WithTimeout(ctx, extRatingTaskTimeout)
		if werr := src.gate.wait(taskCtx); werr != nil {
			cancel()
			return // воркер останавливают — выходим, ничего не помечая
		}
		res, ferr := src.provider.FetchRating(taskCtx, q)
		cancel()

		switch {
		case ferr == nil && res.Average > 0:
			b.upsertLookup(ctx, bk.id, name, "found")
			if bestSource == "" || res.Count > best.Count {
				best, bestSource = res, name
			}
		case errors.Is(ferr, ErrNotFound):
			b.upsertLookup(ctx, bk.id, name, "not_found")
		case ctx.Err() != nil:
			return // отмена воркера, не записываем как ошибку источника
		default:
			b.logger.Info("external rating backfill: provider error", "source", name, "book_id", bk.id, "err", ferr)
			b.upsertLookup(ctx, bk.id, name, "error")
		}
	}
	if bestSource != "" {
		b.writeRating(ctx, bk.id, best, bestSource)
		b.found.Add(1)
	}
}

// writeRating — записать выбранный внешний рейтинг в books (книга всё ещё без
// external_rating: это гарантирует candidateCond).
func (b *ExternalRatingBackfiller) writeRating(ctx context.Context, bookID int64, res RatingResult, source string) {
	if _, err := b.pool.Exec(ctx, `
		UPDATE books
		SET external_rating = $2, external_rating_source = $3, external_rating_count = $4
		WHERE id = $1
	`, bookID, res.Average, source, res.Count); err != nil {
		b.logger.Warn("external rating backfill: write rating failed", "book_id", bookID, "err", err)
	}
}

func (b *ExternalRatingBackfiller) loadLookups(ctx context.Context, bookID int64) (map[string]lookupRow, error) {
	rows, err := b.pool.Query(ctx,
		`SELECT source, outcome, checked_at FROM book_external_rating_lookups WHERE book_id = $1`, bookID)
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
func (b *ExternalRatingBackfiller) isDue(l lookupRow, now time.Time) bool {
	switch l.outcome {
	case "":
		return true
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

func (b *ExternalRatingBackfiller) upsertLookup(ctx context.Context, bookID int64, source, outcome string) {
	if _, err := b.pool.Exec(ctx, `
		INSERT INTO book_external_rating_lookups (book_id, source, outcome, checked_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (book_id, source)
		DO UPDATE SET outcome = EXCLUDED.outcome, checked_at = now()
	`, bookID, source, outcome); err != nil {
		b.logger.Warn("external rating backfill: upsert lookup failed", "book_id", bookID, "source", source, "err", err)
	}
}

// ── Controller: рантайм-управление воркером (зеркало CoverBackfillController) ──

// ExternalRatingBackfillStatus — состояние воркера для админ-UI.
type ExternalRatingBackfillStatus struct {
	Running bool   `json:"external_rating_running"`
	Mode    string `json:"external_rating_mode"` // "off" | "continuous" | "once"
}

// ExternalRatingCoverage — покрытие рейтингом (для админ-статистики). WithRating —
// книги с любым внешним рейтингом (LIBRATE или web); WithWeb — только web
// (external_rating). BySource считается по book_external_rating_lookups
// (outcome='found') — вклад именно фонового backfill'а.
type ExternalRatingCoverage struct {
	Total      int            `json:"total"`
	WithRating int            `json:"with_rating"`
	WithWeb    int            `json:"with_web"`
	BySource   map[string]int `json:"by_source"`
}

type ExternalRatingBackfillController struct {
	pool   *pgxpool.Pool
	gb     RatingProvider
	ol     RatingProvider
	logger *slog.Logger

	mu         sync.Mutex
	cfg        ExternalRatingBackfillConfig
	contCancel context.CancelFunc
	onceCancel context.CancelFunc
}

func NewExternalRatingBackfillController(pool *pgxpool.Pool, gb, ol RatingProvider, cfg ExternalRatingBackfillConfig, logger *slog.Logger) *ExternalRatingBackfillController {
	if logger == nil {
		logger = slog.Default()
	}
	return &ExternalRatingBackfillController{pool: pool, gb: gb, ol: ol, cfg: cfg, logger: logger}
}

func (c *ExternalRatingBackfillController) ready() bool {
	return c.pool != nil && (c.gb != nil || c.ol != nil)
}

// ResetFailedLookups удаляет неудачные попытки (not_found/error) из
// book_external_rating_lookups — книги перепроверятся на следующем проходе
// (напр. после улучшения поиска: кириллица → src_title). 'found' не трогаем.
func (c *ExternalRatingBackfillController) ResetFailedLookups(ctx context.Context) (int64, error) {
	if c.pool == nil {
		return 0, nil
	}
	tag, err := c.pool.Exec(ctx, `DELETE FROM book_external_rating_lookups WHERE outcome IN ('not_found', 'error')`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (c *ExternalRatingBackfillController) Status() ExternalRatingBackfillStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.onceCancel != nil:
		return ExternalRatingBackfillStatus{Running: true, Mode: "once"}
	case c.contCancel != nil:
		return ExternalRatingBackfillStatus{Running: true, Mode: "continuous"}
	default:
		return ExternalRatingBackfillStatus{Running: false, Mode: "off"}
	}
}

func (c *ExternalRatingBackfillController) Start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.contCancel != nil || !c.ready() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.contCancel = cancel
	b := NewExternalRatingBackfiller(c.pool, c.gb, c.ol, c.cfg, c.logger)
	go b.Run(ctx)
	c.logger.Info("external rating backfill: continuous job started")
}

func (c *ExternalRatingBackfillController) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.contCancel == nil {
		return
	}
	c.contCancel()
	c.contCancel = nil
	c.logger.Info("external rating backfill: continuous job stopped")
}

// SetEnabled — тумблер «фоновый воркер вкл/выкл».
func (c *ExternalRatingBackfillController) SetEnabled(on bool) {
	if on {
		c.Start()
	} else {
		c.Stop()
	}
}

// SetConfig применяет новые параметры. Если непрерывный воркер запущен —
// перезапускает его, чтобы подхватить cfg.
func (c *ExternalRatingBackfillController) SetConfig(cfg ExternalRatingBackfillConfig) {
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
func (c *ExternalRatingBackfillController) RunOnce() {
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
		b := NewExternalRatingBackfiller(c.pool, c.gb, c.ol, cfg, c.logger)
		n := b.drain(ctx)
		cancel()
		c.mu.Lock()
		c.onceCancel = nil
		c.mu.Unlock()
		c.logger.Info("external rating backfill: one-shot pass done", "processed", n)
	}()
}

// StopOnce — отменить идущий разовый проход.
func (c *ExternalRatingBackfillController) StopOnce() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.onceCancel == nil {
		return
	}
	c.onceCancel()
	c.logger.Info("external rating backfill: one-shot pass stop requested")
}

// Coverage — покрытие внешним рейтингом (всего книг, с любым внешним рейтингом,
// только web, разбивка вклада источников по book_external_rating_lookups).
func (c *ExternalRatingBackfillController) Coverage(ctx context.Context) (ExternalRatingCoverage, error) {
	out := ExternalRatingCoverage{BySource: map[string]int{}}
	if c.pool == nil {
		return out, nil
	}
	if err := c.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE deleted = false),
		       count(*) FILTER (WHERE deleted = false AND (rating IS NOT NULL OR external_rating IS NOT NULL)),
		       count(*) FILTER (WHERE deleted = false AND external_rating IS NOT NULL)
		FROM books
	`).Scan(&out.Total, &out.WithRating, &out.WithWeb); err != nil {
		return out, err
	}
	rows, err := c.pool.Query(ctx, `
		SELECT source, count(*)
		FROM book_external_rating_lookups
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
