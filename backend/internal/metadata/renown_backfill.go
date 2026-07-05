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

// RenownBackfiller — фоновое дозаполнение внешних счётчиков «известности» работ
// (works.fantlab_marks / ol_ratings_count / ol_want_count / wd_sitelinks) из
// Fantlab, OpenLibrary и Wikidata. Зеркало ExternalRatingBackfiller, но WORK-level: известность —
// свойство работы, кандидаты — работы, учёт попыток — work_renown_lookups,
// а после записи — таргетный ресинк works-индекса (пересчёт интегральной
// популярности computeWorkPopularity, куда счётчики входят слагаемыми).
//
// Режим охвата (WholeCollection):
//   - false (дефолт): ядро коллекции — работы с ≥2 изданиями ИЛИ
//     экранизацией ИЛИ LIBRATE-рейтингом (на проде ~60–70k из 500k): там
//     внешний сигнал вероятен, и именно ядро определяет «золотую полку»;
//   - true: все работы (долго — сотни тысяч rate-gated запросов).
type RenownBackfiller struct {
	pool     *pgxpool.Pool
	fl       RenownProvider // nil → источник недоступен
	ol       RenownProvider
	wd       RenownProvider
	resyncer WorksIndexSyncer // nil → без таргетного ресинка (наполнится полным)
	logger   *slog.Logger
	cfg      RenownBackfillConfig
	flGate   *rateGate
	olGate   *rateGate
	wdGate   *rateGate

	found atomic.Int64 // счётчиков найдено за проход (для логов)

	mu      sync.Mutex
	touched []int64 // работы с новыми счётчиками — на таргетный ресинк
}

// RenownBackfillConfig — рантайм-параметры воркера (зеркало
// settings.RenownConfig; передаётся значениями).
type RenownBackfillConfig struct {
	Fantlab           bool
	OpenLibrary       bool
	Wikidata          bool
	WholeCollection   bool
	FantlabRPM        int
	OpenLibraryRPM    int
	WikidataRPM       int
	FoundRefreshDays  int // известность растёт — found освежаем, но редко
	NotFoundRetryDays int
	ErrorRetryHours   int
}

const (
	renownBatchSize      = 100
	renownWorkers        = 2
	renownRescanInterval = 30 * time.Minute
	renownTaskTimeout    = 30 * time.Second

	// fantlabRPMCap — потолок RPM для Fantlab: лимиты API не документированы
	// (v0.9 «тестовый режим»), держим заведомо вежливый темп.
	fantlabRPMCap = 60
)

// clampFantlabRPM прижимает сконфигурированный RPM к потолку (0/без-лимита →
// cap; меньше — оставляем).
func clampFantlabRPM(rpm int) int {
	if rpm <= 0 || rpm > fantlabRPMCap {
		return fantlabRPMCap
	}
	return rpm
}

// NewRenownBackfiller строит воркер с per-source rate-gate'ами по cfg.
func NewRenownBackfiller(pool *pgxpool.Pool, fl, ol, wd RenownProvider, resyncer WorksIndexSyncer, cfg RenownBackfillConfig, logger *slog.Logger) *RenownBackfiller {
	if logger == nil {
		logger = slog.Default()
	}
	b := &RenownBackfiller{
		pool: pool, fl: fl, ol: ol, wd: wd, resyncer: resyncer, cfg: cfg, logger: logger,
		flGate: &rateGate{}, olGate: &rateGate{}, wdGate: &rateGate{},
	}
	b.flGate.setRPM(clampFantlabRPM(cfg.FantlabRPM))
	b.olGate.setRPM(clampOLRPM(cfg.OpenLibraryRPM))
	b.wdGate.setRPM(cfg.WikidataRPM)
	return b
}

// Run — долгоживущий цикл: дозаполнить due-работы, поспать, пересканить.
func (b *RenownBackfiller) Run(ctx context.Context) {
	if b.pool == nil || (b.fl == nil && b.ol == nil && b.wd == nil) {
		return
	}
	b.logger.Info("renown backfill: started", "workers", renownWorkers, "whole_collection", b.cfg.WholeCollection)
	for {
		n := b.drain(ctx)
		if ctx.Err() != nil {
			return
		}
		if n > 0 {
			b.logger.Info("renown backfill: pass complete", "processed", n, "renown_found", b.found.Load())
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(renownRescanInterval):
		}
	}
}

