package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/inpx"
)

// hashFile считает sha256 файла потоково, чтобы не держать INPX в памяти.
func hashFile(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // путь приходит из конфигурации, не из юзер-инпута
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// upsertCollection возвращает id коллекции INPX-файла (создаёт при первом
// импорте). inpxFilename — basename файла, name — имя из collection.info.
func upsertCollection(ctx context.Context, pool *pgxpool.Pool, inpxFilename, name string) (int64, error) {
	var id int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO collections (name, inpx_filename)
		VALUES ($1, $2)
		ON CONFLICT (inpx_filename) DO UPDATE SET name = EXCLUDED.name
		RETURNING id
	`, name, inpxFilename).Scan(&id); err != nil {
		return 0, fmt.Errorf("upsert collection: %w", err)
	}
	return id, nil
}

// markCollectionImported проставляет хэш, время и версию INPX (version.info)
// после успешного импорта. version — содержимое version.info как есть (пустое →
// NULL, если файла не было).
func markCollectionImported(ctx context.Context, pool *pgxpool.Pool, collectionID int64, hash, version string) error {
	_, err := pool.Exec(ctx,
		`UPDATE collections SET last_inpx_hash = $1, last_imported_at = now(), inpx_version = NULLIF($3, '') WHERE id = $2`,
		hash, collectionID, strings.TrimSpace(version))
	return err
}

// upsertArchive возвращает id записи archives по имени файла: архив один на все
// коллекции (все лежат в BOOKS_ROOT). collection_id — коллекция, которая
// описала архив последней.
func upsertArchive(ctx context.Context, q querier, collectionID int64, filename string) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `
		INSERT INTO archives (collection_id, filename)
		VALUES ($1, $2)
		ON CONFLICT (filename) DO UPDATE SET collection_id = EXCLUDED.collection_id
		WHERE archives.collection_id IS DISTINCT FROM EXCLUDED.collection_id
		RETURNING id
	`, collectionID, filename).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) { // архив уже числится за этим INPX
		err = q.QueryRow(ctx, `SELECT id FROM archives WHERE filename = $1`, filename).Scan(&id)
	}
	if err != nil {
		return 0, fmt.Errorf("upsert archive %q: %w", filename, err)
	}
	return id, nil
}

// upsertAuthor возвращает id для автора (создаёт если не было) и признак, что
// строка записана (вставлена или имя дополнено) — тогда документы поиска его
// работ надо обновить.
func upsertAuthor(ctx context.Context, q querier, a inpx.Author) (int64, bool, error) {
	norm := normalizedAuthorName(a)
	if norm == "" {
		return 0, false, fmt.Errorf("empty normalized author name")
	}
	note := strings.TrimSpace(a.Note)
	if note == "" {
		// Запись без уточнения, а тёзки с этим именем уже разделены (файл более
		// старого выпуска): ложимся на наследника прежней записи, а если
		// разделения не было, но автор с этим именем один — на него. Иначе
		// появился бы лишний «автор без уточнения».
		id, ok, err := existingPlainAuthor(ctx, q, norm)
		if err != nil {
			return 0, false, err
		}
		if ok {
			return id, false, nil
		}
	}
	var noteArg any
	if note != "" {
		noteArg = note
	}
	var id int64
	var written bool
	// Неизменного автора не переписываем (#301): иначе каждый импорт обновлял
	// все ~200 тыс. строк authors.
	err := q.QueryRow(ctx, `
		WITH up AS (
			INSERT INTO authors (last_name, first_name, middle_name, normalized_name, name_note)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (normalized_name, (lower(COALESCE(name_note, '')))) DO UPDATE SET
				last_name   = COALESCE(NULLIF(EXCLUDED.last_name,   ''), authors.last_name),
				first_name  = COALESCE(NULLIF(EXCLUDED.first_name,  ''), authors.first_name),
				middle_name = COALESCE(NULLIF(EXCLUDED.middle_name, ''), authors.middle_name)
			WHERE (authors.last_name, authors.first_name, authors.middle_name) IS DISTINCT FROM
			      (COALESCE(NULLIF(EXCLUDED.last_name,   ''), authors.last_name),
			       COALESCE(NULLIF(EXCLUDED.first_name,  ''), authors.first_name),
			       COALESCE(NULLIF(EXCLUDED.middle_name, ''), authors.middle_name))
			RETURNING id
		)
		SELECT id, true FROM up
		UNION ALL
		SELECT id, false FROM authors
		WHERE normalized_name = $4 AND lower(COALESCE(name_note, '')) = lower(COALESCE($5::text, ''))
		  AND NOT EXISTS (SELECT 1 FROM up)
	`, a.LastName, a.FirstName, a.MiddleName, norm, noteArg).Scan(&id, &written)
	if err != nil {
		return 0, false, fmt.Errorf("upsert author %q: %w", norm, err)
	}
	return id, written, nil
}

// existingPlainAuthor — куда положить автора без уточнения: запись без
// уточнения → наследник разделённой записи (author_splits) → единственный
// автор с этим именем. ok=false — нужна новая запись.
func existingPlainAuthor(ctx context.Context, q querier, norm string) (int64, bool, error) {
	var id int64
	err := q.QueryRow(ctx, `
		SELECT id FROM (
			SELECT id, 0 AS prio FROM authors WHERE normalized_name = $1 AND name_note IS NULL
			UNION ALL
			SELECT s.old_author_id, 1 FROM author_splits s
			WHERE s.base_name = $1 AND s.is_heir AND s.old_author_id IS NOT NULL
			  -- вариант без уточнения, не ставший наследником, — отдельный тёзка
			  AND NOT EXISTS (SELECT 1 FROM author_splits p
			                  WHERE p.base_name = $1 AND p.note = '' AND NOT p.is_heir)
			UNION ALL
			SELECT min(id), 2 FROM authors WHERE normalized_name = $1 HAVING count(*) = 1
		) c
		ORDER BY prio
		LIMIT 1
	`, norm).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("find author %q: %w", norm, err)
	}
	return id, true, nil
}

// upsertSeries возвращает id серии для (normalized_title, author_id).
// Если author_id = 0 — серия без привязки к автору.
func upsertSeries(ctx context.Context, q querier, title string, authorID int64, multi bool) (int64, error) {
	norm := normalize(title)
	if norm == "" {
		return 0, fmt.Errorf("empty normalized series title")
	}
	var id int64
	var aid any
	if authorID > 0 {
		aid = authorID
	} else {
		aid = nil
	}
	// UNIQUE (normalized_title, author_id) — но NULL в author_id создаёт
	// проблему с ON CONFLICT (NULL != NULL в Postgres). Поэтому для случая
	// authorID = 0 идём через SELECT-then-INSERT.
	if authorID == 0 {
		err := q.QueryRow(ctx,
			`SELECT id FROM series WHERE normalized_title = $1 AND author_id IS NULL`,
			norm).Scan(&id)
		if err == nil {
			if multi {
				if _, err := q.Exec(ctx, `UPDATE series SET kind = 'multi' WHERE id = $1 AND kind IS NULL`, id); err != nil {
					return 0, fmt.Errorf("mark series %q multi: %w", norm, err)
				}
			}
			return id, nil
		}
		if err != pgx.ErrNoRows {
			return 0, fmt.Errorf("lookup series %q: %w", norm, err)
		}
		var kind any
		if multi {
			kind = "multi"
		}
		err = q.QueryRow(ctx,
			`INSERT INTO series (title, normalized_title, author_id, kind) VALUES ($1, $2, NULL, $3) RETURNING id`,
			title, norm, kind).Scan(&id)
		if err != nil {
			return 0, fmt.Errorf("insert series %q: %w", norm, err)
		}
		return id, nil
	}
	err := q.QueryRow(ctx, `
		WITH up AS (
			INSERT INTO series (title, normalized_title, author_id)
			VALUES ($1, $2, $3)
			ON CONFLICT (normalized_title, author_id) DO UPDATE SET title = EXCLUDED.title
			WHERE series.title IS DISTINCT FROM EXCLUDED.title
			RETURNING id
		)
		SELECT id FROM up
		UNION ALL
		SELECT id FROM series WHERE normalized_title = $2 AND author_id = $3 AND NOT EXISTS (SELECT 1 FROM up)
	`, title, norm, aid).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert series %q: %w", norm, err)
	}
	return id, nil
}

// upsertGenre возвращает id жанра по FB2-коду; если такого кода нет —
// создаёт запись с NULL в name_ru/name_en. Локализованные имена
// заполняются отдельно — internal/genres.Seed на startup'е backend'а
// прописывает name_ru для всех known кодов из встроенного словаря.
//
// Для кодов которых нет в словаре (новые из реальных коллекций) —
// name_ru остаётся NULL, и SELECT'ы делают COALESCE на fb2_code как
// fallback (видно в catalog.ListGenres и books.Get).
//
// ON CONFLICT DO NOTHING — мы не хотим перетирать локализованные имена
// которые поставил Seed: importer работает асинхронно (после startup'а),
// и без NOTHING он сбросил бы name_ru обратно в NULL для каждого
// импортируемого жанра.
func upsertGenre(ctx context.Context, q querier, code string) (int64, error) {
	if code == "" {
		return 0, fmt.Errorf("empty genre code")
	}
	var id int64
	err := q.QueryRow(ctx, `
		INSERT INTO genres (fb2_code) VALUES ($1)
		ON CONFLICT (fb2_code) DO UPDATE SET fb2_code = EXCLUDED.fb2_code
		RETURNING id
	`, code).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert genre %q: %w", code, err)
	}
	return id, nil
}

// upsertBookResult — что вернул upsertBook.
type upsertBookResult struct {
	ID      int64
	WorkID  int64  // 0 у только что вставленной (работу заводит ensureSingletonWork)
	Year    *int16 // written_year — уходит в документ поиска (year), см. bookDoc
	Created bool   // вставлена впервые
	Written bool   // строка переписана (вставлена или изменилось хоть одно поле)
	// Текущие связи книги до этого импорта — сравнить с записью INPX и не
	// переписывать совпадающие (#301). У новой книги пустые.
	AuthorIDs []int64 // в порядке position
	GenreIDs  []int64 // по возрастанию
}

// upsertBook делает INSERT ON CONFLICT DO UPDATE; идемпотентно по
// (archive_id, lib_id) — одна строка на файл книги, из какого бы INPX она ни
// пришла (миграция 0039). collection_id — INPX, который описал книгу последним.
//
// Строка переписывается, только если хоть одно поле отличается (#301): прежде
// каждый импорт переписывал все 552 тыс. книг (1,7 млн UPDATE, 0 HOT) — 53 минуты
// на выпуске, где изменился 1 % записей. Неизменная книга — один запрос на чтение:
// id, работа и текущие авторы/жанры для сравнения в processRecord.
func upsertBook(ctx context.Context, q querier, in bookRow) (upsertBookResult, error) {
	var r upsertBookResult
	err := q.QueryRow(ctx, `
		WITH up AS (
			INSERT INTO books (
				collection_id, archive_id, lib_id, file_name, ext, size_bytes,
				title, normalized_title, series_id, ser_no, lang, date_added,
				rating, keywords, deleted
			) VALUES (
				$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15
			)
			ON CONFLICT (archive_id, lib_id) DO UPDATE SET
				collection_id    = EXCLUDED.collection_id,
				file_name        = EXCLUDED.file_name,
				ext              = EXCLUDED.ext,
				size_bytes       = EXCLUDED.size_bytes,
				title            = EXCLUDED.title,
				normalized_title = EXCLUDED.normalized_title,
				series_id        = EXCLUDED.series_id,
				ser_no           = EXCLUDED.ser_no,
				lang             = EXCLUDED.lang,
				date_added       = EXCLUDED.date_added,
				rating           = EXCLUDED.rating,
				keywords         = EXCLUDED.keywords,
				deleted          = EXCLUDED.deleted,
				updated_at       = now()
			WHERE (books.collection_id, books.file_name, books.ext, books.size_bytes,
			       books.title, books.normalized_title, books.series_id, books.ser_no,
			       books.lang, books.date_added, books.rating, books.keywords, books.deleted)
			      IS DISTINCT FROM
			      (EXCLUDED.collection_id, EXCLUDED.file_name, EXCLUDED.ext, EXCLUDED.size_bytes,
			       EXCLUDED.title, EXCLUDED.normalized_title, EXCLUDED.series_id, EXCLUDED.ser_no,
			       EXCLUDED.lang, EXCLUDED.date_added, EXCLUDED.rating, EXCLUDED.keywords, EXCLUDED.deleted)
			RETURNING id, COALESCE(work_id, 0) AS work_id, written_year, (xmax = 0) AS inserted
		), row AS (
			SELECT id, work_id, written_year, inserted, true AS written FROM up
			UNION ALL
			SELECT id, COALESCE(work_id, 0), written_year, false, false FROM books
			WHERE archive_id = $2 AND lib_id = $3 AND NOT EXISTS (SELECT 1 FROM up)
		)
		-- Связи читаются из снимка ДО вставки: у новой книги их нет.
		SELECT r.id, r.work_id, r.written_year, r.inserted, r.written,
		       ARRAY(SELECT ba.author_id FROM book_authors ba WHERE ba.book_id = r.id ORDER BY ba.position),
		       ARRAY(SELECT bg.genre_id FROM book_genres bg WHERE bg.book_id = r.id ORDER BY bg.genre_id)
		FROM row r
	`,
		in.collectionID, in.archiveID, in.libID, in.fileName, in.ext, in.size,
		in.title, in.normalizedTitle, in.seriesID, in.serNo, in.lang, in.dateAdded,
		in.rating, in.keywords, in.deleted,
	).Scan(&r.ID, &r.WorkID, &r.Year, &r.Created, &r.Written, &r.AuthorIDs, &r.GenreIDs)
	if err != nil {
		return upsertBookResult{}, fmt.Errorf("upsert book lib_id=%s: %w", in.libID, err)
	}
	return r, nil
}

// ensureSingletonWork создаёт отдельную логическую работу (works) для
// только что вставленной книги и проставляет books.work_id. Поддерживает
// инвариант «у каждой книги есть work_id» (миграция 0017 сделала это для
// существующих, импорт — для новых). Группировка нескольких изданий в одну
// работу — отдельная opt-in фоновая джоба, не здесь.
func ensureSingletonWork(ctx context.Context, q querier, bookID int64, in bookRow, primaryAuthorID int64) (int64, error) {
	var aid any
	if primaryAuthorID > 0 {
		aid = primaryAuthorID
	}
	var workID int64
	if err := q.QueryRow(ctx, `
		INSERT INTO works (title, normalized_title, primary_author_id, series_id, ser_no)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, in.title, in.normalizedTitle, aid, in.seriesID, in.serNo).Scan(&workID); err != nil {
		return 0, fmt.Errorf("insert work for book %d: %w", bookID, err)
	}
	if _, err := q.Exec(ctx, `UPDATE books SET work_id = $2 WHERE id = $1`, bookID, workID); err != nil {
		return 0, fmt.Errorf("set book %d work_id: %w", bookID, err)
	}
	return workID, nil
}

