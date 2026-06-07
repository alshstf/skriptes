package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// WorkGrouper — фоновая группировка ИЗДАНИЙ (строк books) в логические КНИГИ
// (works). Работает ПО АВТОРУ (blast radius = один автор): никогда не сливает
// книги разных primary-авторов. Философия — precision > recall: при сомнении
// оставляем отдельную работу.
//
// Tier-1 (без сети, всегда): союзим издания по
//   - (нормализованное название, язык) — дубли одного языка;
//   - fb2_doc_id — точный дубль файла;
//   - <src-title-info>: перевод ↔ оригинал (по оригинальному названию+языку)
//     и переводы между собой (по src-автор+src-название+src-язык).
//
// Tier-2 (opt-in, rate-gated): резолв внешнего Work ID (OpenLibrary Work /
// Wikidata QID); издания с одинаковым (источник, work_key) союзятся.
//
// Кандидаты: work_scanned_at IS NULL (в режиме фолбэка — ещё и
// edition_meta_scanned_at IS NOT NULL, чтобы были src-ключи). После обработки
// книга помечается work_scanned_at, чтобы не гонять повторно (TTL для Tier-2 —
// в book_work_lookups).
// WorkIDResyncer пере-синкивает Meili-поле work_id из books.work_id
// (реализуется *importer.Importer). Группировка дёргает после прохода, в
// котором work_id у изданий менялся — чтобы distinctAttribute=work_id в поиске
// схлопывал по актуальной работе.
type WorkIDResyncer interface {
	ResyncWorkIDs(ctx context.Context) (int, error)
}

// WorksIndexSyncer — таргетный синк индекса works в Meili (реализуется
// *importer.Importer). Группировка/split/merge вызывают его после изменения
// состава работ: upsert изменённых, delete осиротевших (GC). Опционален —
// получаем type-assert'ом из WorkIDResyncer, чтобы не менять конструкторы.
type WorksIndexSyncer interface {
	UpsertWorksToIndex(ctx context.Context, ids []int64) error
	DeleteWorksFromIndex(ctx context.Context, ids []int64) error
}

type WorkGrouper struct {
	pool      *pgxpool.Pool
	resolvers []WorkKeyResolver    // включённые внешние источники (Tier-2), в порядке приоритета
	gates     map[string]*rateGate // per-source rate-gate
	cfg       WorkGroupConfig
	resyncer  WorkIDResyncer // nil → без авто-ресинка Meili
	logger    *slog.Logger

	merged       atomic.Int64       // сколько изданий переназначено за проход (для логов)
	touchedWorks map[int64]struct{} // канонические работы, изменённые за проход (для works-индекса)
	deletedWorks map[int64]struct{} // работы, удалённые (GC) за проход
	tier2Cursor  int64              // курсор по author_id для батчей внешнего краулера (Tier-2)
}

// WorkGroupConfig — рантайм-параметры (зеркало settings.WorkGroupingConfig;
// значением, без зависимости metadata→settings).
type WorkGroupConfig struct {
	OpenLibrary       bool
	Wikidata          bool
	WholeCollection   bool
	OpenLibraryRPM    int
	WikidataRPM       int
	NotFoundRetryDays int
	ErrorRetryHours   int
}

const (
	workGroupAuthorBatch    = 50
	workGroupRescanInterval = 30 * time.Minute
	workGroupTaskTimeout    = 60 * time.Second
)

// NewWorkGrouper строит воркер. ol/wd — внешние резолверы (nil → источник
// недоступен); фактическое включение — по cfg.OpenLibrary/Wikidata.
func NewWorkGrouper(pool *pgxpool.Pool, ol, wd WorkKeyResolver, cfg WorkGroupConfig, resyncer WorkIDResyncer, logger *slog.Logger) *WorkGrouper {
	if logger == nil {
		logger = slog.Default()
	}
	g := &WorkGrouper{
		pool: pool, cfg: cfg, resyncer: resyncer, logger: logger, gates: map[string]*rateGate{},
		touchedWorks: map[int64]struct{}{}, deletedWorks: map[int64]struct{}{},
	}
	if cfg.OpenLibrary && ol != nil {
		g.resolvers = append(g.resolvers, ol)
		gate := &rateGate{}
		gate.setRPM(cfg.OpenLibraryRPM)
		g.gates[ol.Name()] = gate
	}
	if cfg.Wikidata && wd != nil {
		g.resolvers = append(g.resolvers, wd)
		gate := &rateGate{}
		gate.setRPM(cfg.WikidataRPM)
		g.gates[wd.Name()] = gate
	}
	return g
}

// Run — долгоживущий цикл (в горутине). ЧЕРЕДУЕТ быстрый структурный sweep
// (Tier-1/1.5, без сети) и медленный внешний краулер (Tier-2, rate-gated), с
// приоритетом Tier-1: на каждой итерации сначала догруппировываем всё, что
// можно локально (и новые книги из импорта), затем делаем ОДИН батч Tier-2.
// Так Tier-1/1.5 НЕ заблокирован за rate-лимитом внешних источников (раньше
// Tier-2 был вшит в каждого автора и тормозил весь проход до ~RPM).
func (g *WorkGrouper) Run(ctx context.Context) {
	if g.pool == nil {
		return
	}
	g.logger.Info("work grouping: started", "tier2_sources", len(g.resolvers))
	for {
		g.sweepTier1(ctx)
		if ctx.Err() != nil {
			return
		}
		n2 := 0
		if len(g.resolvers) > 0 {
			n2 = g.crawlTier2Batch(ctx)
		}
		if ctx.Err() != nil {
			return
		}
		// Есть ещё due-кандидаты Tier-2 → сразу следующий батч (он сам rate-gated;
		// Tier-1 перепроверяется в начале каждой итерации). Иначе — спим.
		if n2 > 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(workGroupRescanInterval):
		}
	}
}