// renownCandidate — работа + поля представительного издания для построения
// внешних запросов (для OL переводные ищутся по оригиналу — src_*).
type renownCandidate struct {
	id            int64
	title         string
	lang          string
	isbn          string
	srcTitle      string
	srcAuthorNorm string
	srcLang       string
	authors       []string
	lastName      string
	firstName     string
	wdQID         string // works.ext_ids->>'wd_qid' — хинт для источника wikidata
}

// candidateCond — SQL-условие выбора кандидатов по режиму охвата.
func (b *RenownBackfiller) candidateCond() string {
	if b.cfg.WholeCollection {
		return "true"
	}
	return `(
		COALESCE(w.edition_count, 1) >= 2
		OR EXISTS (
			SELECT 1 FROM book_adaptations ad
			JOIN books bb ON bb.id = ad.book_id
			WHERE bb.work_id = w.id AND bb.deleted = false
		)
		OR EXISTS (
			SELECT 1 FROM books bb
			WHERE bb.work_id = w.id AND bb.deleted = false AND bb.rating > 0
		)
	)`
}

func (b *RenownBackfiller) drain(ctx context.Context) int {
	b.found.Store(0)
	total := 0
	var cursor int64
	for ctx.Err() == nil {
		batch, err := b.fetchBatch(ctx, cursor, renownBatchSize)
		if err != nil {
			b.logger.Warn("renown backfill: fetch batch failed", "err", err)
			break
		}
		if len(batch) == 0 {
			break
		}
		b.processBatch(ctx, batch)
		b.syncTouched(ctx)
		total += len(batch)
		cursor = batch[len(batch)-1].id
	}
	return total
}

