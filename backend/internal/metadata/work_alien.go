package metadata

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// SplitAlienEditions выносит в отдельные работы издания, у которых нет ни одного
// общего автора с якорным изданием своей работы (#285): «Суровые времена» Глена
// Кука внутри «Очищения» Кессела, «The Leviathan» Рота рядом с «Любовью в
// кредит» Д'Алессандро. Такие склейки остаются от ошибок группировки и от
// импорта, который переписывает авторов издания, но работу не пересматривает
// (прод 2026-09: 174 издания в ~160 работах).
//
// Лицо работы — якорь (название издания = название работы; оценки и промпты
// пользователей висят на работе, а видят её по якорю), поэтому уходят остальные.
// Не трогаются: издания с признаком той же книги (одинаковое название с якорем
// или совпадение src_title — перевод, склеенный вручную через разные записи
// автора) и работы с ручной правкой авторов. Вынесенным сбрасывается
// work_scanned_at: группировка сможет найти им правильную работу.
//
// Возвращает работы, которые надо досинкать в поиске (старые и новые).
func SplitAlienEditions(ctx context.Context, pool *pgxpool.Pool) ([]int64, error) {
	rows, err := pool.Query(ctx, `
		WITH multi AS (
			SELECT work_id FROM books WHERE deleted = false AND work_id IS NOT NULL
			GROUP BY work_id HAVING count(*) >= 2
		), anchor AS (
			SELECT DISTINCT ON (b.work_id) b.work_id, b.id AS aid,
			       translate(b.normalized_title::text, 'ё', 'е') AS aft,
			       translate(lower(btrim(COALESCE(b.src_title, ''))), 'ё', 'е') AS asrc
			FROM books b JOIN works w ON w.id = b.work_id
			WHERE b.deleted = false AND b.work_id IN (SELECT work_id FROM multi)
			ORDER BY b.work_id, (b.normalized_title = w.normalized_title) DESC, b.id
		)
		SELECT b.id, b.work_id, a.aid
		FROM books b JOIN anchor a ON a.work_id = b.work_id
		WHERE b.deleted = false AND b.id <> a.aid
		  AND NOT EXISTS (SELECT 1 FROM book_authors x
		                  JOIN book_authors y ON y.author_id = x.author_id AND y.book_id = a.aid
		                  WHERE x.book_id = b.id)
		  AND translate(b.normalized_title::text, 'ё', 'е') <> a.aft
		  AND NOT (a.asrc <> '' AND a.asrc = translate(b.normalized_title::text, 'ё', 'е'))
		  AND NOT (COALESCE(b.src_title, '') <> ''
		           AND translate(lower(btrim(b.src_title)), 'ё', 'е') IN (a.aft, a.asrc))
		  AND NOT EXISTS (SELECT 1 FROM metadata_overrides o
		                  WHERE o.target_kind = 'work' AND o.target_id = b.work_id AND o.field = 'authors')
		ORDER BY b.work_id, b.id`)
	if err != nil {
		return nil, fmt.Errorf("find alien editions: %w", err)
	}
	type cand struct{ book, work, anchor int64 }
	cands, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (cand, error) {
		var c cand
		err := r.Scan(&c.book, &c.work, &c.anchor)
		return c, err
	})
	if err != nil {
		return nil, fmt.Errorf("find alien editions: %w", err)
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
		newID, err := splitAlienEdition(ctx, pool, dom, c.book, c.work, c.anchor)
		if err != nil {
			return keysOf(touched), fmt.Errorf("split edition %d from work %d: %w", c.book, c.work, err)
		}
		touched[c.work] = struct{}{}
		touched[newID] = struct{}{}
	}
	return keysOf(touched), nil
}

func splitAlienEdition(ctx context.Context, pool *pgxpool.Pool, dom string, bookID, workID, anchorID int64) (int64, error) {
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
		RETURNING id`, bookID).Scan(&newID); err != nil {
		return 0, fmt.Errorf("create work: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE books SET work_id = $1, work_scanned_at = NULL WHERE id = $2`, newID, bookID); err != nil {
		return 0, fmt.Errorf("move edition: %w", err)
	}
	// Основной автор прежней работы — из якоря, если прежний ушёл с изданием.
	if _, err := tx.Exec(ctx, `
		UPDATE works w
		SET primary_author_id = (SELECT ba.author_id FROM book_authors ba WHERE ba.book_id = $2 ORDER BY ba.position LIMIT 1)
		WHERE w.id = $1
		  AND NOT EXISTS (SELECT 1 FROM books b JOIN book_authors ba ON ba.book_id = b.id
		                  WHERE b.work_id = w.id AND b.deleted = false AND ba.author_id = w.primary_author_id)
		  AND NOT EXISTS (SELECT 1 FROM metadata_overrides o
		                  WHERE o.target_kind = 'work' AND o.target_id = w.id AND o.field = 'authors')`,
		workID, anchorID); err != nil {
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
