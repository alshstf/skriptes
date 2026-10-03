package importer

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CatalogFix — что поправил FixCatalogInvariants.
type CatalogFix struct {
	PrimaryAuthors  int     // работам поставлен основной автор
	EditionCounts   int     // исправлен счётчик живых изданий
	BooklessDeleted int     // удалены работы без единой книги
	SeriesDetached  int     // работы без живых изданий отвязаны от серии
	SeriesDeleted   int64   // удалены пустые серии
	Changed         []int64 // работы с живыми изданиями, чей документ поиска надо обновить
}

// FixCatalogInvariants чинит то, что копится в каталоге между импортами (#307):
// мягкое удаление книг импортом не собирает мусор.
//
//   - Основной автор — и у работ, где его не было (75 живых работ на проде:
//     RegroupAll их пропускал).
//   - edition_count = число ЖИВЫХ изданий (так его читают популярность, кандидаты
//     «Известности» и группировки): после удаления книги импортом счётчик не
//     пересчитывался — у 87 тыс. работ без живых изданий стояло 1, у живых с
//     удалёнными изданиями — завышенная популярность.
//   - Работа без единой книги (нарушение инварианта) удаляется, если на ней нет
//     пользовательских данных (оценки, запросы оценки, скрытия из ленты).
//   - Работа без живых изданий отпускает серию: иначе серия, в которой не
//     осталось ни одной книги, не удалялась (1 380 таких на проде). Если издание
//     вернётся в следующем выпуске, syncWorkSeries поставит серию обратно; ручные
//     правки серии не трогаем.
//   - Пустые серии удаляются (deleteEmptySeries).
//
// Работы без живых изданий не удаляются: на них висят удалённые издания
// (инвариант «у каждой книги есть работа»), а вернувшееся издание вернёт и
// работу со всеми данными. Авторы без книг тоже остаются: на них ссылаются
// журналы разделения тёзок и перепроверки, а из списков и подсказок их и так
// убирает фильтр по видимым книгам.
func FixCatalogInvariants(ctx context.Context, pool *pgxpool.Pool) (CatalogFix, error) {
	var res CatalogFix
	// Проходы по всем работам и книгам — без параллельных воркеров: в контейнере
	// Postgres /dev/shm по умолчанию 64 МБ, параллельный хэш на ней падает
	// («could not resize shared memory segment», прод 2026-10, #304), а в один
	// поток это секунды.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return res, err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SET max_parallel_workers_per_gather = 0`); err != nil {
		return res, err
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), `RESET max_parallel_workers_per_gather`) }()
	db := conn.Conn()
	changed := map[int64]struct{}{}

	ids, err := fixWorkPrimaryAuthors(ctx, db)
	if err != nil {
		return res, fmt.Errorf("fix primary authors: %w", err)
	}
	res.PrimaryAuthors = len(ids)
	addIDs(changed, ids)

	if ids, err = collectIDs(ctx, db, `
		WITH c AS (
			SELECT w.id, count(b.id) FILTER (WHERE NOT b.deleted) AS n
			FROM works w LEFT JOIN books b ON b.work_id = w.id
			GROUP BY w.id
		)
		UPDATE works w SET edition_count = c.n, updated_at = now()
		FROM c
		WHERE w.id = c.id AND w.edition_count IS DISTINCT FROM c.n
		RETURNING w.id`); err != nil {
		return res, fmt.Errorf("recount editions: %w", err)
	}
	res.EditionCounts = len(ids)
	addIDs(changed, ids)

	if ids, err = collectIDs(ctx, db, `
		DELETE FROM works w
		WHERE NOT EXISTS (SELECT 1 FROM books b WHERE b.work_id = w.id)
		  AND NOT EXISTS (SELECT 1 FROM book_ratings r WHERE r.work_id = w.id)
		  AND NOT EXISTS (SELECT 1 FROM book_rating_prompts p WHERE p.work_id = w.id)
		  AND NOT EXISTS (SELECT 1 FROM feed_dismissals d WHERE d.work_id = w.id)
		RETURNING w.id`); err != nil {
		return res, fmt.Errorf("delete bookless works: %w", err)
	}
	res.BooklessDeleted = len(ids)
	for _, id := range ids {
		delete(changed, id)
	}

	if ids, err = collectIDs(ctx, db, `
		UPDATE works w SET series_id = NULL, ser_no = NULL, updated_at = now()
		WHERE w.series_id IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM books b WHERE b.work_id = w.id AND NOT b.deleted)
		  AND NOT EXISTS (
		      SELECT 1 FROM metadata_overrides o
		      WHERE o.target_kind = 'work' AND o.target_id = w.id AND o.field IN ('series', 'ser_no'))
		RETURNING w.id`); err != nil {
		return res, fmt.Errorf("detach dead works from series: %w", err)
	}
	res.SeriesDetached = len(ids)

	if res.SeriesDeleted, err = deleteEmptySeries(ctx, db); err != nil {
		return res, fmt.Errorf("delete empty series: %w", err)
	}

	// В поиске — только работы с живыми изданиями; документы остальных убирает
	// сверка индексов, а UpsertWorksToIndex удалит их и сам.
	for id := range changed {
		res.Changed = append(res.Changed, id)
	}
	return res, nil
}

// dbConn — общее у *pgxpool.Pool и *pgx.Conn для шагов после импорта.
type dbConn interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

func collectIDs(ctx context.Context, db dbConn, query string, args ...any) ([]int64, error) {
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}