func (b *RenownBackfiller) fetchBatch(ctx context.Context, afterID int64, limit int) ([]renownCandidate, error) {
	// e — представительное издание работы (якорь → min id): его src_*/isbn/lang
	// питают внешний запрос; JOIN LATERAL заодно требует ≥1 живого издания.
	q := fmt.Sprintf(`
		SELECT w.id, w.title,
		       COALESCE(e.lang, ''), COALESCE(e.isbn, ''),
		       COALESCE(e.src_title, ''), COALESCE(e.src_author_normalized::text, ''), COALESCE(e.src_lang, ''),
		       COALESCE((
		           SELECT array_agg(DISTINCT TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name)))
		           FROM book_authors ba
		           JOIN authors a ON a.id = ba.author_id
		           JOIN books bb  ON bb.id = ba.book_id
		           WHERE bb.work_id = w.id AND bb.deleted = false
		       ), '{}'),
		       COALESCE(pa.last_name, ''), COALESCE(pa.first_name, ''),
		       COALESCE(w.ext_ids->>'wd_qid', '')
		FROM works w
		LEFT JOIN authors pa ON pa.id = w.primary_author_id
		JOIN LATERAL (
		    SELECT bb.lang, bb.isbn, bb.src_title, bb.src_author_normalized, bb.src_lang
		    FROM books bb
		    WHERE bb.work_id = w.id AND bb.deleted = false
		    ORDER BY (bb.normalized_title = w.normalized_title) DESC, bb.id
		    LIMIT 1
		) e ON true
		WHERE w.id > $1
		  AND %s
		ORDER BY w.id
		LIMIT $2
	`, b.candidateCond())
	rows, err := b.pool.Query(ctx, q, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]renownCandidate, 0, limit)
	for rows.Next() {
		var c renownCandidate
		if err := rows.Scan(&c.id, &c.title, &c.lang, &c.isbn,
			&c.srcTitle, &c.srcAuthorNorm, &c.srcLang,
			&c.authors, &c.lastName, &c.firstName, &c.wdQID); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (b *RenownBackfiller) processBatch(ctx context.Context, batch []renownCandidate) {
	sem := make(chan struct{}, renownWorkers)
	var wg sync.WaitGroup
	for _, c := range batch {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(c renownCandidate) {
			defer wg.Done()
			defer func() { <-sem }()
			b.processOne(ctx, c)
		}(c)
	}
	wg.Wait()
}

type renownSource struct {
	provider RenownProvider
	gate     *rateGate
	query    WorkQuery
}

// sources — включённые источники с per-source запросом: Fantlab ищет нативно
// (works.title локализован на язык библиотеки, кириллический автор), OL — по
// оригиналу для переводных (src_title + латинский автор, как rating/cover),
// Wikidata — резолв QID по названию (или готовый QID из ext_ids) → sitelinks.
func (b *RenownBackfiller) sources(c renownCandidate) []renownSource {
	base := WorkQuery{
		BookID:    c.id, // work id: для WorkQuery это только контекст логов
		ISBN:      c.isbn,
		LastName:  c.lastName,
		FirstName: c.firstName,
	}
	var out []renownSource
	if b.cfg.Fantlab && b.fl != nil {
		q := base
		q.Title = c.title
		q.Authors = c.authors
		out = append(out, renownSource{b.fl, b.flGate, q})
	}
	if b.cfg.OpenLibrary && b.ol != nil {
		title, authors, lang := externalTitleAuthorLang(externalQueryFields{
			id: c.id, title: c.title, lang: c.lang, authors: c.authors,
			srcTitle: c.srcTitle, srcAuthorNorm: c.srcAuthorNorm, srcLang: c.srcLang,
		})
		q := base
		q.Title = title
		q.SrcTitle = c.srcTitle
		q.Authors = authors
		q.Lang = lang
		out = append(out, renownSource{b.ol, b.olGate, q})
	}
	if b.cfg.Wikidata && b.wd != nil {
		q := base
		q.Title = c.title
		q.SrcTitle = c.srcTitle // резолвер сам предпочтёт оригинал
		q.Authors = c.authors
		q.Lang = c.lang
		q.WikidataQID = c.wdQID // готовый QID из Tier-2 — резолв пропускается
		out = append(out, renownSource{b.wd, b.wdGate, q})
	}
	return out
}

func (b *RenownBackfiller) processOne(ctx context.Context, c renownCandidate) {
	lookups, err := b.loadLookups(ctx, c.id)
	if err != nil {
		b.logger.Warn("renown backfill: load lookups failed", "work_id", c.id, "err", err)
		return
	}
	now := time.Now()
	gotAny := false
	for _, src := range b.sources(c) {
		name := src.provider.Name()
		if !b.isDue(lookups[name], now) {
			continue
		}
		taskCtx, cancel := context.WithTimeout(ctx, renownTaskTimeout)
		if werr := src.gate.wait(taskCtx); werr != nil {
			cancel()
			return // воркер останавливают — выходим, ничего не помечая
		}
		res, ferr := src.provider.FetchRenown(taskCtx, src.query)
		cancel()

		switch {
		case ferr == nil && res.total() > 0:
			b.writeRenown(ctx, c.id, name, res)
			b.upsertLookup(ctx, c.id, name, "found")
			b.found.Add(1)
			gotAny = true
		case errors.Is(ferr, ErrNotFound):
			b.upsertLookup(ctx, c.id, name, "not_found")
		case ctx.Err() != nil:
			return // отмена воркера, не записываем как ошибку источника
		default:
			b.logger.Info("renown backfill: provider error", "source", name, "work_id", c.id, "err", ferr)
			b.upsertLookup(ctx, c.id, name, "error")
		}
	}
	if gotAny {
		b.mu.Lock()
		b.touched = append(b.touched, c.id)
		b.mu.Unlock()
	}
}

// writeRenown — записать счётчики источника в свои колонки works.
func (b *RenownBackfiller) writeRenown(ctx context.Context, workID int64, source string, res RenownResult) {
	var err error
	switch source {
	case "fantlab":
		_, err = b.pool.Exec(ctx,
			`UPDATE works SET fantlab_marks = $2, updated_at = now() WHERE id = $1`, workID, res.Ratings)
		// Типизация от Фантлаба (курируемая — надёжнее эвристики): collection/
		// anthology → пишем kind; "novel" — уверенно обычное произведение →
		// СНИМАЕМ ошибочную эвристику (kind → NULL). kind_source='fantlab' в
		// обоих случаях (запоминаем уверенность — эвристика не перетрёт, см.
		// ClassifyWorkKinds). Ручной override неприкосновенен. "" (цикл/статья/
		// незнакомый тип) — не решаем, ничего не трогаем.
		if err == nil && res.Kind != "" {
			kind := &res.Kind
			if res.Kind == "novel" {
				kind = nil
			}
			_, err = b.pool.Exec(ctx, `
				UPDATE works SET kind = $2, kind_source = 'fantlab', updated_at = now()
				WHERE id = $1 AND kind_source IS DISTINCT FROM 'override'`, workID, kind)
		}
	case "openlibrary":
		_, err = b.pool.Exec(ctx,
			`UPDATE works SET ol_ratings_count = $2, ol_want_count = $3, updated_at = now() WHERE id = $1`,
			workID, res.Ratings, res.Want)
	case "wikidata":
		_, err = b.pool.Exec(ctx,
			`UPDATE works SET wd_sitelinks = $2, updated_at = now() WHERE id = $1`, workID, res.Sitelinks)
	default:
		err = fmt.Errorf("unknown renown source %q", source)
	}
	if err != nil {
		b.logger.Warn("renown backfill: write failed", "work_id", workID, "source", source, "err", err)
	}
}

// syncTouched — таргетный ресинк работ с новыми счётчиками (пересчёт
// popularity в works-индексе тем же scanWorkDocs).
func (b *RenownBackfiller) syncTouched(ctx context.Context) {
	b.mu.Lock()
	ids := b.touched
	b.touched = nil
	b.mu.Unlock()
	if len(ids) == 0 || b.resyncer == nil || ctx.Err() != nil {
		return
	}
	if err := b.resyncer.UpsertWorksToIndex(ctx, ids); err != nil {
		b.logger.Warn("renown backfill: works index sync failed", "count", len(ids), "err", err)
	}
}

func (b *RenownBackfiller) loadLookups(ctx context.Context, workID int64) (map[string]lookupRow, error) {
	rows, err := b.pool.Query(ctx,
		`SELECT source, outcome, checked_at FROM work_renown_lookups WHERE work_id = $1`, workID)
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

// isDue — пора ли (пере)спрашивать источник: нет строки → да; found → по
// FoundRefreshDays (известность растёт, но медленно; 0 = не освежать);
// not_found / error — по своим TTL.
func (b *RenownBackfiller) isDue(l lookupRow, now time.Time) bool {
	switch l.outcome {
	case "":
		return true
	case "found":
		if b.cfg.FoundRefreshDays <= 0 {
			return false
		}
		return now.Sub(l.checkedAt) >= time.Duration(b.cfg.FoundRefreshDays)*24*time.Hour
	case "not_found":
		return now.Sub(l.checkedAt) >= time.Duration(b.cfg.NotFoundRetryDays)*24*time.Hour
	case "error":
		return now.Sub(l.checkedAt) >= time.Duration(b.cfg.ErrorRetryHours)*time.Hour
	default:
		return true
	}
}

func (b *RenownBackfiller) upsertLookup(ctx context.Context, workID int64, source, outcome string) {
	if _, err := b.pool.Exec(ctx, `
		INSERT INTO work_renown_lookups (work_id, source, outcome, checked_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (work_id, source)
		DO UPDATE SET outcome = EXCLUDED.outcome, checked_at = now()
	`, workID, source, outcome); err != nil {
		b.logger.Warn("renown backfill: upsert lookup failed", "work_id", workID, "source", source, "err", err)
	}
}

// ── Controller: рантайм-управление воркером (зеркало ExternalRatingBackfillController) ──

// RenownBackfillStatus — состояние воркера для админ-UI.
type RenownBackfillStatus struct {
	Running bool   `json:"renown_running"`
	Mode    string `json:"renown_mode"` // "off" | "continuous" | "once"
}

// RenownCoverage — покрытие счётчиками известности (для админ-статистики).
// HeadTotal — размер ядра (кандидаты дефолтного охвата), WithFantlab/
// WithOL — работы с заполненными счётчиками, BySource — found-строки lookups.
type RenownCoverage struct {
	Total       int            `json:"total"`
	HeadTotal   int            `json:"head_total"`
	WithAny     int            `json:"with_any"`
	WithFantlab int            `json:"with_fantlab"`
	WithOL      int            `json:"with_ol"`
	WithWD      int            `json:"with_wd"`
	BySource    map[string]int `json:"by_source"`
}

type RenownBackfillController struct {
	pool     *pgxpool.Pool
	fl       RenownProvider
	ol       RenownProvider
	wd       RenownProvider
	resyncer WorksIndexSyncer
	logger   *slog.Logger

	mu         sync.Mutex
	cfg        RenownBackfillConfig
	contCancel context.CancelFunc
	onceCancel context.CancelFunc
}

func NewRenownBackfillController(pool *pgxpool.Pool, fl, ol, wd RenownProvider, resyncer WorksIndexSyncer, cfg RenownBackfillConfig, logger *slog.Logger) *RenownBackfillController {
	if logger == nil {
		logger = slog.Default()
	}
	return &RenownBackfillController{pool: pool, fl: fl, ol: ol, wd: wd, resyncer: resyncer, cfg: cfg, logger: logger}
}

func (c *RenownBackfillController) ready() bool {
	return c.pool != nil && (c.fl != nil || c.ol != nil || c.wd != nil)
}

// ResetFailedLookups удаляет неудачные попытки (not_found/error) из
// work_renown_lookups — работы перепроверятся на следующем проходе. found не трогаем.
func (c *RenownBackfillController) ResetFailedLookups(ctx context.Context) (int64, error) {
	if c.pool == nil {
		return 0, nil
	}
	tag, err := c.pool.Exec(ctx, `DELETE FROM work_renown_lookups WHERE outcome IN ('not_found', 'error')`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (c *RenownBackfillController) Status() RenownBackfillStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.onceCancel != nil:
		return RenownBackfillStatus{Running: true, Mode: "once"}
	case c.contCancel != nil:
		return RenownBackfillStatus{Running: true, Mode: "continuous"}
	default:
		return RenownBackfillStatus{Running: false, Mode: "off"}
	}
}

func (c *RenownBackfillController) Start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.contCancel != nil || !c.ready() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.contCancel = cancel
	b := NewRenownBackfiller(c.pool, c.fl, c.ol, c.wd, c.resyncer, c.cfg, c.logger)
	go b.Run(ctx)
	c.logger.Info("renown backfill: continuous job started")
}

func (c *RenownBackfillController) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.contCancel == nil {
		return
	}
	c.contCancel()
	c.contCancel = nil
	c.logger.Info("renown backfill: continuous job stopped")
}

