package metadata

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Фаза «Фантлаб по автору» воркера «Год написания» (#533): один запрос
// библиографии на автора (FantlabProvider.AuthorYears) вместо поиска на каждую
// книгу — у Wikidata тот дал 7 % из 258 тыс. изданий. Идёт ПЕРЕД книжной
// фазой Wikidata: на запрос год получают десятки работ, а не одна. Найденный год
// — внешний год работы (works.external_year, как у воркера известности); год
// работы пересчитывается по правилам work_years.go (самый ранний правдоподобный,
// ручная правка неприкосновенна). Учёт — author_fantlab_lookups (миграция 0062):
// кандидаты, которых пора спросить, — в SQL (грабля 21), ядро — по известности.

// FantlabYearSource — то, что фазе нужно от Фантлаба (подменяется в тестах).
type FantlabYearSource interface {
	AuthorYears(ctx context.Context, a FantlabAuthorQuery, works []FantlabWorkQuery, wait func(context.Context) error) (FantlabAuthorYears, error)
}

// fantlabIDResolver — FantLab ID авторов по QID (Wikidata P7433); реализует
// WikidataAdaptationsProvider.
type fantlabIDResolver interface {
	FantlabAuthorIDs(ctx context.Context, qids []string) (map[string]int64, error)
}

const (
	fantlabAuthorBatch    = 25
	fantlabFoundRetryDays = 180 // найденного автора перепроверять — у него выходят книги, Фантлаб дополняется
	// fantlabOmnibusSizeRatio — том по циклу должен быть во столько раз больше
	// медианы обычных работ автора: у сетевых серий первый роман назван как цикл
	// («Империя Оствер» — 1,25 медианы), у томов — 3–30 («Хроники Амбера» — 33).
	// Прогон на проде 2026-10-17: из 307 совпадений с циклом проходят 183.
	fantlabOmnibusSizeRatio = 2.5
)

type fantlabAuthor struct {
	id          int64
	last, first string
	qid         string
}

// drainFantlabAuthors — проход по авторам, у которых есть работы без года.
// Возвращает число обработанных авторов; работы с новым внешним годом — в
// b.changedWorks.
func (b *YearBackfiller) drainFantlabAuthors(ctx context.Context) int {
	if b.fl == nil || !b.cfg.Fantlab {
		return 0
	}
	total := 0
	seen := map[int64]bool{}
	for ctx.Err() == nil {
		batch, err := b.fetchFantlabAuthors(ctx)
		if err != nil {
			b.logger.Warn("year backfill: fetch fantlab authors failed", "err", err)
			return total
		}
		fresh := batch[:0]
		for _, a := range batch {
			if !seen[a.id] {
				seen[a.id] = true
				fresh = append(fresh, a)
			}
		}
		if len(fresh) == 0 {
			return total // пусто или те же (учёт не записался) — не крутимся
		}
		ids := b.fantlabIDs(ctx, fresh)
		for _, a := range fresh {
			if ctx.Err() != nil {
				return total
			}
			b.processFantlabAuthor(ctx, a, ids[a.qid])
			total++
		}
	}
	return total
}

// fetchFantlabAuthors — авторы (не служебные) с живыми работами без года
// написания, которых пора спросить: не спрашивали, not_found/error/found
// старше своего TTL. Ядро — по известности.
func (b *YearBackfiller) fetchFantlabAuthors(ctx context.Context) ([]fantlabAuthor, error) {
	ttl := b.ttl()
	rows, err := b.pool.Query(ctx, `
		SELECT a.id, a.last_name, COALESCE(a.first_name, ''), COALESCE(a.ext_ids->>'wd_qid', '')
		FROM authors a
		LEFT JOIN author_fantlab_lookups l ON l.author_id = a.id
		WHERE NOT a.is_service
		  AND (l.author_id IS NULL
		       OR (l.outcome = 'not_found' AND l.checked_at < now() - $1::interval)
		       OR (l.outcome = 'error' AND l.checked_at < now() - $2::interval)
		       OR (l.outcome = 'found' AND l.checked_at < now() - $3::interval))
		  AND EXISTS (SELECT 1 FROM works w
		              WHERE w.primary_author_id = a.id AND COALESCE(w.kind, '') = '' AND w.written_year IS NULL
		                AND EXISTS (SELECT 1 FROM books bk WHERE bk.work_id = w.id AND NOT bk.deleted))
		ORDER BY a.renown DESC, a.id
		LIMIT $4`,
		pgInterval(ttl.notFound), pgInterval(ttl.error), pgInterval(fantlabFoundRetryDays*24*time.Hour), fantlabAuthorBatch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []fantlabAuthor
	for rows.Next() {
		var a fantlabAuthor
		if err := rows.Scan(&a.id, &a.last, &a.first, &a.qid); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// fantlabIDs — FantLab ID пачки авторов по Wikidata P7433 (одним запросом).
// Сбой Wikidata — не повод стоять: авторы пойдут через поиск по имени.
func (b *YearBackfiller) fantlabIDs(ctx context.Context, batch []fantlabAuthor) map[string]int64 {
	r, ok := b.wd.(fantlabIDResolver)
	if !ok || b.wd == nil {
		return nil
	}
	var qids []string
	for _, a := range batch {
		if a.qid != "" {
			qids = append(qids, a.qid)
		}
	}
	if len(qids) == 0 {
		return nil
	}
	if err := b.wdGate.wait(ctx); err != nil {
		return nil
	}
	ids, err := r.FantlabAuthorIDs(ctx, qids)
	if err != nil {
		if !errors.Is(err, ErrSourcePaused) {
			b.logger.Info("year backfill: wikidata fantlab ids failed", "err", err)
		}
		return nil
	}
	return ids
}

// processFantlabAuthor — библиография автора → внешний год его работ.
func (b *YearBackfiller) processFantlabAuthor(ctx context.Context, a fantlabAuthor, fantlabID int64) {
	works, err := b.fantlabWorks(ctx, a.id)
	if err != nil {
		b.logger.Warn("year backfill: load works for fantlab failed", "author_id", a.id, "err", err)
		return
	}
	tctx, cancel := context.WithTimeout(ctx, 3*yearBackfillTaskTimeout)
	defer cancel()
	res, err := b.fl.AuthorYears(tctx, FantlabAuthorQuery{LastName: a.last, FirstName: a.first, FantlabID: fantlabID}, works, b.flGate.wait)
	b.lookedUp.Add(1)
	switch {
	case errors.Is(err, ErrSourcePaused), ctx.Err() != nil:
		return // пауза источника (#299) или остановка — без учёта, спросим позже
	case errors.Is(err, ErrNotFound):
		b.storeFantlabLookup(ctx, a.id, "not_found", FantlabAuthorYears{}, 0)
		return
	case err != nil:
		b.logger.Info("year backfill: fantlab author failed", "author_id", a.id, "err", err)
		b.storeFantlabLookup(ctx, a.id, "error", FantlabAuthorYears{}, 0)
		return
	}
	dated := 0
	for _, m := range res.Matched {
		if m.Year <= 0 {
			continue
		}
		tag, err := b.pool.Exec(ctx, `
			UPDATE works SET external_year = $2, external_year_source = 'fantlab',
			       ext_ids = CASE WHEN $3::bigint > 0 AND NOT ext_ids ? 'fl_id' THEN ext_ids || jsonb_build_object('fl_id', $3::bigint) ELSE ext_ids END,
			       updated_at = now()
			WHERE id = $1 AND (external_year IS NULL OR external_year > $2)`, m.WorkID, m.Year, m.FlWorkID)
		if err != nil {
			b.logger.Warn("year backfill: write fantlab year failed", "work_id", m.WorkID, "err", err)
			continue
		}
		if tag.RowsAffected() > 0 {
			dated++
			b.changedMu.Lock()
			b.changedWorks = append(b.changedWorks, m.WorkID)
			b.changedMu.Unlock()
		}
	}
	b.yearChanged.Add(int64(dated))
	b.writeFantlabKinds(ctx, a.id, res.Kinds)
	b.recordFantlabWorkKeys(ctx, a.id, res.Matched)
	b.storeFantlabLookup(ctx, a.id, "found", res, dated)
}

// writeFantlabKinds — тип тома-сборника по библиографии (#532): название работы
// у Фантлаба — только цикл/сборник/антология автора. Кандидаты — работы без
// типа (fantlabWorks); решение «это роман» воркера «Известность» (kind NULL,
// kind_source fantlab) и ручную правку не трогаем. Изменённые работы — в
// works-индекс (kind — его поле) вместе с годами.
func (b *YearBackfiller) writeFantlabKinds(ctx context.Context, authorID int64, kinds []FantlabKindMatch) {
	median := -1.0 // медиана размера обычных работ автора; считается при первом томе по циклу
	for _, k := range kinds {
		minSize := 0.0
		if k.Kind == "omnibus" {
			if median < 0 {
				median = b.authorMedianWorkSize(ctx, authorID)
			}
			if median <= 0 {
				continue // размеров нет — не отличить том от романа с названием цикла
			}
			minSize = fantlabOmnibusSizeRatio * median
		}
		tag, err := b.pool.Exec(ctx, `
			UPDATE works w SET kind = $2, kind_source = 'fantlab', updated_at = now()
			WHERE w.id = $1 AND w.kind IS DISTINCT FROM $2 AND (w.kind_source IS NULL OR w.kind_source = 'heuristic')
			  AND COALESCE((SELECT max(b.size_bytes) FROM books b WHERE b.work_id = w.id AND NOT b.deleted), 0) >= $3::float8`,
			k.WorkID, k.Kind, minSize)
		if err != nil {
			b.logger.Warn("year backfill: write fantlab kind failed", "work_id", k.WorkID, "err", err)
			continue
		}
		if tag.RowsAffected() > 0 {
			b.kindChanged.Add(1)
			b.changedMu.Lock()
			b.changedWorks = append(b.changedWorks, k.WorkID)
			b.changedMu.Unlock()
		}
	}
}

// fantlabWorks — работы автора для сопоставления (основной автор, не сборники,
// с живыми изданиями) — и датированные тоже: год Фантлаба может оказаться
// раньше fb2-года переиздания («Поиски Аверроэса» Борхеса: fb2 1949, верно 1947).
func (b *YearBackfiller) fantlabWorks(ctx context.Context, authorID int64) ([]FantlabWorkQuery, error) {
	rows, err := b.pool.Query(ctx, `
		SELECT w.id, w.title,
		       COALESCE(ARRAY(SELECT DISTINCT bk.src_title FROM books bk
		                      WHERE bk.work_id = w.id AND NOT bk.deleted AND bk.src_title IS NOT NULL), '{}')
		FROM works w
		WHERE w.primary_author_id = $1 AND COALESCE(w.kind, '') = ''
		  AND EXISTS (SELECT 1 FROM books bk WHERE bk.work_id = w.id AND NOT bk.deleted)`, authorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FantlabWorkQuery
	for rows.Next() {
		var q FantlabWorkQuery
		if err := rows.Scan(&q.ID, &q.Title, &q.SrcTitles); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// recordFantlabWorkKeys — ключ группировки «произведение Фантлаба» (#532) для
// изданий работ автора, сошедшихся по ПОЛНОМУ названию к одному произведению
// библиографии: «Гарри Поттер и философский камень» и «…и Волшебный камень»
// (перевод Спивак) — разные названия одного произведения. Склеивает их
// группировка (Tier-1 по найденным ключам, гейт fantlabBucketConflicts):
// новый или изменённый ключ снимает work_scanned_at, как RecordBookWorkKey.
// Одиночкам ключ не пишем — склеивать не с чем.
func (b *YearBackfiller) recordFantlabWorkKeys(ctx context.Context, authorID int64, matched []FantlabYearMatch) {
	byFl := map[int64][]int64{}
	for _, m := range matched {
		if m.Exact && m.FlWorkID > 0 {
			byFl[m.FlWorkID] = append(byFl[m.FlWorkID], m.WorkID)
		}
	}
	var works []int64
	var keys []string
	for fl, ws := range byFl {
		if len(ws) < 2 {
			continue
		}
		for _, w := range ws {
			works = append(works, w)
			keys = append(keys, strconv.FormatInt(fl, 10))
		}
	}
	if len(works) == 0 {
		return
	}
	tag, err := b.pool.Exec(ctx, `
		WITH k AS (SELECT * FROM unnest($1::bigint[], $2::text[]) AS t(work_id, key)),
		up AS (
			INSERT INTO book_work_lookups (book_id, source, outcome, work_key, checked_at)
			SELECT bk.id, $3, 'found', k.key, now()
			FROM k JOIN books bk ON bk.work_id = k.work_id AND NOT bk.deleted
			ON CONFLICT (book_id, source) DO UPDATE SET outcome = 'found', work_key = EXCLUDED.work_key, checked_at = now()
			WHERE book_work_lookups.outcome <> 'found' OR book_work_lookups.work_key IS DISTINCT FROM EXCLUDED.work_key
			RETURNING book_id
		)
		UPDATE books SET work_scanned_at = NULL WHERE id IN (SELECT book_id FROM up)`, works, keys, fantlabWorkKeySource)
	if err != nil {
		b.logger.Warn("year backfill: record fantlab work keys failed", "author_id", authorID, "err", err)
		return
	}
	if n := tag.RowsAffected(); n > 0 {
		b.logger.Info("year backfill: fantlab work keys recorded", "author_id", authorID, "editions", n)
	}
}

// authorMedianWorkSize — медиана размера (крупнейшее издание) обычных работ
// автора, байт; 0 — не известна.
func (b *YearBackfiller) authorMedianWorkSize(ctx context.Context, authorID int64) float64 {
	var m *float64
	if err := b.pool.QueryRow(ctx, `
		SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY sz)
		FROM (SELECT max(bk.size_bytes) AS sz
		      FROM works w JOIN books bk ON bk.work_id = w.id AND NOT bk.deleted
		      WHERE w.primary_author_id = $1 AND COALESCE(w.kind, '') = ''
		      GROUP BY w.id) s
		WHERE sz > 0`, authorID).Scan(&m); err != nil {
		b.logger.Warn("year backfill: author median size failed", "author_id", authorID, "err", err)
		return 0
	}
	if m == nil {
		return 0
	}
	return *m
}

func (b *YearBackfiller) storeFantlabLookup(ctx context.Context, authorID int64, outcome string, res FantlabAuthorYears, dated int) {
	var via *string
	if res.Via != "" {
		via = &res.Via
	}
	if _, err := b.pool.Exec(ctx, `
		INSERT INTO author_fantlab_lookups (author_id, outcome, fantlab_id, via, matched, dated, checked_at)
		VALUES ($1, $2, NULLIF($3::bigint, 0), $4, $5, $6, now())
		ON CONFLICT (author_id) DO UPDATE SET outcome = EXCLUDED.outcome, fantlab_id = EXCLUDED.fantlab_id,
		       via = EXCLUDED.via, matched = EXCLUDED.matched, dated = EXCLUDED.dated, checked_at = now()`,
		authorID, outcome, res.FantlabID, via, len(res.Matched), dated); err != nil {
		b.logger.Warn("year backfill: store fantlab lookup failed", "author_id", authorID, "err", err)
	}
}

// pgInterval — длительность строкой интервала Postgres («3600 seconds»).
func pgInterval(d time.Duration) string {
	return fmt.Sprintf("%d seconds", int64(d.Seconds()))
}
