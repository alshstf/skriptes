package catalog

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Хранимые агрегаты автора для сортировок /authors (#302, миграция 0045) и
// латинское имя для поиска (#290/#291, миграция 0046):
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

// RecomputeAuthorStats пересчитывает authors.book_count/max_rating/latin_name целиком и
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
		),
		-- Латинское имя (миграция 0046): фамилия — та, за которую голосует больше
		-- половины книг автора с латинским src-автором, кроме сборников (там в
		-- оригинале часто составитель); имя — самая частая запись с этой фамилией.
		-- По фамилии, а не по записи целиком: у Толкина голоса делятся между
		-- «tolkien john ronald reuel» и «tolkien j. r. r.».
		votes AS (
		    SELECT ba.author_id, lower(btrim(b.src_author_normalized::text)) AS name
		    FROM book_authors ba
		    JOIN books b      ON b.id = ba.book_id AND b.deleted = false
		    LEFT JOIN works w ON w.id = b.work_id
		    WHERE COALESCE(w.kind, '') = ''
		      AND b.src_author_normalized::text ~ '^[a-z]'
		      AND b.src_author_normalized::text !~ '[а-яё]'
		),
		surnames AS (
		    SELECT author_id, split_part(name, ' ', 1) AS surname, count(*) AS n,
		           sum(count(*)) OVER (PARTITION BY author_id) AS total
		    FROM votes GROUP BY 1, 2
		),
		top AS (
		    SELECT DISTINCT ON (author_id) author_id, surname
		    FROM surnames WHERE n * 2 > total
		    ORDER BY author_id, n DESC, surname
		),
		lat AS (
		    SELECT DISTINCT ON (v.author_id) v.author_id, v.name
		    FROM votes v JOIN top t ON t.author_id = v.author_id AND split_part(v.name, ' ', 1) = t.surname
		    GROUP BY v.author_id, v.name
		    ORDER BY v.author_id, count(*) DESC, v.name
		)
		UPDATE authors a SET book_count = COALESCE(s.n, 0), max_rating = s.r, latin_name = lat.name
		FROM authors a2 LEFT JOIN s ON s.author_id = a2.id LEFT JOIN lat ON lat.author_id = a2.id
		WHERE a.id = a2.id
		  AND (a.book_count IS DISTINCT FROM COALESCE(s.n, 0) OR a.max_rating IS DISTINCT FROM s.r
		       OR a.latin_name IS DISTINCT FROM lat.name)`)
	if err != nil {
		return 0, fmt.Errorf("author stats: update: %w", err)
	}
	return tag.RowsAffected(), nil
}

// RecomputeAuthorStats — то же для сервиса каталога (после слияния авторов).
func (s *Service) RecomputeAuthorStats(ctx context.Context) (int64, error) {
	return RecomputeAuthorStats(ctx, s.pool)
}