// SetEnabled — тумблер «фоновый воркер вкл/выкл».
func (c *RenownBackfillController) SetEnabled(on bool) {
	if on {
		c.Start()
	} else {
		c.Stop()
	}
}

// SetConfig применяет новые параметры. Если непрерывный воркер запущен —
// перезапускает его, чтобы подхватить cfg.
func (c *RenownBackfillController) SetConfig(cfg RenownBackfillConfig) {
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
func (c *RenownBackfillController) RunOnce() {
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
		b := NewRenownBackfiller(c.pool, c.fl, c.ol, c.wd, c.resyncer, cfg, c.logger)
		n := b.drain(ctx)
		cancel()
		c.mu.Lock()
		c.onceCancel = nil
		c.mu.Unlock()
		c.logger.Info("renown backfill: one-shot pass done", "processed", n)
	}()
}

// StopOnce — отменить идущий разовый проход.
func (c *RenownBackfillController) StopOnce() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.onceCancel == nil {
		return
	}
	c.onceCancel()
	c.logger.Info("renown backfill: one-shot pass stop requested")
}

// Coverage — покрытие счётчиками известности.
func (c *RenownBackfillController) Coverage(ctx context.Context) (RenownCoverage, error) {
	out := RenownCoverage{BySource: map[string]int{}}
	if c.pool == nil {
		return out, nil
	}
	// head_total — set-based (UNION), а НЕ per-work EXISTS: коррелированный EXISTS
	// по book_adaptations/books на КАЖДУЮ из ~500k works уходил за 5с (прод-краш
	// 1.8.0: таймаут → nil coverage). UNION трёх дешёвых множеств
	// (переиздания ∪ экранизация ∪ LIBRATE) считается за доли секунды.
	if err := c.pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM works),
		  (SELECT count(*) FROM (
		      SELECT id AS work_id FROM works WHERE COALESCE(edition_count, 1) >= 2
		      UNION
		      SELECT bb.work_id FROM book_adaptations ad JOIN books bb ON bb.id = ad.book_id
		        WHERE bb.deleted = false AND bb.work_id IS NOT NULL
		      UNION
		      SELECT bb.work_id FROM books bb
		        WHERE bb.deleted = false AND bb.rating > 0 AND bb.work_id IS NOT NULL
		  ) head),
		  (SELECT count(*) FROM works WHERE fantlab_marks IS NOT NULL
		      OR ol_ratings_count IS NOT NULL OR ol_want_count IS NOT NULL OR wd_sitelinks IS NOT NULL),
		  (SELECT count(*) FROM works WHERE fantlab_marks IS NOT NULL),
		  (SELECT count(*) FROM works WHERE ol_ratings_count IS NOT NULL OR ol_want_count IS NOT NULL),
		  (SELECT count(*) FROM works WHERE wd_sitelinks IS NOT NULL)
	`).Scan(&out.Total, &out.HeadTotal, &out.WithAny, &out.WithFantlab, &out.WithOL, &out.WithWD); err != nil {
		return out, err
	}
	rows, err := c.pool.Query(ctx, `
		SELECT source, count(*)
		FROM work_renown_lookups
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
