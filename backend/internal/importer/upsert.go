package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"

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

// upsertCollection возвращает id коллекции и предыдущий хэш INPX (если был).
// inpxFilename — basename файла, name — имя из collection.info.
func upsertCollection(ctx context.Context, pool *pgxpool.Pool, inpxFilename, name string) (id int64, prevHash string, err error) {
	row := pool.QueryRow(ctx, `
		INSERT INTO collections (name, inpx_filename)
		VALUES ($1, $2)
		ON CONFLICT (inpx_filename) DO UPDATE SET name = EXCLUDED.name
		RETURNING id, COALESCE(last_inpx_hash, '')
	`, name, inpxFilename)
	if err := row.Scan(&id, &prevHash); err != nil {
		return 0, "", fmt.Errorf("upsert collection: %w", err)
	}
	return id, prevHash, nil
}

// markCollectionImported проставляет хэш и время после успешного импорта.
func markCollectionImported(ctx context.Context, pool *pgxpool.Pool, collectionID int64, hash string) error {
	_, err := pool.Exec(ctx,
		`UPDATE collections SET last_inpx_hash = $1, last_imported_at = now() WHERE id = $2`,
		hash, collectionID)
	return err
}

// upsertArchive возвращает id записи archives для (collection_id, filename).
func upsertArchive(ctx context.Context, q querier, collectionID int64, filename string) (int64, error) {
	var id int64
	err := q.QueryRow(ctx, `
		INSERT INTO archives (collection_id, filename)
		VALUES ($1, $2)
		ON CONFLICT (collection_id, filename) DO UPDATE SET filename = EXCLUDED.filename
		RETURNING id
	`, collectionID, filename).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert archive %q: %w", filename, err)
	}
	return id, nil
}

// upsertAuthor возвращает id для автора (создаёт если не было).
func upsertAuthor(ctx context.Context, q querier, a inpx.Author) (int64, error) {
	norm := normalizedAuthorName(a)
	if norm == "" {
		return 0, fmt.Errorf("empty normalized author name")
	}
	var id int64
	err := q.QueryRow(ctx, `
		INSERT INTO authors (last_name, first_name, middle_name, normalized_name)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (normalized_name) DO UPDATE SET
			last_name   = COALESCE(NULLIF(EXCLUDED.last_name,   ''), authors.last_name),
			first_name  = COALESCE(NULLIF(EXCLUDED.first_name,  ''), authors.first_name),
			middle_name = COALESCE(NULLIF(EXCLUDED.middle_name, ''), authors.middle_name)
		RETURNING id
	`, a.LastName, a.FirstName, a.MiddleName, norm).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert author %q: %w", norm, err)
	}
	return id, nil
}

// upsertSeries возвращает id серии для (normalized_title, author_id).
// Если author_id = 0 — серия без привязки к автору.
func upsertSeries(ctx context.Context, q querier, title string, authorID int64) (int64, error) {
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
			return id, nil
		}
		if err != pgx.ErrNoRows {
			return 0, fmt.Errorf("lookup series %q: %w", norm, err)
		}
		err = q.QueryRow(ctx,
			`INSERT INTO series (title, normalized_title, author_id) VALUES ($1, $2, NULL) RETURNING id`,
			title, norm).Scan(&id)
		if err != nil {
			return 0, fmt.Errorf("insert series %q: %w", norm, err)
		}
		return id, nil
	}
	err := q.QueryRow(ctx, `
		INSERT INTO series (title, normalized_title, author_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (normalized_title, author_id) DO UPDATE SET title = EXCLUDED.title
		RETURNING id
	`, title, norm, aid).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert series %q: %w", norm, err)
	}
	return id, nil
}

// upsertGenre возвращает id жанра по FB2-коду; если такого кода нет — создаёт
// запись с name_ru = name_en = код (улучшение имён — отдельная задача).
func upsertGenre(ctx context.Context, q querier, code string) (int64, error) {
	if code == "" {
		return 0, fmt.Errorf("empty genre code")
	}
	var id int64
	err := q.QueryRow(ctx, `
		INSERT INTO genres (fb2_code, name_ru, name_en)
		VALUES ($1, $1, $1)
		ON CONFLICT (fb2_code) DO UPDATE SET fb2_code = EXCLUDED.fb2_code
		RETURNING id
	`, code).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert genre %q: %w", code, err)
	}
	return id, nil
}

// upsertBookResult — что вернул upsertBook: id и факт создания (insert vs update).
type upsertBookResult struct {
	ID      int64
	Created bool
}

// upsertBook делает INSERT ON CONFLICT DO UPDATE; идемпотентно по
// (collection_id, archive_id, lib_id).
func upsertBook(ctx context.Context, q querier, in bookRow) (upsertBookResult, error) {
	var id int64
	var inserted bool
	err := q.QueryRow(ctx, `
		INSERT INTO books (
			collection_id, archive_id, lib_id, file_name, ext, size_bytes,
			title, normalized_title, series_id, ser_no, lang, date_added,
			rating, keywords, deleted
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15
		)
		ON CONFLICT (collection_id, archive_id, lib_id) DO UPDATE SET
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
		RETURNING id, (xmax = 0) AS inserted
	`,
		in.collectionID, in.archiveID, in.libID, in.fileName, in.ext, in.size,
		in.title, in.normalizedTitle, in.seriesID, in.serNo, in.lang, in.dateAdded,
		in.rating, in.keywords, in.deleted,
	).Scan(&id, &inserted)
	if err != nil {
		return upsertBookResult{}, fmt.Errorf("upsert book lib_id=%s: %w", in.libID, err)
	}
	return upsertBookResult{ID: id, Created: inserted}, nil
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
