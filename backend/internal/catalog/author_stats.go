package catalog

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Хранимые агрегаты автора для сортировок /authors (#302, миграция 0045):
// authors.book_count — число работ, authors.max_rating — максимум внешнего
// рейтинга COALESCE(LIBRATE, web); оба по живым изданиям без сборников — те же
// определения, что у book_count/external_rating в строке списка
// (ListAuthorsFiltered без скрытий). Коррелированный подзапрос по всем 140 тыс.
// авторов на каждый запрос стоил ~10 с; полный пересчёт — ~1 с.
//
// Порядок по ним — общий для всех: личные скрытия жанров/языков в ключ не
// входят (числа в строке по-прежнему по видимым книгам).

// authorsBulkLockID — тот же ключ pg_advisory_lock, что у пересчёта известности
// (importer.authorRenownLockID): оба массово обновляют строки authors в
// произвольном порядке и вперемешку ловили бы deadlock.
const authorsBulkLockID = 0x617574687265

// RecomputeAuthorStats пересчитывает authors.book_count/max_rating целиком и
// возвращает число изменённых строк (пишутся только изменившиеся).
func RecomputeAuthorStats(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("author stats: acquire conn: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, authorsBulkLockID); err != nil {
		return 0, fmt.Errorf("author stats: advisory lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, authorsBulkLockID)
	}()
	tag, err := conn.Exec(ctx, `
		WITH s AS (
		    SELECT ba.author_id,
		           count(DISTINCT COALESCE(b.work_id, -b.id))::int AS n,
		           max(COALESCE(b.rating, b.external_rating))::real AS r
		    FROM book_authors ba
		    JOIN books b      ON b.id = ba.book_id AND b.deleted = false
		    LEFT JOIN works w ON w.id = b.work_id
		    WHERE COALESCE(w.kind, '') = ''
		    GROUP BY ba.author_id
		)
		UPDATE authors a SET book_count = COALESCE(s.n, 0), max_rating = s.r
		FROM authors a2 LEFT JOIN s ON s.author_id = a2.id
		WHERE a.id = a2.id
		  AND (a.book_count IS DISTINCT FROM COALESCE(s.n, 0) OR a.max_rating IS DISTINCT FROM s.r)`)
	if err != nil {
		return 0, fmt.Errorf("author stats: update: %w", err)
	}
	return tag.RowsAffected(), nil
}
