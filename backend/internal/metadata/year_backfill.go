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

	"github.com/skriptes/skriptes/backend/internal/metrics"
)

// YearBackfiller — фоновое дозаполнение written_year из ВНЕШНЕГО источника
// (Wikidata P577) для книг, у которых год не извлёкся локально из fb2.
// OpenLibrary убран в 1.39.4: его first_publish_year — самое раннее издание,
// которое знает каталог, и для русских книг это год переиздания (прод
// 2026-10-11: верно 59 % по латинским названиям, ~30 % по кириллическим, против
// 94 % у Wikidata; ~/projects/plans/skriptes/archive/ol-year-translit-dryrun.md). В отличие от прогрева обложек ходит в сеть,
// поэтому: opt-in (выключен по умолчанию), низкая конкуренция, per-source
// rate-limit и per-source учёт попыток (book_year_lookups), чтобы не долбить
// один источник повторно.
//
// Кандидаты: written_year IS NULL AND year_local_scanned_at IS NOT NULL —
// локальная fb2-фаза уже отработала, года нет → пробуем внешние.
type YearBackfiller struct {
	pool     *pgxpool.Pool
	wd       YearProvider // nil → источник недоступен
	logger   *slog.Logger
	cfg      YearBackfillConfig
	wdGate   *rateGate
	resyncer YearResyncer // nil → без авто-ресинка Meili-года

	yearChanged atomic.Int64 // сколько книг получили written_year за проход
	lookedUp    atomic.Int64 // сколько запросов к источникам сделано за проход (для логов)

	changedMu    sync.Mutex // processBatch гоняет writeFound из нескольких горутин
	changedBooks []int64    // id книг, у которых год появился (для works-индекса)
}

