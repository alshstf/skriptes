package metadata

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SplitAnthologyEditions выносит из работ издания-антологии с тем же названием
// (#464): «Патруль времени» Андерсона и одноимённая антология 17 авторов,
// «Дракула» Стокера и том «Антологии ужасов». Признак — тот же, что у гейта
// группировки (unionGated): у работы самое «малоавторское» издание с
// 1–maxCoreAuthors авторами, а у антологии авторов на anthologyExtraAuthors больше
// (прод 2026-10: 143 издания в 137 работах). Работы, которые сами многоавторские,
// не трогаются; работы с ручной правкой авторов — тоже.
//
// Копии одной антологии (тот же состав авторов) уходят вместе в одну новую
// работу. Якорь не защищается, в отличие от SplitAlienEditions: название у
// антологии то же, что у работы, поэтому якорем нередко оказывается она сама, а
// лицо работы — роман, он остаётся. Оценки и промпты висят на работе и остаются
// у романа. Вынесенным сбрасывается work_scanned_at: группировка с гейтом снова
// их не приклеит, а копии антологии соберёт.
//
// Возвращает работы, которые надо досинкать в поиске (старые и новые).
func SplitAnthologyEditions(ctx context.Context, pool *pgxpool.Pool) ([]int64, error) {
	rows, err := pool.Query(ctx, `
		WITH ed AS (
			SELECT b.id, b.work_id,
			       (SELECT count(*) FROM book_authors x WHERE x.book_id = b.id) AS na,
			       COALESCE((SELECT string_agg(x.author_id::text, ',' ORDER BY x.author_id)
			                 FROM book_authors x WHERE x.book_id = b.id), '') AS aset
			FROM books b
			WHERE b.deleted = false AND b.work_id IN (
				SELECT work_id FROM books WHERE deleted = false AND work_id IS NOT NULL
				GROUP BY work_id HAVING count(*) >= 2)
		), wm AS (
			SELECT work_id, min(na) AS mn FROM ed GROUP BY work_id
		)
		SELECT ed.work_id, ed.aset, array_agg(ed.id ORDER BY ed.id)
		FROM ed JOIN wm USING (work_id)
		WHERE wm.mn <= $1 AND ed.na - wm.mn >= $2
		  AND NOT EXISTS (SELECT 1 FROM metadata_overrides o
		                  WHERE o.target_kind = 'work' AND o.target_id = ed.work_id AND o.field = 'authors')
		GROUP BY ed.work_id, ed.aset
		ORDER BY ed.work_id, ed.aset`, maxCoreAuthors, anthologyExtraAuthors)
	if err != nil {
		return nil, fmt.Errorf("find anthology editions: %w", err)
	}
	type cand struct {
		work  int64
		books []int64
	}
	cands, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (cand, error) {
		var c cand
		var aset string
		err := r.Scan(&c.work, &aset, &c.books)
		return c, err
	})
	if err != nil {
		return nil, fmt.Errorf("find anthology editions: %w", err)
	}
	if len(cands) == 0 {
		return nil, nil
	}
	dom, err := dominantLang(ctx, pool)
	if err != nil {
		return nil, err
	}
	touched := map[int64]struct{}{}
	for _, c := range cands {
		newID, err := splitAnthology(ctx, pool, dom, c.books, c.work)
		if err != nil {
			return keysOf(touched), fmt.Errorf("split anthology %v from work %d: %w", c.books, c.work, err)
		}
		touched[c.work] = struct{}{}
		touched[newID] = struct{}{}
	}
	return keysOf(touched), nil
}

func splitAnthology(ctx context.Context, pool *pgxpool.Pool, dom string, bookIDs []int64, workID int64) (int64, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var newID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO works (title, normalized_title, primary_author_id, written_year, written_year_source, series_id, ser_no)
		SELECT b.title, b.normalized_title,
		       (SELECT ba.author_id FROM book_authors ba WHERE ba.book_id = b.id ORDER BY ba.position LIMIT 1),
		       b.written_year, b.written_year_source, b.series_id, b.ser_no
		FROM books b WHERE b.id = $1
		RETURNING id`, bookIDs[0]).Scan(&newID); err != nil {
		return 0, fmt.Errorf("create work: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE books SET work_id = $1, work_scanned_at = NULL WHERE id = ANY($2)`, newID, bookIDs); err != nil {
		return 0, fmt.Errorf("move editions: %w", err)
	}
	// Основной автор прежней работы — первый автор оставшихся изданий, если
	// прежний ушёл вместе с антологией.
	if _, err := tx.Exec(ctx, `
		UPDATE works w
		SET primary_author_id = (SELECT ba.author_id FROM books b JOIN book_authors ba ON ba.book_id = b.id
		                         WHERE b.work_id = w.id AND b.deleted = false
		                         ORDER BY ba.position, b.id LIMIT 1)
		WHERE w.id = $1
		  AND NOT EXISTS (SELECT 1 FROM books b JOIN book_authors ba ON ba.book_id = b.id
		                  WHERE b.work_id = w.id AND b.deleted = false AND ba.author_id = w.primary_author_id)`,
		workID); err != nil {
		return 0, fmt.Errorf("fix primary author: %w", err)
	}
	ids := []int64{workID, newID}
	if err := recomputeWorkAggregates(ctx, tx, ids); err != nil {
		return 0, err
	}
	if dom != "" {
		if _, err := recomputeWorkTitles(ctx, tx, dom, ids); err != nil {
			return 0, fmt.Errorf("work titles: %w", err)
		}
	}
	return newID, tx.Commit(ctx)
}
