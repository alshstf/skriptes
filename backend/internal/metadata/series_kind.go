package metadata

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Издательские серии одного автора (#468): series.kind = 'publisher'. На
// карточке книги во всех представлениях показывается только авторский цикл
// (kind IS NULL); межавторские ('multi') и издательские — нет (правило владельца
// 2026-10-09). Признаки издательской:
//
//   - название — редакционная группировка: «<Автор>. Сборники», «Собрание
//     сочинений…», «Избранное», «Новые переводы»;
//   - или устройство: ≥2 работ, ни у одной нет номера в серии, и у большинства
//     есть издание того же языка вне серии (переиздание отдельных книг, как
//     «Джордж Оруэлл. Новые переводы»). У цикла почти все тома с номерами и
//     почти без изданий вне него («Хроники Дюны» — 0 из 9, «Эндер» — 0 из 13).
//
// Сомнительное остаётся циклом: спрятать настоящий цикл хуже, чем показать
// лишнюю серию. Прод 2026-10: 1262 серии из 55,5 тыс. одноавторских, 6182 работы.
const publisherSeriesTitleRe = `(\mсборники\M|собрание сочинений|\mизбранное\M|избранные произведения|новые переводы|полное собрание)`

// ClassifyPublisherSeries ставит и снимает kind='publisher' у одноавторских
// серий по признакам выше и пересчитывает серию затронутых работ (у работы в
// издательской и в цикле — цикл). Идемпотентен. Возвращает работы для
// переиндексации.
func ClassifyPublisherSeries(ctx context.Context, pool *pgxpool.Pool) ([]int64, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		WITH sw AS (
			SELECT b.series_id, b.work_id, min(b.lang) AS lang, bool_or(b.ser_no > 0) AS numbered
			FROM books b JOIN series s ON s.id = b.series_id
			WHERE b.deleted = false AND b.work_id IS NOT NULL AND (s.kind IS NULL OR s.kind = 'publisher')
			GROUP BY b.series_id, b.work_id
		), outside AS (
			SELECT DISTINCT sw.series_id, sw.work_id
			FROM sw JOIN books o ON o.work_id = sw.work_id AND o.deleted = false AND o.lang = sw.lang
			WHERE o.series_id IS DISTINCT FROM sw.series_id
		), st AS (
			SELECT sw.series_id, count(*) AS works, count(*) FILTER (WHERE sw.numbered) AS numbered,
			       count(o.work_id) AS outside
			FROM sw LEFT JOIN outside o USING (series_id, work_id)
			GROUP BY sw.series_id
		), want AS (
			SELECT s.id FROM series s LEFT JOIN st ON st.series_id = s.id
			WHERE (s.kind IS NULL OR s.kind = 'publisher')
			  AND (s.title ~* $1 OR (st.works >= 2 AND st.numbered = 0 AND st.outside * 2 >= st.works))
		), marked AS (
			UPDATE series s SET kind = 'publisher' FROM want WHERE s.id = want.id AND s.kind IS NULL RETURNING s.id
		), unmarked AS (
			UPDATE series s SET kind = NULL
			WHERE s.kind = 'publisher' AND NOT EXISTS (SELECT 1 FROM want WHERE want.id = s.id) RETURNING s.id
		)
		SELECT id FROM marked UNION ALL SELECT id FROM unmarked`, publisherSeriesTitleRe)
	if err != nil {
		return nil, fmt.Errorf("classify publisher series: %w", err)
	}
	changed, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, fmt.Errorf("classify publisher series: %w", err)
	}
	if len(changed) == 0 {
		return nil, tx.Commit(ctx)
	}
	works, err := scanInt64s(ctx, tx, `
		SELECT DISTINCT work_id FROM books
		WHERE series_id = ANY($1) AND deleted = false AND work_id IS NOT NULL
		UNION
		SELECT id FROM works WHERE series_id = ANY($1)`, changed)
	if err != nil {
		return nil, fmt.Errorf("works of reclassified series: %w", err)
	}
	if err := recomputeWorkAggregates(ctx, tx, works); err != nil {
		return nil, err
	}
	return works, tx.Commit(ctx)
}
