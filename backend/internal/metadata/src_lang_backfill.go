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

// SrcLangBackfiller — фоновое дозаполнение books.src_lang (язык оригинала) из
// ВНЕШНЕГО источника для переводов без fb2 <src-lang>. Зеркало YearBackfiller:
// opt-in, rate-gate, per-source учёт попыток (book_src_lang_lookups, TTL).
//
// v1 источник один — Wikidata P407 с precision-гейтами (см. FetchSrcLang):
// ровно один ISO-код у P407, и — гейт ЗАПИСИ здесь — код ≠ языку издания
// (натив «оригинал = язык издания» src_lang не получает: карточка показывает
// «Перевод с …» только у настоящих переводов; фильтр нативов закрывает
// производное поле orig_lang works-индекса).
//
// Кандидаты (фолбэк-режим): src_lang пуст, локальный fb2-скан издания уже
// прошёл (edition_meta_scanned_at NOT NULL) и оригинала не дал. Вся коллекция —
// все книги без src_lang, даже не тронутые fb2-проходом.
type SrcLangBackfiller struct {
	pool     *pgxpool.Pool
	wd       SrcLangProvider // nil → источник недоступен
	logger   *slog.Logger
	cfg      SrcLangBackfillConfig
	wdGate   *rateGate
	resyncer WorksIndexSyncer // nil → без таргетного ресинка works-индекса

	langChanged atomic.Int64 // сколько книг получили src_lang за проход

	changedMu    sync.Mutex
	changedBooks []int64 // id книг с новым src_lang (для works-индекса: src_lang[]/orig_lang[])
}

// SrcLangBackfillConfig — рантайм-параметры воркера (зеркало
// settings.SrcLangEnrichmentConfig; значениями, без зависимости metadata→settings).
type SrcLangBackfillConfig struct {
	Wikidata          bool
	WholeCollection   bool
	WikidataRPM       int
	NotFoundRetryDays int
	ErrorRetryHours   int
}

const (
	srcLangBackfillBatchSize      = 100
	srcLangBackfillWorkers        = 2
	srcLangBackfillRescanInterval = 30 * time.Minute
	srcLangBackfillTaskTimeout    = 60 * time.Second
)

// NewSrcLangBackfiller строит воркер с rate-gate'ом по cfg.
func NewSrcLangBackfiller(pool *pgxpool.Pool, wd SrcLangProvider, cfg SrcLangBackfillConfig, resyncer WorksIndexSyncer, logger *slog.Logger) *SrcLangBackfiller {
	if logger == nil {
		logger = slog.Default()
	}
	b := &SrcLangBackfiller{
		pool: pool, wd: wd, cfg: cfg, resyncer: resyncer, logger: logger,
		wdGate: &rateGate{},
	}
	b.wdGate.setRPM(cfg.WikidataRPM)
	return b
}