// sweepTier1 — БЫСТРЫЙ структурный проход без сети: все кандидаты
// (work_scanned_at IS NULL [+ edition_meta в fallback]), Tier-1/1.5 кластеризация
// + apply + пометка scanned. Идёт до исчерпания. Возвращает число авторов.
func (g *WorkGrouper) sweepTier1(ctx context.Context) int {
	g.resetPassState()
	total := 0
	var cursor int64
	for ctx.Err() == nil {
		authors, err := g.fetchCandidateAuthors(ctx, cursor, workGroupAuthorBatch)
		if err != nil {
			g.logger.Warn("work grouping: fetch tier-1 authors failed", "err", err)
			break
		}
		if len(authors) == 0 {
			break
		}
		for _, aid := range authors {
			if ctx.Err() != nil {
				break
			}
			if err := g.groupAuthorTier1(ctx, aid); err != nil {
				g.logger.Warn("work grouping: tier-1 author failed", "author_id", aid, "err", err)
			}
			total++
		}
		cursor = authors[len(authors)-1]
	}
	// Книги без авторов сгруппировать не по чему — помечаем подтверждёнными.
	g.markAuthorlessScanned(ctx)
	g.syncSearchAfterPass(ctx)
	if total > 0 {
		g.logger.Info("work grouping: tier-1 sweep done", "authors", total, "merged", g.merged.Load())
	}
	return total
}

// crawlTier2Batch — ОДИН батч внешнего краулера (если есть резолверы): авторы с
// singleton-работами, «due» по book_work_lookups; внешний резолв (rate-gated) +
// союз по Work ID. Курсор по author_id; исчерпался → сброс (по TTL кандидаты
// вернутся). Возвращает число авторов в батче (0 = больше нечего).
func (g *WorkGrouper) crawlTier2Batch(ctx context.Context) int {
	g.resetPassState()
	authors, err := g.fetchTier2Authors(ctx, g.tier2Cursor, workGroupAuthorBatch)
	if err != nil {
		g.logger.Warn("work grouping: fetch tier-2 authors failed", "err", err)
		return 0
	}
	if len(authors) == 0 {
		g.tier2Cursor = 0 // круг пройден — на следующем заходе due вернутся по TTL
		return 0
	}
	for _, aid := range authors {
		if ctx.Err() != nil {
			break
		}
		if err := g.groupAuthorTier2(ctx, aid); err != nil {
			g.logger.Warn("work grouping: tier-2 author failed", "author_id", aid, "err", err)
		}
	}
	g.tier2Cursor = authors[len(authors)-1]
	g.syncSearchAfterPass(ctx)
	if g.merged.Load() > 0 {
		g.logger.Info("work grouping: tier-2 batch merged", "authors", len(authors), "merged", g.merged.Load())
	}
	return len(authors)
}

// drainAll — полный проход: Tier-1 sweep + Tier-2 до исчерпания. Для кнопки
// «Запустить сейчас» и интеграционных тестов. Возвращает число обработанных
// авторов (Tier-1 + Tier-2).
func (g *WorkGrouper) drainAll(ctx context.Context) int {
	total := g.sweepTier1(ctx)
	if len(g.resolvers) > 0 {
		for ctx.Err() == nil {
			n := g.crawlTier2Batch(ctx)
			if n == 0 {
				break
			}
			total += n
		}
	}
	return total
}

// resetPassState обнуляет счётчики прохода (для логов + таргетного синка).
func (g *WorkGrouper) resetPassState() {
	g.merged.Store(0)
	g.touchedWorks = map[int64]struct{}{}
	g.deletedWorks = map[int64]struct{}{}
}

// syncSearchAfterPass синкает поиск, если за проход что-то менялось: books-индекс
// work_id (distinct/OPDS) + таргетный works-индекс (upsert изменённых, delete GC).
func (g *WorkGrouper) syncSearchAfterPass(ctx context.Context) {
	if g.resyncer != nil && g.merged.Load() > 0 && ctx.Err() == nil {
		if n, err := g.resyncer.ResyncWorkIDs(ctx); err != nil {
			g.logger.Warn("work grouping: resync work_id to meili failed", "err", err)
		} else {
			g.logger.Info("work grouping: work_id resynced to meili", "merged", g.merged.Load(), "synced", n)
		}
	}
	if syncer, ok := g.resyncer.(WorksIndexSyncer); ok && ctx.Err() == nil {
		if len(g.deletedWorks) > 0 {
			if err := syncer.DeleteWorksFromIndex(ctx, keysOf(g.deletedWorks)); err != nil {
				g.logger.Warn("work grouping: delete works from index failed", "err", err)
			}
		}
		if len(g.touchedWorks) > 0 {
			if err := syncer.UpsertWorksToIndex(ctx, keysOf(g.touchedWorks)); err != nil {
				g.logger.Warn("work grouping: upsert works to index failed", "err", err)
			}
		}
	}
}

func (g *WorkGrouper) candidateCond() string {
	if g.cfg.WholeCollection {
		return "b.work_scanned_at IS NULL"
	}
	return "b.work_scanned_at IS NULL AND b.edition_meta_scanned_at IS NOT NULL"
}