// YearBackfillConfig — рантайм-параметры воркера (зеркало
// settings.YearEnrichmentConfig; передаётся значениями, без зависимости
// metadata→settings).
type YearBackfillConfig struct {
	Wikidata          bool
	WholeCollection   bool
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
func NewYearBackfiller(pool *pgxpool.Pool, wd YearProvider, cfg YearBackfillConfig, resyncer YearResyncer, logger *slog.Logger) *YearBackfiller {
	if logger == nil {
		logger = slog.Default()
	}
	b := &YearBackfiller{
		pool: pool, wd: wd, cfg: cfg, resyncer: resyncer, logger: logger,
		wdGate: &rateGate{},
	}
	b.wdGate.setRPM(cfg.WikidataRPM)
	return b
}

// Run — долгоживущий цикл: дозаполнить все pending-книги, поспать, пересканить
// (новые книги / истёкшие TTL). Блокирующий; вызывать в горутине.
func (b *YearBackfiller) Run(ctx context.Context) {
	if b.pool == nil || b.wd == nil {
		return
	}
	b.logger.Info("year backfill: started", "workers", yearBackfillWorkers)
	for {
		n := b.drain(ctx)
		if ctx.Err() != nil {
			return
		}
		if lookups := b.lookedUp.Load(); n > 0 && lookups > 0 {
			b.logger.Info("year backfill: pass complete", "candidates", n, "lookups", lookups)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(yearBackfillRescanInterval):
		}
	}
}

type yearCandidate struct {
	id            int64
	title         string
	lang          string
	authors       []string
	srcTitle      string
	srcAuthorNorm string
	srcLang       string

	// Что о книге уже знают Wikidata-пути других воркеров (#294), см. wdHintColumns.
	wdQID        string     // QID работы из works.ext_ids
	wdNotFoundAt *time.Time // свежее «не найдено» Tier-2 группировки по этой книге
}

// queryFields — поля внешнего поискового запроса (buildExternalQuery).
func (c yearCandidate) queryFields() externalQueryFields {
	return externalQueryFields{
		id: c.id, title: c.title, lang: c.lang, authors: c.authors,
		srcTitle: c.srcTitle, srcAuthorNorm: c.srcAuthorNorm, srcLang: c.srcLang,
	}
}

// wdHintColumns — колонки к выборке кандидатов (алиас books = b): QID работы,
// который уже нашли Tier-2 группировки или «Известность», и время «не найдено»
// Tier-2 группировки по этой же книге (#294). Ставятся после authors в SELECT.
const wdHintColumns = `,
		       COALESCE((SELECT w.ext_ids->>'wd_qid' FROM works w WHERE w.id = b.work_id), ''),
		       (SELECT l.checked_at FROM book_work_lookups l
		         WHERE l.book_id = b.id AND l.source = 'wikidata' AND l.outcome = 'not_found')`

// wikidataShortcut — как спросить Wikidata о книге с учётом того, что уже
// известно (#294). Есть QID работы — он уходит в запрос, поиск книги по
// названию не нужен. Нет QID, а Tier-2 группировки недавно (моложе notFoundTTL)
// не нашёл книгу тем же запросом — reuseNotFound: ответ «не найдено» без
// запроса. Запрос совпадает, только когда у книги нет названия оригинала:
// иначе группировка ищет по src_title с авторами издания, а год и язык
// оригинала — по src_title с латинским автором.
func (c yearCandidate) wikidataShortcut(q *BookQuery, notFoundTTL time.Duration, now time.Time) (reuseNotFound bool) {
	if isQID(c.wdQID) {
		q.WikidataQID = c.wdQID
		return false
	}
	return c.srcTitle == "" && c.wdNotFoundAt != nil && now.Sub(*c.wdNotFoundAt) < notFoundTTL
}

func (b *YearBackfiller) drain(ctx context.Context) int {
	b.yearChanged.Store(0)
	b.lookedUp.Store(0)
	b.changedMu.Lock()
	b.changedBooks = nil
	b.changedMu.Unlock()
	total := 0
	// Двухфазный обход «сначала ядро, потом хвост» (bookCoreCond): год приходит
	// знаменитым/переиздаваемым книгам на дни раньше, полнота прохода не меняется.
	// Кандидат, обработанный в фазе ядра, во второй фазе не всплывает (условия
	// взаимоисключающие); TTL lookups страхует от повторов между пересканами.
	for _, phase := range corePhases {
		ttl := b.ttl().forPhase(phase)
		var cursor int64
		for ctx.Err() == nil {
			batch, err := b.fetchBatch(ctx, cursor, yearBackfillBatchSize, phase.cond, ttl)
			if err != nil {
				b.logger.Warn("year backfill: fetch batch failed", "err", err)
				break
			}
			if len(batch) == 0 {
				break
			}
			b.processBatch(ctx, batch, ttl)
			total += len(batch)
			cursor = batch[len(batch)-1].id
		}
	}
	if b.yearChanged.Load() == 0 || ctx.Err() != nil {
		return total
	}
	// Год работы — по правилам work_years.go (потолок — самое раннее издание, не
	// раньше рождения автора). До 1.39.3 воркер его не пересчитывал, и найденный
	// год книги шёл на карточку как есть (COALESCE(w.written_year, b.written_year)).
	workIDs := b.changedWorkIDs(ctx)
	if _, err := RecomputeWorkYears(ctx, b.pool, workIDs); err != nil {
		b.logger.Warn("year backfill: recompute work years failed", "err", err)
	}
	// Авто-синк Meili-поля year, если за проход год у книг появился.
	if b.resyncer != nil {
		if n, err := b.resyncer.ResyncYears(ctx); err != nil {
			b.logger.Warn("year backfill: resync years failed", "err", err)
		} else {
			b.logger.Info("year backfill: years resynced to meili", "changed", b.yearChanged.Load(), "synced", n)
		}
		// Год работы в индексе works = COALESCE(work.written_year, min года изданий) —
		// таргетно пере-собираем работы изменённых книг, чтобы /books-список (по
		// works) не отставал по году/фасету. Только фоновый проход; лёгкий лениво-
		// путь EnrichBooksNow works-индекс не трогает (наполнится на следующем
		// полном ресинке импорта/группировки).
		if syncer, ok := b.resyncer.(WorksIndexSyncer); ok {
			if len(workIDs) > 0 {
				if err := syncer.UpsertWorksToIndex(ctx, workIDs); err != nil {
					b.logger.Warn("year backfill: upsert works to index failed", "err", err)
				}
			}
		}
	}
	return total
}

// changedWorkIDs — distinct work_id книг, у которых за проход появился год.
func (b *YearBackfiller) changedWorkIDs(ctx context.Context) []int64 {
	b.changedMu.Lock()
	ids := append([]int64(nil), b.changedBooks...)
	b.changedMu.Unlock()
	if len(ids) == 0 {
		return nil
	}
	rows, err := b.pool.Query(ctx,
		`SELECT DISTINCT work_id FROM books WHERE id = ANY($1) AND work_id IS NOT NULL`, ids)
	if err != nil {
		b.logger.Warn("year backfill: map changed books to works failed", "err", err)
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

// fetchBatch — страница кандидатов keyset'ом по id. phaseCond — доп. условие
// фазы приоритизации ("AND <core>" / "AND NOT <core>"), см. bookCoreCond.
// Только кандидаты, которых пора спросить хотя бы у одного включённого
// источника (dueCond), — остальных проход не перечитывает.
func (b *YearBackfiller) fetchBatch(ctx context.Context, afterID int64, limit int, phaseCond string, ttl lookupTTL) ([]yearCandidate, error) {
	q := fmt.Sprintf(`
		SELECT b.id, b.title, COALESCE(b.lang, ''),
		       COALESCE(b.src_title, ''), COALESCE(b.src_author_normalized::text, ''), COALESCE(b.src_lang, ''),
		       COALESCE(
		           array_agg(TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name)))
		           FILTER (WHERE a.id IS NOT NULL),
		           '{}'
		       ) AS authors%s
		FROM books b
		LEFT JOIN book_authors ba ON ba.book_id = b.id
		LEFT JOIN authors a       ON a.id = ba.author_id
		WHERE b.deleted = false
		  AND %s
		  %s
		  AND %s
		  AND b.id > $1
		GROUP BY b.id
		ORDER BY b.id
		LIMIT $2
	`, wdHintColumns, b.candidateCond(), phaseCond, dueCond("book_year_lookups", "book_id", "b.id", 3))
	args := append([]any{afterID, limit}, dueArgs(b.sourceNames(), ttl)...)
	rows, err := b.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]yearCandidate, 0, limit)
	for rows.Next() {
		var c yearCandidate
		if err := rows.Scan(&c.id, &c.title, &c.lang, &c.srcTitle, &c.srcAuthorNorm, &c.srcLang, &c.authors,
			&c.wdQID, &c.wdNotFoundAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (b *YearBackfiller) processBatch(ctx context.Context, batch []yearCandidate, ttl lookupTTL) {
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
			_ = b.processOne(ctx, c, ttl)
		}(c)
	}
	wg.Wait()
}

type yearSource struct {
	provider YearProvider
	gate     *rateGate
}

// sources — включённые внешние источники: Wikidata (P577).
func (b *YearBackfiller) sources() []yearSource {
	var out []yearSource
	if b.cfg.Wikidata && b.wd != nil {
		out = append(out, yearSource{b.wd, b.wdGate})
	}
	return out
}

// sourceNames — имена включённых источников (как они записаны в учёте попыток).
func (b *YearBackfiller) sourceNames() []string {
	var out []string
	for _, src := range b.sources() {
		out = append(out, src.provider.Name())
	}
	return out
}

// processOne — спросить включённые источники, которым пора (сроки ttl — фазы
// обхода, см. enrichPhase). true — год книге записан.
func (b *YearBackfiller) processOne(ctx context.Context, bk yearCandidate, ttl lookupTTL) bool {
	lookups, err := b.loadLookups(ctx, bk.id)
	if err != nil {
		b.logger.Warn("year backfill: load lookups failed", "book_id", bk.id, "err", err)
		return false
	}
	now := time.Now()
	q := buildExternalQuery(bk.queryFields())

	for _, src := range b.sources() {
		name := src.provider.Name()
		if !ttl.isDue(lookups[name], now) {
			continue
		}
		if name == wikidataSource && bk.wikidataShortcut(&q, ttl.notFound, now) {
			b.recordReusedNotFound(ctx, bk.id, name)
			continue
		}
		taskCtx, cancel := context.WithTimeout(ctx, yearBackfillTaskTimeout)
		if werr := src.gate.wait(taskCtx); werr != nil {
			cancel()
			return false // воркер останавливают — выходим, ничего не помечая
		}
		b.lookedUp.Add(1)
		year, ferr := src.provider.FetchYear(taskCtx, q)
		cancel()

		switch {
		case ferr == nil && year > 0:
			werr := b.writeFound(ctx, bk.id, name, year)
			if errors.Is(werr, errYearAfterEdition) {
				// Год написания не бывает позже года этого издания: источник нашёл
				// не то (перевод, переиздание) — «не найдено», спросим следующий.
				b.logger.Info("year backfill: year after edition — rejected", "source", name, "book_id", bk.id, "year", year)
				b.upsertLookup(ctx, bk.id, name, "not_found", 0)
				continue
			}
			if werr != nil {
				b.logger.Warn("year backfill: write found failed", "book_id", bk.id, "err", werr)
				return false
			}
			b.logger.Info("year backfill: year found", "source", name, "book_id", bk.id, "year", year)
			return true // год есть — остальные источники не нужны
		case errors.Is(ferr, ErrNotFound):
			b.upsertLookup(ctx, bk.id, name, "not_found", 0)
		case errors.Is(ferr, ErrSourcePaused):
			continue // источник на паузе (#299): запрос не ушёл, книгу возьмём позже
		case ctx.Err() != nil:
			return false // отмена воркера, не записываем как ошибку источника
		default:
			b.logger.Info("year backfill: provider error", "source", name, "book_id", bk.id, "err", ferr)
			b.upsertLookup(ctx, bk.id, name, "error", 0)
		}
	}
	return false
}

// EnrichOne — разовое внешнее дозаполнение года для ОДНОЙ книги (ленивый путь
// при открытии карточки серии/автора). Использует ту же машинерию, что фоновый
// проход: per-source TTL (book_year_lookups), rate-gate, порядок источников из
// cfg. Никаких новых правил/обхода лимитов.
func (b *YearBackfiller) EnrichOne(ctx context.Context, id int64, title, lang string, authors []string) {
	if !b.processOne(ctx, yearCandidate{id: id, title: title, lang: lang, authors: authors}, b.ttl()) {
		return
	}
	// Год работы — по тем же правилам, что после фонового прохода.
	var workID *int64
	if err := b.pool.QueryRow(ctx, `SELECT work_id FROM books WHERE id = $1`, id).Scan(&workID); err != nil || workID == nil {
		return
	}
	if _, err := RecomputeWorkYears(ctx, b.pool, []int64{*workID}); err != nil {
		b.logger.Warn("year enrich: recompute work year failed", "book_id", id, "err", err)
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

// ttl — сроки перепроверки: found окончательный, not_found / error — по конфигу.
func (b *YearBackfiller) ttl() lookupTTL {
	return retryTTL(b.cfg.NotFoundRetryDays, b.cfg.ErrorRetryHours)
}

// isDue — пора ли (пере)спрашивать источник: нет строки → да; found → нет;
// not_found / error → да, если старше соответствующего TTL.
func (b *YearBackfiller) isDue(l lookupRow, now time.Time) bool {
	return b.ttl().isDue(l, now)
}

// errYearAfterEdition — внешний год позже года издания этой книги (известного,
// не заглушки раньше minEditionYear): год написания таким быть не может.
var errYearAfterEdition = errors.New("year after edition year")

func (b *YearBackfiller) writeFound(ctx context.Context, bookID int64, source string, year int) error {
	tag, err := b.pool.Exec(ctx, `
		UPDATE books SET
			written_year = COALESCE(written_year, $2::smallint),
			written_year_source = CASE WHEN written_year IS NULL THEN $3 ELSE written_year_source END
		WHERE id = $1
		  AND NOT COALESCE(edition_year BETWEEN $4 AND $5 AND $2::smallint > edition_year, false)
	`, bookID, year, source, minEditionYear, time.Now().Year())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errYearAfterEdition
	}
	b.upsertLookup(ctx, bookID, source, "found", year)
	b.yearChanged.Add(1)
	b.changedMu.Lock()
	b.changedBooks = append(b.changedBooks, bookID)
	b.changedMu.Unlock()
	return nil
}

func (b *YearBackfiller) upsertLookup(ctx context.Context, bookID int64, source, outcome string, year int) {
	metrics.EnrichmentLookups.WithLabelValues("year", source, outcome).Inc()
	b.storeLookup(ctx, bookID, source, outcome, year)
}

// recordReusedNotFound — «не найдено» без запроса: Tier-2 группировки недавно не
// нашёл книгу в Wikidata тем же запросом (#294). В метриках — исход reused.
func (b *YearBackfiller) recordReusedNotFound(ctx context.Context, bookID int64, source string) {
	metrics.EnrichmentLookups.WithLabelValues("year", source, "reused").Inc()
	b.storeLookup(ctx, bookID, source, "not_found", 0)
}

func (b *YearBackfiller) storeLookup(ctx context.Context, bookID int64, source, outcome string, year int) {
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
	wd       YearProvider
	resyncer YearResyncer
	logger   *slog.Logger

	mu         sync.Mutex
	cfg        YearBackfillConfig
	contCancel context.CancelFunc
	onceCancel context.CancelFunc
}

func NewYearBackfillController(pool *pgxpool.Pool, wd YearProvider, cfg YearBackfillConfig, resyncer YearResyncer, logger *slog.Logger) *YearBackfillController {
	if logger == nil {
		logger = slog.Default()
	}
	return &YearBackfillController{pool: pool, wd: wd, resyncer: resyncer, cfg: cfg, logger: logger}
}

func (c *YearBackfillController) ready() bool {
	return c.pool != nil && c.wd != nil
}

// ResetFailedLookups удаляет неудачные попытки (not_found/error) из
// book_year_lookups — книги перепроверятся на следующем проходе (напр. после
// улучшения поиска: кириллица → src_title). 'found' не трогаем. Возвращает число.
func (c *YearBackfillController) ResetFailedLookups(ctx context.Context) (int64, error) {
	if c.pool == nil {
		return 0, nil
	}
	tag, err := c.pool.Exec(ctx, `DELETE FROM book_year_lookups WHERE outcome IN ('not_found', 'error')`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
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
	ctx, cancel := context.WithCancel(workersCtx)
	c.contCancel = cancel
	b := NewYearBackfiller(c.pool, c.wd, c.cfg, c.resyncer, c.logger)
	spawn(func() { b.Run(ctx) })
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
	ctx, cancel := context.WithCancel(workersCtx)
	c.onceCancel = cancel
	cfg := c.cfg
	c.mu.Unlock()
	spawn(func() {
		b := NewYearBackfiller(c.pool, c.wd, cfg, c.resyncer, c.logger)
		n := b.drain(ctx)
		cancel()
		c.mu.Lock()
		c.onceCancel = nil
		c.mu.Unlock()
		c.logger.Info("year backfill: one-shot pass done", "candidates", n, "lookups", b.lookedUp.Load())
	})
}

// LazyBook — книга-кандидат для ленивого внешнего дозаполнения года.
type LazyBook struct {
	ID      int64
	Title   string
	Lang    string
	Authors []string
}

// EnrichBooksNow — ленивое внешнее дозаполнение года для книг карточки
// серии/автора. Строит ОДИН воркер с текущим cfg (общий rate-gate серилизует
// вызовы по RPM), идёт по списку через ту же processOne-машинерию, затем синкает
// Meili-поле year, если год появился. No-op, если внешние источники не
// сконфигурены. Блокирующий — звать из горутины с детач-ctx.
func (c *YearBackfillController) EnrichBooksNow(ctx context.Context, books []LazyBook) {
	if !c.ready() || len(books) == 0 {
		return
	}
	c.mu.Lock()
	cfg := c.cfg
	c.mu.Unlock()
	b := NewYearBackfiller(c.pool, c.wd, cfg, c.resyncer, c.logger)
	b.yearChanged.Store(0)
	for _, bk := range books {
		if ctx.Err() != nil {
			return
		}
		b.EnrichOne(ctx, bk.ID, bk.Title, bk.Lang, bk.Authors)
	}
	if b.resyncer != nil && b.yearChanged.Load() > 0 && ctx.Err() == nil {
		if n, err := b.resyncer.ResyncYears(ctx); err != nil {
			c.logger.Warn("year lazy: resync years failed", "err", err)
		} else {
			c.logger.Info("year lazy: years resynced to meili", "changed", b.yearChanged.Load(), "synced", n)
		}
	}
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
