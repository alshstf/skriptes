package metadata

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Год работы (#288). fb2 <title-info><date> на проде — единственный источник,
// и он врёт: «Хаджи-Мурат» 2020, «Белые ночи» 1986; у 7,4 тыс. книг год написания
// позже года издания. Сверка выборки с Фантлабом (2026-10-04, 30 переводов и 31
// оригинал): fb2-год совпадает в 83–87 %, ошибается почти только позже настоящего
// (переиздание/перевод) — поэтому «у переводов fb2 не брать» отвергнуто (выбросило
// бы верные годы), а принято «самый ранний правдоподобный год»:
//
//   - fb2-год издания правдоподобен, если он в [1000, текущий]; год позже года
//     этого издания заменяется годом издания (верхняя граница: это чаще дата
//     электронной книги, чем другой век; plausibleFb2Year при извлечении,
//     CleanImplausibleBookYears — для накопленного);
//   - год работы = самый ранний из правдоподобных fb2-годов её изданий и внешнего
//     года (works.external_year: Фантлаб — год первой публикации); если самый
//     ранний год издания работы ещё раньше, он и есть потолок («год написания /
//     первого издания»);
//   - ручная правка года (metadata_overrides) неприкосновенна.

const minPlausibleYear = 1000

// plausibleYear — год в [1000, текущий], иначе 0.
func plausibleYear(y int) int {
	if y < minPlausibleYear || y > time.Now().Year() {
		return 0
	}
	return y
}

// plausibleFb2Year — fb2-год написания издания: вне [1000, текущий] — 0; позже
// года издания (edition 0 — неизвестен) — год издания, верхняя граница.
func plausibleFb2Year(written, edition int) int {
	written = plausibleYear(written)
	if written > 0 && plausibleYear(edition) > 0 && written > edition {
		return edition
	}
	return written
}

// recomputeWorkYears — works.written_year/source по правилу файла для работ ids
// (allWorks — для всех). Возвращает работы, у которых год изменился.
func recomputeWorkYears(ctx context.Context, ex pgxExec, ids []int64, allWorks bool) ([]int64, error) {
	rows, err := ex.Query(ctx, `
		WITH ed AS (
		    SELECT b.work_id,
		           min(b.written_year) FILTER (WHERE b.written_year BETWEEN $3 AND $4) AS wy,
		           (array_agg(b.written_year_source ORDER BY b.written_year)
		              FILTER (WHERE b.written_year BETWEEN $3 AND $4))[1] AS wsrc,
		           min(b.edition_year) FILTER (WHERE b.edition_year BETWEEN $3 AND $4) AS ey
		    FROM books b
		    WHERE b.deleted = false AND b.work_id IS NOT NULL AND ($2 OR b.work_id = ANY($1))
		    GROUP BY b.work_id
		), calc AS (
		    SELECT w.id,
		           -- fb2-год с потолком по самому раннему изданию.
		           CASE WHEN ed.wy IS NOT NULL AND ed.ey IS NOT NULL AND ed.ey < ed.wy THEN ed.ey ELSE ed.wy END AS by,
		           CASE WHEN ed.wy IS NOT NULL AND ed.ey IS NOT NULL AND ed.ey < ed.wy THEN 'edition_year' ELSE ed.wsrc END AS bsrc,
		           w.external_year AS xy, w.external_year_source AS xsrc
		    FROM works w LEFT JOIN ed ON ed.work_id = w.id
		    WHERE ($2 OR w.id = ANY($1))
		      AND NOT EXISTS (SELECT 1 FROM metadata_overrides o
		                      WHERE o.target_kind = 'work' AND o.target_id = w.id AND o.field = 'written_year')
		), fin AS (
		    SELECT id,
		           CASE WHEN xy IS NOT NULL AND (by IS NULL OR xy < by) THEN xy ELSE by END AS y,
		           CASE WHEN xy IS NOT NULL AND (by IS NULL OR xy < by) THEN xsrc ELSE bsrc END AS src
		    FROM calc
		)
		UPDATE works w SET written_year = fin.y, written_year_source = fin.src
		FROM fin
		WHERE w.id = fin.id
		  AND (w.written_year IS DISTINCT FROM fin.y OR w.written_year_source IS DISTINCT FROM fin.src)
		RETURNING w.id`, ids, allWorks, minPlausibleYear, time.Now().Year())
	if err != nil {
		return nil, fmt.Errorf("recompute work years: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

// CleanImplausibleBookYears — разовая чистка накопленных fb2-годов изданий по
// правилу plausibleFb2Year (на проде: 7,4 тыс. позже года издания — к году
// издания, 3 в будущем — пусто) и пересчёт года всех работ. Возвращает работы с
// изменившимся годом.
func CleanImplausibleBookYears(ctx context.Context, pool *pgxpool.Pool) ([]int64, error) {
	if _, err := pool.Exec(ctx, `
		UPDATE books SET written_year = NULL, written_year_source = NULL
		WHERE written_year_source = 'fb2_title' AND (written_year < $1 OR written_year > $2)`,
		minPlausibleYear, time.Now().Year()); err != nil {
		return nil, fmt.Errorf("clean implausible book years: %w", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE books SET written_year = edition_year, written_year_source = 'edition_year'
		WHERE written_year_source = 'fb2_title'
		  AND edition_year BETWEEN $1 AND $2 AND written_year > edition_year`,
		minPlausibleYear, time.Now().Year()); err != nil {
		return nil, fmt.Errorf("cap book years by edition: %w", err)
	}
	return recomputeWorkYears(ctx, pool, nil, true)
}

// RecomputeWorkYears — пересчёт года работ ids (после записи внешнего года).
func RecomputeWorkYears(ctx context.Context, pool *pgxpool.Pool, ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	return recomputeWorkYears(ctx, pool, ids, false)
}