func (g *WorkGrouper) fetchCandidateAuthors(ctx context.Context, after int64, limit int) ([]int64, error) {
	q := fmt.Sprintf(`
		SELECT DISTINCT pa.author_id
		FROM books b
		JOIN LATERAL (
			SELECT ba.author_id FROM book_authors ba WHERE ba.book_id = b.id ORDER BY ba.position LIMIT 1
		) pa ON true
		WHERE b.deleted = false AND %s AND pa.author_id > $1
		ORDER BY pa.author_id
		LIMIT $2
	`, g.candidateCond())
	rows, err := g.pool.Query(ctx, q, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// fetchTier2Authors — кандидаты для внешнего краулера: авторы, у кого есть
// SINGLETON-работа (edition_count = 1), «due» по book_work_lookups — нет
// found-строки И не проверялось в пределах NotFoundRetryDays. Это грубый фильтр
// (избегает churn на полностью проверенных книгах); точный per-source isDue —
// внутри applyTier2. Курсор по author_id.
func (g *WorkGrouper) fetchTier2Authors(ctx context.Context, after int64, limit int) ([]int64, error) {
	ttlDays := g.cfg.NotFoundRetryDays
	if ttlDays <= 0 {
		ttlDays = 1
	}
	rows, err := g.pool.Query(ctx, `
		SELECT DISTINCT pa.author_id
		FROM books b
		JOIN LATERAL (
			SELECT ba.author_id FROM book_authors ba WHERE ba.book_id = b.id ORDER BY ba.position LIMIT 1
		) pa ON true
		JOIN works w ON w.id = b.work_id
		WHERE b.deleted = false
		  AND w.edition_count = 1
		  AND NOT EXISTS (SELECT 1 FROM book_work_lookups l WHERE l.book_id = b.id AND l.outcome = 'found')
		  AND NOT EXISTS (SELECT 1 FROM book_work_lookups l WHERE l.book_id = b.id AND l.checked_at > now() - make_interval(days => $3))
		  AND pa.author_id > $1
		ORDER BY pa.author_id
		LIMIT $2
	`, after, limit, ttlDays)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (g *WorkGrouper) markAuthorlessScanned(ctx context.Context) {
	cond := g.candidateCond()
	_, err := g.pool.Exec(ctx, fmt.Sprintf(`
		UPDATE books b SET work_scanned_at = now()
		WHERE b.deleted = false AND %s
		  AND NOT EXISTS (SELECT 1 FROM book_authors ba WHERE ba.book_id = b.id)
	`, cond))
	if err != nil {
		g.logger.Warn("work grouping: mark authorless scanned failed", "err", err)
	}
}

// groupBook — издание в памяти для кластеризации.
type groupBook struct {
	id            int64
	workID        int64
	title         string
	normTitle     string
	lang          string
	srcTitleNorm  string
	srcAuthorNorm string
	srcLang       string
	docID         string
	isbn          string
	seriesID      int64 // для Tier-1.5: (серия, ser_no) ⇒ одна работа
	serNo         int
	lastName      string
	firstName     string
	scanned       bool // work_scanned_at NOT NULL (контекст, не кандидат)
}

// groupAuthorTier1 — структурная группировка одного автора БЕЗ сети
// (Tier-1/1.5) + apply (пустой extByRoot). Быстрая фаза.
func (g *WorkGrouper) groupAuthorTier1(ctx context.Context, authorID int64) error {
	books, err := g.loadAuthorBooks(ctx, authorID)
	if err != nil {
		return err
	}
	if len(books) == 0 {
		return nil
	}
	uf := clusterTier1(books)
	return g.apply(ctx, authorID, books, uf, nil)
}

// groupAuthorTier2 — Tier-1 (восстановить uf) + внешний резолв Work ID
// (rate-gated, для due-одиночек) + союз по work_key + apply. Медленная фаза.
func (g *WorkGrouper) groupAuthorTier2(ctx context.Context, authorID int64) error {
	books, err := g.loadAuthorBooks(ctx, authorID)
	if err != nil {
		return err
	}
	if len(books) == 0 {
		return nil
	}
	uf := clusterTier1(books)
	resolvedExt := g.applyTier2(ctx, books, uf)
	return g.apply(ctx, authorID, books, uf, resolvedExt)
}

func (g *WorkGrouper) loadAuthorBooks(ctx context.Context, authorID int64) ([]groupBook, error) {
	rows, err := g.pool.Query(ctx, `
		SELECT b.id, b.work_id, b.title, b.normalized_title::text, COALESCE(b.lang,''),
		       COALESCE(b.src_title,''), COALESCE(b.src_author_normalized::text,''), COALESCE(b.src_lang,''),
		       COALESCE(b.fb2_doc_id,''), COALESCE(b.isbn,''),
		       COALESCE(b.series_id, 0), COALESCE(b.ser_no, 0),
		       (b.work_scanned_at IS NOT NULL),
		       a.last_name, COALESCE(a.first_name,'')
		FROM books b
		JOIN LATERAL (
			SELECT ba.author_id FROM book_authors ba WHERE ba.book_id = b.id ORDER BY ba.position LIMIT 1
		) pa ON true
		JOIN authors a ON a.id = pa.author_id
		WHERE b.deleted = false AND pa.author_id = $1
		ORDER BY b.id
	`, authorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []groupBook
	for rows.Next() {
		var b groupBook
		var srcTitle string
		if err := rows.Scan(&b.id, &b.workID, &b.title, &b.normTitle, &b.lang,
			&srcTitle, &b.srcAuthorNorm, &b.srcLang, &b.docID, &b.isbn,
			&b.seriesID, &b.serNo,
			&b.scanned, &b.lastName, &b.firstName); err != nil {
			return nil, err
		}
		b.srcTitleNorm = normalizePersonKey(srcTitle)
		out = append(out, b)
	}
	return out, rows.Err()
}

// clusterTier1 — союз изданий по внутренним (без сети) ключам. Возвращает
// union-find над индексами books. Чистая функция (тестируемо).
func clusterTier1(books []groupBook) *unionFind {
	uf := newUnionFind(len(books))
	byTitleLang := map[string][]int{} // (normTitle, lang) — оригинал/дубль одного языка
	byDoc := map[string][]int{}
	byTrans := map[string][]int{}    // (srcAuthorNorm, srcTitleNorm, srcLang) — переводы между собой
	bySeriesNo := map[string][]int{} // (series_id, ser_no) — Tier-1.5: один том серии ⇒ одна работа
	key := func(parts ...string) string {
		s := parts[0]
		for _, p := range parts[1:] {
			s += "\x00" + p
		}
		return s
	}
	for i, b := range books {
		byTitleLang[key(b.normTitle, b.lang)] = append(byTitleLang[key(b.normTitle, b.lang)], i)
		if b.docID != "" {
			byDoc[b.docID] = append(byDoc[b.docID], i)
		}
		if b.srcTitleNorm != "" && b.srcLang != "" {
			tk := key(b.srcAuthorNorm, b.srcTitleNorm, b.srcLang)
			byTrans[tk] = append(byTrans[tk], i)
		}
		if b.serNo > 0 && b.seriesID > 0 {
			snk := fmt.Sprintf("%d\x00%d", b.seriesID, b.serNo)
			bySeriesNo[snk] = append(bySeriesNo[snk], i)
		}
	}
	unionBucket := func(m map[string][]int) {
		for _, idxs := range m {
			for j := 1; j < len(idxs); j++ {
				uf.union(idxs[0], idxs[j])
			}
		}
	}
	unionBucket(byTitleLang)
	unionBucket(byDoc)
	unionBucket(byTrans)
	// Перевод ↔ оригинал: src(название,язык) перевода == (normTitle,lang) оригинала.
	for i, b := range books {
		if b.srcTitleNorm == "" || b.srcLang == "" {
			continue
		}
		for _, j := range byTitleLang[key(b.srcTitleNorm, b.srcLang)] {
			uf.union(i, j)
		}
	}
	// Tier-1.5: один том серии (series_id, ser_no) у одного автора ⇒ одна работа —
	// ловит разно-названные переводы без <src-title-info> и без сети. Гейт точности:
	// если в бакете ≥2 РАЗНЫХ непустых srcTitleNorm (конфликт оригиналов) — НЕ
	// союзим (это разные книги с одинаковым ser_no), оставляем другим тирам/ручному
	// merge. Пустой src конфликтом не считается.
	for _, idxs := range bySeriesNo {
		if len(idxs) < 2 {
			continue
		}
		srcs := map[string]struct{}{}
		for _, i := range idxs {
			if s := books[i].srcTitleNorm; s != "" {
				srcs[s] = struct{}{}
			}
		}
		if len(srcs) > 1 {
			continue // конфликт оригиналов — разные книги под одним ser_no
		}
		for j := 1; j < len(idxs); j++ {
			uf.union(idxs[0], idxs[j])
		}
	}
	return uf
}

// applyTier2 — для кандидатов-одиночек (после Tier-1) резолвит внешний Work ID,
// союзит издания с одинаковым (source, work_key), возвращает map[canonicalRoot]extJSON.
func (g *WorkGrouper) applyTier2(ctx context.Context, books []groupBook, uf *unionFind) map[int]map[string]string {
	extByRoot := map[int]map[string]string{}
	if len(g.resolvers) == 0 {
		return extByRoot
	}
	ids := make([]int64, len(books))
	idxByID := map[int64]int{}
	for i, b := range books {
		ids[i] = b.id
		idxByID[b.id] = i
	}
	lookups, err := g.loadWorkLookups(ctx, ids)
	if err != nil {
		g.logger.Warn("work grouping: load work lookups failed", "err", err)
		return extByRoot
	}
	// keyBuckets: (source\x00work_key) → индексы книг (уже найденные ранее + новые).
	keyBuckets := map[string][]int{}
	for id, bySrc := range lookups {
		idx, ok := idxByID[id]
		if !ok {
			continue
		}
		for src, lr := range bySrc {
			if lr.outcome == "found" && lr.workKey != "" {
				bk := src + "\x00" + lr.workKey
				keyBuckets[bk] = append(keyBuckets[bk], idx)
			}
		}
	}
	now := time.Now()
	for i, b := range books {
		if ctx.Err() != nil {
			break
		}
		// applyTier2 вызывается только из Tier-2-фазы (после Tier-1 sweep все
		// книги уже scanned), поэтому гейта по b.scanned тут нет — кандидатность
		// определяют одиночка-после-Tier-1 + per-source isDue (TTL ниже).
		// Резолвим только одиночек после Tier-1 (экономия внешних вызовов).
		if uf.size(i) > 1 {
			continue
		}
		q := WorkQuery{
			BookID: b.id, Title: b.title, ISBN: b.isbn, Lang: b.lang,
			Authors: []string{fullName(b.lastName, b.firstName)}, LastName: b.lastName, FirstName: b.firstName,
		}
		// src-название (оригинал) даёт лучший хит для переводов.
		// srcTitleNorm нормализован — для запроса нужен исходный src_title; его нет
		// в groupBook, поэтому используем normTitle как fallback (для оригиналов это
		// и есть название). Для переводов с пустым результатом останется одиночкой.
		for _, r := range g.resolvers {
			name := r.Name()
			if !g.isDue(lookups[b.id][name], now) {
				continue
			}
			taskCtx, cancel := context.WithTimeout(ctx, workGroupTaskTimeout)
			if werr := g.gates[name].wait(taskCtx); werr != nil {
				cancel()
				return extByRoot // воркер останавливают
			}
			key, ferr := r.ResolveWorkKey(taskCtx, q)
			cancel()
			if ferr == nil && key != "" {
				g.upsertWorkLookup(ctx, b.id, name, "found", key)
				bk := name + "\x00" + key
				keyBuckets[bk] = append(keyBuckets[bk], i)
				break // нашли — остальные источники не нужны
			}
			if errors.Is(ferr, ErrNotFound) {
				g.upsertWorkLookup(ctx, b.id, name, "not_found", "")
				continue
			}
			if ctx.Err() != nil {
				return extByRoot
			}
			g.logger.Info("work grouping: resolver error", "source", name, "book_id", b.id, "err", ferr)
			g.upsertWorkLookup(ctx, b.id, name, "error", "")
		}
	}
	// Союз по совпавшим внешним work_key + сбор ext_ids на корень кластера.
	for bk, idxs := range keyBuckets {
		src, workKey := splitKey(bk)
		for j := 1; j < len(idxs); j++ {
			uf.union(idxs[0], idxs[j])
		}
		root := uf.find(idxs[0])
		if extByRoot[root] == nil {
			extByRoot[root] = map[string]string{}
		}
		extByRoot[root][extFieldFor(src)] = workKey
	}
	return extByRoot
}

// apply — транзакционно применяет кластеры: переназначает work_id на каноническую
// работу, чистит опустевшие works, пересчитывает edition_count/written_year/
// series, мержит ext_ids, помечает кандидатов work_scanned_at.
func (g *WorkGrouper) apply(ctx context.Context, authorID int64, books []groupBook, uf *unionFind, extByRoot map[int]map[string]string) error {
	// Сгруппировать индексы по корню кластера.
	clusters := map[int][]int{}
	for i := range books {
		r := uf.find(i)
		clusters[r] = append(clusters[r], i)
	}

	tx, err := g.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	affected := map[int64]struct{}{} // канонические + потерявшие книги work_id
	var candidateIDs []int64
	for _, b := range books {
		if !b.scanned {
			candidateIDs = append(candidateIDs, b.id)
		}
	}

	for root, idxs := range clusters {
		canonical := pickCanonicalWork(books, idxs)
		var moved []int64
		for _, i := range idxs {
			if books[i].workID != canonical {
				moved = append(moved, books[i].id)
				affected[books[i].workID] = struct{}{}
			}
		}
		if len(moved) > 0 {
			if _, err := tx.Exec(ctx, `UPDATE books SET work_id = $1 WHERE id = ANY($2)`, canonical, moved); err != nil {
				return fmt.Errorf("reassign work_id: %w", err)
			}
			g.merged.Add(int64(len(moved)))
		}
		if len(idxs) > 1 || len(moved) > 0 {
			affected[canonical] = struct{}{}
		}
		// ext_ids внешних work_key на каноническую работу.
		if ext := extByRoot[root]; len(ext) > 0 {
			raw, _ := json.Marshal(ext)
			if _, err := tx.Exec(ctx, `UPDATE works SET ext_ids = ext_ids || $2::jsonb, updated_at = now() WHERE id = $1`,
				canonical, string(raw)); err != nil {
				return fmt.Errorf("merge ext_ids: %w", err)
			}
		}
	}

	// GC опустевших работ автора (RETURNING — для синка works-индекса).
	gcDeleted, err := scanInt64s(ctx, tx, `
		DELETE FROM works w
		WHERE w.primary_author_id = $1
		  AND NOT EXISTS (SELECT 1 FROM books b WHERE b.work_id = w.id)
		RETURNING w.id
	`, authorID)
	if err != nil {
		return fmt.Errorf("gc works: %w", err)
	}

	if err := recomputeWorkAggregates(ctx, tx, keysOf(affected)); err != nil {
		return err
	}

	if len(candidateIDs) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE books SET work_scanned_at = now() WHERE id = ANY($1)`, candidateIDs); err != nil {
			return fmt.Errorf("mark scanned: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	// Учёт для works-индекса — только после успешного коммита. deleted = GC'нутые,
	// touched = изменённые канонические (affected минус удалённые).
	deletedSet := make(map[int64]struct{}, len(gcDeleted))
	for _, id := range gcDeleted {
		deletedSet[id] = struct{}{}
		g.deletedWorks[id] = struct{}{}
	}
	for id := range affected {
		if _, gone := deletedSet[id]; !gone {
			g.touchedWorks[id] = struct{}{}
		}
	}
	return nil
}

// pickCanonicalWork — каноническая работа кластера: work_id, встречающийся у
// наибольшего числа членов (стабильность — оставляем доминирующую работу);
// тай-брейк — наименьший id.
func pickCanonicalWork(books []groupBook, idxs []int) int64 {
	count := map[int64]int{}
	for _, i := range idxs {
		count[books[i].workID]++
	}
	var best int64
	bestN := -1
	for wid, n := range count {
		if n > bestN || (n == bestN && wid < best) {
			best, bestN = wid, n
		}
	}
	return best
}

// pgExecer — общий Exec для *pgxpool.Pool и pgx.Tx.
type pgExecer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// recomputeWorkAggregates пересчитывает производные поля работ из их изданий:
// edition_count, written_year (самый ранний + источник), series (из
// представительного издания, если у работы серии ещё нет). Используется и
// фоновой группировкой, и ручными split/merge.
func recomputeWorkAggregates(ctx context.Context, ex pgExecer, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	// Все агрегаты пересчитываются АВТОРИТЕТНО из ТЕКУЩИХ живых изданий работы
	// (а не set-if-null). Это важно для split/merge: после выноса издания год и
	// серия должны переderiv'иться по оставшимся, ИНАЧЕ остаётся протухшее
	// значение (баг: работа сохраняла серию вынесенной книги). LEFT JOIN LATERAL
	// → если поддерживающих изданий нет, поле очищается (NULL).
	if _, err := ex.Exec(ctx, `
		UPDATE works w SET edition_count = COALESCE(c.n, 0), updated_at = now()
		FROM (
			SELECT w2.id AS work_id, (
				SELECT count(*) FROM books b WHERE b.work_id = w2.id AND b.deleted = false
			) AS n
			FROM works w2 WHERE w2.id = ANY($1)
		) c
		WHERE w.id = c.work_id
	`, ids); err != nil {
		return fmt.Errorf("recount editions: %w", err)
	}
	if _, err := ex.Exec(ctx, `
		UPDATE works w SET written_year = c.y, written_year_source = c.src
		FROM (
			SELECT w2.id AS work_id, sub.y, sub.src
			FROM works w2
			LEFT JOIN LATERAL (
				SELECT b.written_year::int AS y, b.written_year_source AS src
				FROM books b
				WHERE b.work_id = w2.id AND b.deleted = false AND b.written_year IS NOT NULL
				ORDER BY b.written_year ASC
				LIMIT 1
			) sub ON true
			WHERE w2.id = ANY($1)
		) c
		WHERE w.id = c.work_id
	`, ids); err != nil {
		return fmt.Errorf("recompute written_year: %w", err)
	}
	if _, err := ex.Exec(ctx, `
		UPDATE works w SET series_id = c.series_id, ser_no = c.ser_no
		FROM (
			SELECT w2.id AS work_id, sub.series_id, sub.ser_no
			FROM works w2
			LEFT JOIN LATERAL (
				SELECT b.series_id, b.ser_no
				FROM books b
				WHERE b.work_id = w2.id AND b.deleted = false AND b.series_id IS NOT NULL
				ORDER BY b.ser_no NULLS LAST, b.id
				LIMIT 1
			) sub ON true
			WHERE w2.id = ANY($1)
		) c
		WHERE w.id = c.work_id
	`, ids); err != nil {
		return fmt.Errorf("recompute series: %w", err)
	}
	return nil
}

// SplitEditions — РУЧНОЕ разъединение: выносит указанные издания в НОВУЮ работу
// (починка ложного слияния). Возвращает id новой работы. Помечает их
// work_scanned_at, чтобы фоновая джоба не слила обратно.
func (c *WorkGroupController) SplitEditions(ctx context.Context, bookIDs []int64) (int64, error) {
	if len(bookIDs) == 0 {
		return 0, fmt.Errorf("no book ids")
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	oldIDs, err := scanInt64s(ctx, tx, `SELECT DISTINCT work_id FROM books WHERE id = ANY($1) AND work_id IS NOT NULL`, bookIDs)
	if err != nil {
		return 0, err
	}
	var newID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO works (title, normalized_title, primary_author_id, written_year, written_year_source, series_id, ser_no)
		SELECT b.title, b.normalized_title,
		       (SELECT ba.author_id FROM book_authors ba WHERE ba.book_id = b.id ORDER BY ba.position LIMIT 1),
		       b.written_year, b.written_year_source, b.series_id, b.ser_no
		FROM books b WHERE b.id = $1
		RETURNING id
	`, bookIDs[0]).Scan(&newID); err != nil {
		return 0, fmt.Errorf("create split work: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE books SET work_id = $1, work_scanned_at = now() WHERE id = ANY($2)`, newID, bookIDs); err != nil {
		return 0, fmt.Errorf("reassign split editions: %w", err)
	}
	gcDeleted, err := scanInt64s(ctx, tx, `
		DELETE FROM works w WHERE w.id = ANY($1) AND NOT EXISTS (SELECT 1 FROM books b WHERE b.work_id = w.id)
		RETURNING w.id
	`, oldIDs)
	if err != nil {
		return 0, fmt.Errorf("gc split works: %w", err)
	}
	if err := recomputeWorkAggregates(ctx, tx, append(oldIDs, newID)); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	// Синк поиска: новая работа + выжившие старые upsert, GC'нутые — удалить.
	c.syncSearchAfterManual(append(survivors(oldIDs, gcDeleted), newID), gcDeleted)
	return newID, nil
}

// MergeWorks — РУЧНОЕ объединение нескольких работ в одну (target; по умолчанию
// наименьший id). Переносит все издания в target, чистит опустевшие works.
func (c *WorkGroupController) MergeWorks(ctx context.Context, workIDs []int64, target int64) (int64, error) {
	if len(workIDs) < 2 {
		return 0, fmt.Errorf("need at least two works")
	}
	if target == 0 {
		target = workIDs[0]
		for _, id := range workIDs {
			if id < target {
				target = id
			}
		}
	}
	others := make([]int64, 0, len(workIDs))
	inList := false
	for _, id := range workIDs {
		if id == target {
			inList = true
			continue
		}
		others = append(others, id)
	}
	if !inList {
		return 0, fmt.Errorf("target must be one of work_ids")
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE books SET work_id = $1 WHERE work_id = ANY($2)`, target, others); err != nil {
		return 0, fmt.Errorf("reassign merge editions: %w", err)
	}
	gcDeleted, err := scanInt64s(ctx, tx, `
		DELETE FROM works w WHERE w.id = ANY($1) AND NOT EXISTS (SELECT 1 FROM books b WHERE b.work_id = w.id)
		RETURNING w.id
	`, others)
	if err != nil {
		return 0, fmt.Errorf("gc merged works: %w", err)
	}
	if err := recomputeWorkAggregates(ctx, tx, []int64{target}); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	// Синк поиска: target upsert, поглощённые работы — удалить из works-индекса.
	c.syncSearchAfterManual([]int64{target}, gcDeleted)
	return target, nil
}

// survivors — элементы all, которых нет в removed (для синка works-индекса
// после split: старые работы, пережившие GC).
func survivors(all, removed []int64) []int64 {
	rm := make(map[int64]struct{}, len(removed))
	for _, id := range removed {
		rm[id] = struct{}{}
	}
	var out []int64
	for _, id := range all {
		if _, gone := rm[id]; !gone {
			out = append(out, id)
		}
	}
	return out
}

// syncSearchAfterManual — детачнутый синк поиска после РУЧНЫХ split/merge:
// books-индекс (work_id для distinct/OPDS) + таргетный works-индекс. В фоне,
// чтобы не держать админ-запрос на полном ResyncWorkIDs.
func (c *WorkGroupController) syncSearchAfterManual(touched, deleted []int64) {
	if c.resyncer == nil {
		return
	}
	go func() {
		ctx := context.Background()
		if _, err := c.resyncer.ResyncWorkIDs(ctx); err != nil {
			c.logger.Warn("manual work edit: resync work_id failed", "err", err)
		}
		syncer, ok := c.resyncer.(WorksIndexSyncer)
		if !ok {
			return
		}
		if len(deleted) > 0 {
			if err := syncer.DeleteWorksFromIndex(ctx, deleted); err != nil {
				c.logger.Warn("manual work edit: delete works from index failed", "err", err)
			}
		}
		if len(touched) > 0 {
			if err := syncer.UpsertWorksToIndex(ctx, touched); err != nil {
				c.logger.Warn("manual work edit: upsert works to index failed", "err", err)
			}
		}
	}()
}

func scanInt64s(ctx context.Context, ex interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}, sql string, args ...any) ([]int64, error) {
	rows, err := ex.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ── book_work_lookups: загрузка / isDue / upsert ────────────────

type workLookupRow struct {
	outcome   string
	workKey   string
	checkedAt time.Time
}

func (g *WorkGrouper) loadWorkLookups(ctx context.Context, ids []int64) (map[int64]map[string]workLookupRow, error) {
	out := map[int64]map[string]workLookupRow{}
	rows, err := g.pool.Query(ctx,
		`SELECT book_id, source, outcome, COALESCE(work_key,''), checked_at FROM book_work_lookups WHERE book_id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var src string
		var lr workLookupRow
		if err := rows.Scan(&id, &src, &lr.outcome, &lr.workKey, &lr.checkedAt); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = map[string]workLookupRow{}
		}
		out[id][src] = lr
	}
	return out, rows.Err()
}

func (g *WorkGrouper) isDue(l workLookupRow, now time.Time) bool {
	switch l.outcome {
	case "":
		return true
	case "found":
		return false
	case "not_found":
		return now.Sub(l.checkedAt) >= time.Duration(g.cfg.NotFoundRetryDays)*24*time.Hour
	case "error":
		return now.Sub(l.checkedAt) >= time.Duration(g.cfg.ErrorRetryHours)*time.Hour
	default:
		return true
	}
}

func (g *WorkGrouper) upsertWorkLookup(ctx context.Context, bookID int64, source, outcome, workKey string) {
	var kptr *string
	if workKey != "" {
		kptr = &workKey
	}
	if _, err := g.pool.Exec(ctx, `
		INSERT INTO book_work_lookups (book_id, source, outcome, work_key, checked_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (book_id, source)
		DO UPDATE SET outcome = EXCLUDED.outcome, work_key = EXCLUDED.work_key, checked_at = now()
	`, bookID, source, outcome, kptr); err != nil {
		g.logger.Warn("work grouping: upsert work lookup failed", "book_id", bookID, "source", source, "err", err)
	}
}

// ── helpers ─────────────────────────────────────────────────────

func fullName(last, first string) string {
	if first == "" {
		return last
	}
	return last + " " + first
}

func extFieldFor(source string) string {
	switch source {
	case "openlibrary":
		return "ol_work"
	case "wikidata":
		return "wd_qid"
	default:
		return source
	}
}

func splitKey(bk string) (source, workKey string) {
	for i := 0; i < len(bk); i++ {
		if bk[i] == 0 {
			return bk[:i], bk[i+1:]
		}
	}
	return bk, ""
}

func keysOf(m map[int64]struct{}) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ── union-find ──────────────────────────────────────────────────

type unionFind struct {
	parent []int
	rank   []int
	sz     []int
}

func newUnionFind(n int) *unionFind {
	uf := &unionFind{parent: make([]int, n), rank: make([]int, n), sz: make([]int, n)}
	for i := range uf.parent {
		uf.parent[i] = i
		uf.sz[i] = 1
	}
	return uf
}

func (u *unionFind) find(x int) int {
	for u.parent[x] != x {
		u.parent[x] = u.parent[u.parent[x]] // path halving
		x = u.parent[x]
	}
	return x
}

func (u *unionFind) union(a, b int) {
	ra, rb := u.find(a), u.find(b)
	if ra == rb {
		return
	}
	if u.rank[ra] < u.rank[rb] {
		ra, rb = rb, ra
	}
	u.parent[rb] = ra
	u.sz[ra] += u.sz[rb]
	if u.rank[ra] == u.rank[rb] {
		u.rank[ra]++
	}
}

func (u *unionFind) size(x int) int { return u.sz[u.find(x)] }

// ── Controller: рантайм-управление воркером (зеркало YearBackfillController) ──

// WorkGroupStatus — состояние воркера для админ-UI.
type WorkGroupStatus struct {
	Running bool   `json:"work_grouping_running"`
	Mode    string `json:"work_grouping_mode"` // "off" | "continuous" | "once"
}

// WorkGroupCoverage — покрытие группировки для админ-статистики.
type WorkGroupCoverage struct {
	Books             int `json:"books"`               // живых изданий
	Works             int `json:"works"`               // логических книг
	MultiEditionWorks int `json:"multi_edition_works"` // книг с >1 изданием
	Scanned           int `json:"scanned"`             // изданий, прошедших группировку
}

type WorkGroupController struct {
	pool     *pgxpool.Pool
	ol       WorkKeyResolver
	wd       WorkKeyResolver
	resyncer WorkIDResyncer
	logger   *slog.Logger

	mu         sync.Mutex
	cfg        WorkGroupConfig
	contCancel context.CancelFunc
	onceCancel context.CancelFunc
}

func NewWorkGroupController(pool *pgxpool.Pool, ol, wd WorkKeyResolver, cfg WorkGroupConfig, resyncer WorkIDResyncer, logger *slog.Logger) *WorkGroupController {
	if logger == nil {
		logger = slog.Default()
	}
	return &WorkGroupController{pool: pool, ol: ol, wd: wd, resyncer: resyncer, cfg: cfg, logger: logger}
}

func (c *WorkGroupController) ready() bool { return c.pool != nil }

func (c *WorkGroupController) Status() WorkGroupStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case c.onceCancel != nil:
		return WorkGroupStatus{Running: true, Mode: "once"}
	case c.contCancel != nil:
		return WorkGroupStatus{Running: true, Mode: "continuous"}
	default:
		return WorkGroupStatus{Running: false, Mode: "off"}
	}
}

func (c *WorkGroupController) Start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.contCancel != nil || !c.ready() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.contCancel = cancel
	g := NewWorkGrouper(c.pool, c.ol, c.wd, c.cfg, c.resyncer, c.logger)
	go g.Run(ctx)
	c.logger.Info("work grouping: continuous job started")
}