// sameAuthors — авторы книги те же и в том же порядке.
func sameAuthors(current, next []int64) bool {
	return slices.Equal(current, next)
}

// sameGenres — набор жанров тот же (current отсортирован, next — как в записи,
// с возможными повторами).
func sameGenres(current, next []int64) bool {
	n := slices.Clone(next)
	slices.Sort(n)
	return slices.Equal(current, slices.Compact(n))
}

// replaceBookAuthors переписывает m:n book↔author для одной книги.
func replaceBookAuthors(ctx context.Context, q querier, bookID int64, authorIDs []int64) error {
	if _, err := q.Exec(ctx, `DELETE FROM book_authors WHERE book_id = $1`, bookID); err != nil {
		return fmt.Errorf("delete book_authors: %w", err)
	}
	for i, aid := range authorIDs {
		if _, err := q.Exec(ctx,
			`INSERT INTO book_authors (book_id, author_id, position) VALUES ($1, $2, $3)`,
			bookID, aid, i,
		); err != nil {
			return fmt.Errorf("insert book_author: %w", err)
		}
	}
	return nil
}

// replaceBookGenres переписывает m:n book↔genre для одной книги.
func replaceBookGenres(ctx context.Context, q querier, bookID int64, genreIDs []int64) error {
	if _, err := q.Exec(ctx, `DELETE FROM book_genres WHERE book_id = $1`, bookID); err != nil {
		return fmt.Errorf("delete book_genres: %w", err)
	}
	for _, gid := range genreIDs {
		if _, err := q.Exec(ctx,
			`INSERT INTO book_genres (book_id, genre_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			bookID, gid,
		); err != nil {
			return fmt.Errorf("insert book_genre: %w", err)
		}
	}
	return nil
}

// querier — общий интерфейс для *pgxpool.Pool и pgx.Tx, чтобы upsert-функции
// могли работать как вне, так и внутри транзакций.
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconnTag, error)
}

// pgconnTag — алиас на возвращаемый тип Exec (минимальная поверхность).
type pgconnTag = pgconnCommandTag

// Эти типы вытащены отдельно, чтобы querier не утянул весь pgconn.
type pgconnCommandTag = interface{ String() string }
