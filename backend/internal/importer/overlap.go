package importer

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/inpx"
)

// ErrOverlappingCollection — INPX ещё не импортировался, но описывает книги,
// которые уже лежат в другой коллекции.
var ErrOverlappingCollection = errors.New("inpx overlaps an existing collection")

// OverlapError — подробности ErrOverlappingCollection для лога.
//
// Коллекция определяется именем INPX-файла, книга — (коллекция, архив, lib_id).
// Если раздача переименовала INPX или рядом лежат два INPX одной библиотеки,
// импорт завёл бы новую коллекцию и вставил каждую книгу второй раз (#250).
// Такой файл не импортируется. Реальный случай — lib.rus.ec, сентябрь 2026:
// вместо librusec_local_fb2.inpx пришли librusec_mhl.inpx (формат MyHomeLib,
// тот же, что был) и librusec_flib.inpx (те же книги в формате FLibrary,
// со skriptes не проверялся) — по словам раздающего. Переименованный INPX
// не подхватывается как старая коллекция автоматически (#254): владелец
// оставляет в каталоге один файл (mhl) под прежним именем.
type OverlapError struct {
	File           string // имя нового INPX
	Collection     string // коллекция, с которой он пересёкся
	CollectionFile string // её INPX (collections.inpx_filename)
	Matched        int    // сколько записей выборки уже есть в ней
	Sampled        int    // размер выборки
}

func (e *OverlapError) Error() string {
	return fmt.Sprintf("%s: %d of %d sampled records already belong to collection %q (%s)",
		e.File, e.Matched, e.Sampled, e.Collection, e.CollectionFile)
}

func (e *OverlapError) Unwrap() error { return ErrOverlappingCollection }

// overlapSampleSize — сколько первых записей нового INPX сверять с базой.
// Одна и та же библиотека совпадает почти целиком, разные — практически ни в
// чём (у разных библиотек разные имена архивов), так что выборки хватает.
const overlapSampleSize = 1000

var errSampleFull = errors.New("sample full")

// collectionKnown — есть ли уже коллекция с этим INPX-файлом.
func collectionKnown(ctx context.Context, pool *pgxpool.Pool, inpxFilename string) (bool, error) {
	var ok bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM collections WHERE inpx_filename = $1)`, inpxFilename).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check collection: %w", err)
	}
	return ok, nil
}

// checkOverlap сверяет первые overlapSampleSize записей нового INPX с книгами
// других коллекций по (имя архива, lib_id). Совпала хотя бы половина выборки →
// *OverlapError.
func checkOverlap(ctx context.Context, pool *pgxpool.Pool, ix *inpx.Inpx, inpxFilename string) error {
	archives := make([]string, 0, overlapSampleSize)
	libIDs := make([]string, 0, overlapSampleSize)
	err := ix.Each(func(f inpx.InpFile, rec inpx.Record) error {
		if rec.LibID == "" {
			return nil
		}
		archives = append(archives, f.Archive)
		libIDs = append(libIDs, rec.LibID)
		if len(libIDs) >= overlapSampleSize {
			return errSampleFull
		}
		return nil
	})
	if err != nil && !errors.Is(err, errSampleFull) {
		return fmt.Errorf("sample inpx records: %w", err)
	}
	if len(libIDs) == 0 {
		return nil
	}

	var name, file string
	var matched int
	err = pool.QueryRow(ctx, `
		SELECT c.name, c.inpx_filename, count(*)
		FROM unnest($1::text[], $2::text[]) AS s(archive, lib_id)
		JOIN archives a    ON a.filename = s.archive
		JOIN books b       ON b.collection_id = a.collection_id AND b.archive_id = a.id AND b.lib_id = s.lib_id
		JOIN collections c ON c.id = a.collection_id
		GROUP BY c.id
		ORDER BY count(*) DESC
		LIMIT 1`, archives, libIDs).Scan(&name, &file, &matched)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check overlap: %w", err)
	}
	if matched*2 < len(libIDs) {
		return nil
	}
	return &OverlapError{File: inpxFilename, Collection: name, CollectionFile: file, Matched: matched, Sampled: len(libIDs)}
}