func (c *WorkGroupController) Stop() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.contCancel == nil {
		return
	}
	c.contCancel()
	c.contCancel = nil
	c.logger.Info("work grouping: continuous job stopped")
}

// SetEnabled — мастер-тумблер «фоновый воркер вкл/выкл».
func (c *WorkGroupController) SetEnabled(on bool) {
	if on {
		c.Start()
	} else {
		c.Stop()
	}
}

// SetConfig применяет новые параметры; перезапускает непрерывный воркер.
func (c *WorkGroupController) SetConfig(cfg WorkGroupConfig) {
	c.mu.Lock()
	c.cfg = cfg
	running := c.contCancel != nil
	c.mu.Unlock()
	if running {
		c.Stop()
		c.Start()
	}
}

// RunOnce — один проход группировки (кнопка «Запустить сейчас»).
func (c *WorkGroupController) RunOnce() {
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
		g := NewWorkGrouper(c.pool, c.ol, c.wd, cfg, c.resyncer, c.logger)
		n := g.drainAll(ctx)
		cancel()
		c.mu.Lock()
		c.onceCancel = nil
		c.mu.Unlock()
		c.logger.Info("work grouping: one-shot pass done", "authors", n, "editions_merged", g.merged.Load())
	}()
}

// StopOnce — отменить идущий разовый проход.
func (c *WorkGroupController) StopOnce() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.onceCancel == nil {
		return
	}
	c.onceCancel()
	c.logger.Info("work grouping: one-shot pass stop requested")
}

// Coverage — покрытие группировки.
func (c *WorkGroupController) Coverage(ctx context.Context) (WorkGroupCoverage, error) {
	var out WorkGroupCoverage
	if c.pool == nil {
		return out, nil
	}
	// Считаем по ЖИВЫМ изданиям (deleted=false): работы удалённых изданий нигде
	// не показываются и группировкой не трогаются, поэтому в покрытие не входят
	// (иначе works > editions из-за singleton-работ удалённых книг). COALESCE(-id)
	// — defensive на случай (невозможного по инварианту) NULL work_id.
	if err := c.pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM books WHERE deleted = false),
			(SELECT count(DISTINCT COALESCE(work_id, -id)) FROM books WHERE deleted = false),
			(SELECT count(*) FROM (
				SELECT 1 FROM books WHERE deleted = false
				GROUP BY COALESCE(work_id, -id) HAVING count(*) > 1
			) t),
			(SELECT count(*) FROM books WHERE deleted = false AND work_scanned_at IS NOT NULL)
	`).Scan(&out.Books, &out.Works, &out.MultiEditionWorks, &out.Scanned); err != nil {
		return out, err
	}
	return out, nil
}