// Run — долгоживущий цикл: пройти всех кандидатов, поспать, пересканить
// (новые книги / истёкшие TTL). Блокирующий; вызывать в горутине.
func (b *SrcLangBackfiller) Run(ctx context.Context) {
	if b.pool == nil || b.wd == nil {
		return
	}
	b.logger.Info("src_lang backfill: started", "workers", srcLangBackfillWorkers)
	for {
		n := b.drain(ctx)
		if ctx.Err() != nil {
			return
		}
		if n > 0 {
			b.logger.Info("src_lang backfill: pass complete", "processed", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(srcLangBackfillRescanInterval):
		}
	}
}

func (b *SrcLangBackfiller) drain(ctx context.Context) int {
	b.langChanged.Store(0)
	b.changedMu.Lock()
	b.changedBooks = nil
	b.changedMu.Unlock()
	total := 0
	// Двухфазный обход «сначала ядро, потом хвост» (bookCoreCond): голова books
	// на реальном проде — современный самиздат-натив, весь not_found (замер
	// 2026-07-19: 528/528); переводы известных книг живут в ядре (переиздания/
	// экранизации/LIBRATE) — идём к ним первыми.
	for _, phaseCond := range []string{"AND " + bookCoreCond, "AND NOT " + bookCoreCond} {
		var cursor int64
		for ctx.Err() == nil {
			batch, err := b.fetchBatch(ctx, cursor, srcLangBackfillBatchSize, phaseCond)
			if err != nil {
				b.logger.Warn("src_lang backfill: fetch batch failed", "err", err)
				break
			}
			if len(batch) == 0 {
				break
			}
			b.processBatch(ctx, batch)
			total += len(batch)
			cursor = batch[len(batch)-1].id
		}
	}
	// src_lang живёт в works-индексе (src_lang[] + производный orig_lang[]) —
	// таргетно пере-собираем работы изменённых книг, чтобы фильтр «Язык
	// оригинала» и карточка не ждали полного ресинка.
	if b.resyncer != nil && b.langChanged.Load() > 0 && ctx.Err() == nil {
		if workIDs := b.changedWorkIDs(ctx); len(workIDs) > 0 {
			if err := b.resyncer.UpsertWorksToIndex(ctx, workIDs); err != nil {
				b.logger.Warn("src_lang backfill: upsert works to index failed", "err", err)
			} else {
				b.logger.Info("src_lang backfill: works index updated", "changed", b.langChanged.Load(), "works", len(workIDs))
			}
		}
	}
	return total
}

// changedWorkIDs — distinct work_id книг, получивших src_lang за проход.
func (b *SrcLangBackfiller) changedWorkIDs(ctx context.Context) []int64 {
	b.changedMu.Lock()
	ids := append([]int64(nil), b.changedBooks...)
	b.changedMu.Unlock()
	if len(ids) == 0 {
		return nil
	}
	rows, err := b.pool.Query(ctx,
		`SELECT DISTINCT work_id FROM books WHERE id = ANY($1) AND work_id IS NOT NULL`, ids)
	if err != nil {
		b.logger.Warn("src_lang backfill: map changed books to works failed", "err", err)
		return nil
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil
		}
		out = append(out, id)
	}
	return out
}

// candidateCond — условие выбора кандидатов по режиму охвата (см. doc типа).
func (b *SrcLangBackfiller) candidateCond() string {
	if b.cfg.WholeCollection {
		return "(b.src_lang IS NULL OR btrim(b.src_lang) = '')"
	}
	return "(b.src_lang IS NULL OR btrim(b.src_lang) = '') AND b.edition_meta_scanned_at IS NOT NULL"
}

