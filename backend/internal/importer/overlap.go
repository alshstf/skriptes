package importer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/inpx"
)

// ErrOverlappingCollection — рядом лежит другой INPX с теми же книгами.
var ErrOverlappingCollection = errors.New("inpx overlaps another inpx in use")

// OverlapError — подробности ErrOverlappingCollection для лога.
//
// Книга — это файл: (имя архива, lib_id), из какого бы INPX она ни пришла
// (миграция 0039), так что два INPX одной библиотеки дублей не создают. Но если
// оба лежат в каталоге, каждый выпуск импортировался бы дважды, а метаданные
// одного перезаписывали бы метаданные другого. Реальный случай — lib.rus.ec с
// сентября 2026: librusec_mhl.inpx (формат MyHomeLib, продолжение прежнего
// librusec_local_fb2.inpx) и librusec_flib.inpx (те же книги в формате FLibrary),
// #250. Поэтому второй такой INPX пропускается, пока владелец не оставит один
// (или не выберет его в SKRIPTES_INPX_FILES).
//
// Если прежнего INPX в каталоге уже нет (раздача его переименовала), новый файл
// просто продолжает те же книги — это не ошибка.
type OverlapError struct {
	File           string // имя INPX, который пропускаем
	Collection     string // коллекция, за которой числятся его книги
	CollectionFile string // её INPX (collections.inpx_filename), он тоже в каталоге
	Matched        int    // сколько записей выборки числятся за ней
	Sampled        int    // размер выборки
}

func (e *OverlapError) Error() string {
	return fmt.Sprintf("%s: %d of %d sampled records belong to collection %q (%s), whose INPX is also in use",
		e.File, e.Matched, e.Sampled, e.Collection, e.CollectionFile)
}

func (e *OverlapError) Unwrap() error { return ErrOverlappingCollection }

// overlapSampleSize — сколько первых записей INPX сверять с базой. Одна и та же
// библиотека совпадает почти целиком, разные — практически ни в чём (у разных
// библиотек разные имена архивов), так что выборки хватает.
const overlapSampleSize = 1000

var errSampleFull = errors.New("sample full")

// collectionHash — хэш последнего удачного импорта этого INPX-файла; "" — файл
// ещё не импортировался.
func collectionHash(ctx context.Context, pool *pgxpool.Pool, inpxFilename string) (string, error) {
	var hash string
	err := pool.QueryRow(ctx,
		`SELECT COALESCE(last_inpx_hash, '') FROM collections WHERE inpx_filename = $1`, inpxFilename).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read collection hash: %w", err)
	}
	return hash, nil
}

// overlapOwner сверяет первые overlapSampleSize записей INPX с книгами в базе
// по (имя архива, lib_id) и возвращает другую коллекцию (не с этим файлом), за
// которой числится хотя бы половина выборки; nil — такой нет.
func overlapOwner(ctx context.Context, pool *pgxpool.Pool, ix *inpx.Inpx, inpxFilename string) (*OverlapError, error) {
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
		return nil, fmt.Errorf("sample inpx records: %w", err)
	}
	if len(libIDs) == 0 {
		return nil, nil
	}

	var name, file string
	var matched int
	err = pool.QueryRow(ctx, `
		SELECT c.name, c.inpx_filename, count(*)
		FROM unnest($1::text[], $2::text[]) AS s(archive, lib_id)
		JOIN archives a    ON a.filename = s.archive
		JOIN books b       ON b.archive_id = a.id AND b.lib_id = s.lib_id
		JOIN collections c ON c.id = b.collection_id
		WHERE c.inpx_filename <> $3
		GROUP BY c.id
		ORDER BY count(*) DESC
		LIMIT 1`, archives, libIDs, inpxFilename).Scan(&name, &file, &matched)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("check overlap: %w", err)
	}
	if matched*2 < len(libIDs) {
		return nil, nil
	}
	return &OverlapError{File: inpxFilename, Collection: name, CollectionFile: file, Matched: matched, Sampled: len(libIDs)}, nil
}

// inpxInUse — INPX-файл name лежит в каталоге dir и выбран для импорта
// (SKRIPTES_INPX_FILES пуст или содержит его).
func inpxInUse(dir, name string, only []string) bool {
	if allow := cleanNames(only); len(allow) > 0 && !slices.Contains(allow, name) {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, name))
	return err == nil && info.Mode().IsRegular()
}