// fetchBatch — страница кандидатов keyset'ом по id. phaseCond — доп. условие
// фазы приоритизации ("AND <core>" / "AND NOT <core>"), см. bookCoreCond.
func (b *SrcLangBackfiller) fetchBatch(ctx context.Context, afterID int64, limit int, phaseCond string) ([]yearCandidate, error) {
	q := fmt.Sprintf(`
		SELECT b.id, b.title, COALESCE(b.lang, ''),
		       COALESCE(b.src_title, ''), COALESCE(b.src_author_normalized::text, ''), COALESCE(b.src_lang, ''),
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
		  %s
		  AND b.id > $1
		GROUP BY b.id
		ORDER BY b.id
		LIMIT $2
	`, b.candidateCond(), phaseCond)
	rows, err := b.pool.Query(ctx, q, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]yearCandidate, 0, limit)
	for rows.Next() {
		var c yearCandidate
		if err := rows.Scan(&c.id, &c.title, &c.lang, &c.srcTitle, &c.srcAuthorNorm, &c.srcLang, &c.authors); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (b *SrcLangBackfiller) processBatch(ctx context.Context, batch []yearCandidate) {
	sem := make(chan struct{}, srcLangBackfillWorkers)
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

func (b *SrcLangBackfiller) processOne(ctx context.Context, bk yearCandidate) {
	if !b.cfg.Wikidata || b.wd == nil {
		return
	}
	lookups, err := b.loadLookups(ctx, bk.id)
	if err != nil {
		b.logger.Warn("src_lang backfill: load lookups failed", "book_id", bk.id, "err", err)
		return
	}
	name := b.wd.Name()
	if !b.isDue(lookups[name], time.Now()) {
		return
	}
	// Кандидат без src_lang, но src_title может быть (fb2 иногда даёт название
	// оригинала без языка) — buildExternalQuery тогда ищет по оригиналу.
	q := buildExternalQuery(externalQueryFields(bk))

	taskCtx, cancel := context.WithTimeout(ctx, srcLangBackfillTaskTimeout)
	if werr := b.wdGate.wait(taskCtx); werr != nil {
		cancel()
		return // воркер останавливают — выходим, ничего не помечая
	}
	code, ferr := b.wd.FetchSrcLang(taskCtx, q)
	cancel()

	switch {
	case ferr == nil && code != "":
		// Гейт записи: оригинал ≠ язык издания. Совпали → натив/переиздание на
		// языке оригинала — src_lang по продуктовой семантике остаётся пустым
		// (см. doc типа); помечаем not_found (дозаполнять нечего, TTL пере-
		// спросит нескоро).
		if code == normalizeLangCode(bk.lang) {
			b.upsertLookup(ctx, bk.id, name, "not_found", "")
			return
		}
		if err := b.writeFound(ctx, bk.id, name, code); err != nil {
			b.logger.Warn("src_lang backfill: write found failed", "book_id", bk.id, "err", err)
		} else {
			b.logger.Info("src_lang backfill: src_lang found", "source", name, "book_id", bk.id, "src_lang", code)
		}
	case errors.Is(ferr, ErrNotFound):
		b.upsertLookup(ctx, bk.id, name, "not_found", "")
	case ctx.Err() != nil:
		return // отмена воркера, не записываем как ошибку источника
	default:
		b.logger.Info("src_lang backfill: provider error", "source", name, "book_id", bk.id, "err", ferr)
		b.upsertLookup(ctx, bk.id, name, "error", "")
	}
}

func (b *SrcLangBackfiller) loadLookups(ctx context.Context, bookID int64) (map[string]lookupRow, error) {
	rows, err := b.pool.Query(ctx,
		`SELECT source, outcome, checked_at FROM book_src_lang_lookups WHERE book_id = $1`, bookID)
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

// isDue — зеркало YearBackfiller.isDue на TTL этого воркера.
func (b *SrcLangBackfiller) isDue(l lookupRow, now time.Time) bool {
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

func (b *SrcLangBackfiller) writeFound(ctx context.Context, bookID int64, source, code string) error {
	// set-if-null: непустой src_lang (fb2/оверрайд/прошлый проход) не перетираем.
	if _, err := b.pool.Exec(ctx, `
		UPDATE books SET src_lang = COALESCE(NULLIF(btrim(src_lang), ''), $2)
		WHERE id = $1
	`, bookID, code); err != nil {
		return err
	}
	b.upsertLookup(ctx, bookID, source, "found", code)
	b.langChanged.Add(1)
	b.changedMu.Lock()
	b.changedBooks = append(b.changedBooks, bookID)
	b.changedMu.Unlock()
	return nil
}

func (b *SrcLangBackfiller) upsertLookup(ctx context.Context, bookID int64, source, outcome, code string) {
	var cptr *string
	if code != "" {
		cptr = &code
	}
	if _, err := b.pool.Exec(ctx, `
		INSERT INTO book_src_lang_lookups (book_id, source, outcome, src_lang, checked_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (book_id, source)
		DO UPDATE SET outcome = EXCLUDED.outcome, src_lang = EXCLUDED.src_lang, checked_at = now()
	`, bookID, source, outcome, cptr); err != nil {
		b.logger.Warn("src_lang backfill: upsert lookup failed", "book_id", bookID, "source", source, "err", err)
	}
}

// ── Controller: рантайм-управление воркером (зеркало YearBackfillController) ──

// SrcLangBackfillStatus — состояние воркера для админ-UI.
type SrcLangBackfillStatus struct {
	Running bool   `json:"src_lang_backfill_running"`
	Mode    string `json:"src_lang_backfill_mode"` // "off" | "continuous" | "once"
}

// SrcLangCoverage — покрытие src_lang (для админ-статистики). BySource — число
// книг, где язык оригинала дал внешний источник (book_src_lang_lookups
// outcome='found'); остальное с src_lang — из fb2/оверрайдов.
type SrcLangCoverage struct {
	Total       int            `json:"total"`
	WithSrcLang int            `json:"with_src_lang"`
	BySource    map[string]int `json:"by_source"`
}

type SrcLangBackfillController struct {
	pool     *pgxpool.Pool
	wd       SrcLangProvider
	resyncer WorksIndexSyncer
	logger   *slog.Logger

	mu         sync.Mutex
	cfg        SrcLangBackfillConfig
	contCancel context.CancelFunc
	onceCancel context.CancelFunc
}

func NewSrcLangBackfillController(pool *pgxpool.Pool, wd SrcLangProvider, cfg SrcLangBackfillConfig, resyncer WorksIndexSyncer, logger *slog.Logger) *SrcLangBackfillController {
	if logger == nil {
		logger = slog.Default()
	}
	return &SrcLangBackfillController{pool: pool, wd: wd, resyncer: resyncer, cfg: cfg, logger: logger}
}

func (c *SrcLangBackfillController) ready() bool {
	return c.pool != nil && c.wd != nil
}

// ResetFailedLookups удаляет неудачные попытки (not_found/error) — книги
// перепроверятся на следующем проходе. 'found' не трогаем.
func (c *SrcLangBackfillController) ResetFailedLookups(ctx context.Context) (int64, error) {
	if c.pool == nil {
		return 0, nil
	}
	tag, err := c.pool.Exec(ctx, `DELETE FROM book_src_lang_lookups WHERE outcome IN ('not_found', 'error')`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (c *SrcLangBackfillController) Status() SrcLangBackfillStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.onceCancel != nil:
		return SrcLangBackfillStatus{Running: true, Mode: "once"}
	case c.contCancel != nil:
		return SrcLangBackfillStatus{Running: true, Mode: "continuous"}
	default:
		return SrcLangBackfillStatus{Running: false, Mode: "off"}
	}
}

func (c *SrcLangBackfillController) Start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.contCancel != nil || !c.ready() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.contCancel = cancel
	b := NewSrcLangBackfiller(c.pool, c.wd, c.cfg, c.resyncer, c.logger)
	go b.Run(ctx)
	c.logger.Info("src_lang backfill: continuous job started")
}

func (c *SrcLangBackfillController) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.contCancel == nil {
		return
	}
	c.contCancel()
	c.contCancel = nil
	c.logger.Info("src_lang backfill: continuous job stopped")
}

// SetEnabled — тумблер «фоновый воркер вкл/выкл».
func (c *SrcLangBackfillController) SetEnabled(on bool) {
	if on {
		c.Start()
	} else {
		c.Stop()
	}
}

// SetConfig применяет новые параметры; перезапускает работающий воркер.
func (c *SrcLangBackfillController) SetConfig(cfg SrcLangBackfillConfig) {
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
func (c *SrcLangBackfillController) RunOnce() {
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
		b := NewSrcLangBackfiller(c.pool, c.wd, cfg, c.resyncer, c.logger)
		n := b.drain(ctx)
		cancel()
		c.mu.Lock()
		c.onceCancel = nil
		c.mu.Unlock()
		c.logger.Info("src_lang backfill: one-shot pass done", "processed", n)
	}()
}

// StopOnce — отменить идущий разовый проход.
func (c *SrcLangBackfillController) StopOnce() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.onceCancel == nil {
		return
	}
	c.onceCancel()
	c.logger.Info("src_lang backfill: one-shot pass stop requested")
}

// Coverage — покрытие src_lang: всего живых книг, с языком оригинала, разбивка
// по внешним источникам (found-строки lookups).
func (c *SrcLangBackfillController) Coverage(ctx context.Context) (SrcLangCoverage, error) {
	out := SrcLangCoverage{BySource: map[string]int{}}
	if c.pool == nil {
		return out, nil
	}
	if err := c.pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE deleted = false),
		       count(*) FILTER (WHERE deleted = false AND src_lang IS NOT NULL AND btrim(src_lang) <> '')
		FROM books
	`).Scan(&out.Total, &out.WithSrcLang); err != nil {
		return out, err
	}
	rows, err := c.pool.Query(ctx, `
		SELECT l.source, count(*)
		FROM book_src_lang_lookups l
		JOIN books b ON b.id = l.book_id AND b.deleted = false
		WHERE l.outcome = 'found'
		GROUP BY l.source
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
