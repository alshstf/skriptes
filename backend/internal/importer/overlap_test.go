package importer_test

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	meili "github.com/meilisearch/meilisearch-go"
	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/skriptes/skriptes/backend/internal/inpx/inpxtest"
	"github.com/stretchr/testify/require"
)

// TestImport_SameBooksFromAnotherInpx — книга = (архив, lib_id), из какого бы INPX
// она ни пришла (#250, миграция 0039):
//   - второй INPX той же библиотеки рядом с первым пропускается (OverlapError);
//   - если прежний INPX из каталога ушёл (раздача переименовала файл) или не
//     выбран в SKRIPTES_INPX_FILES, новый продолжает те же строки: id книг не
//     меняются, приходит только дельта;
//   - INPX другой библиотеки — своя коллекция.
func TestImport_SameBooksFromAnotherInpx(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, _ := startPostgres(t, ctx)
	mgr := startMeilisearch(t, ctx)
	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr})
	dir := t.TempDir()

	countRows := func(q string) int {
		t.Helper()
		var n int
		require.NoError(t, pool.QueryRow(ctx, q).Scan(&n))
		return n
	}
	bookIDs := func() map[string]int64 {
		t.Helper()
		rows, err := pool.Query(ctx, `SELECT lib_id, id FROM books`)
		require.NoError(t, err)
		defer rows.Close()
		out := map[string]int64{}
		for rows.Next() {
			var lib string
			var id int64
			require.NoError(t, rows.Scan(&lib, &id))
			out[lib] = id
		}
		require.NoError(t, rows.Err())
		return out
	}

	books := []inpxtest.Book{
		{LibID: "910001", Title: "Первая", Lang: "ru", Authors: []string{"Иванов,Иван"}},
		{LibID: "910002", Title: "Вторая", Lang: "ru", Authors: []string{"Иванов,Иван"}},
		{LibID: "910003", Title: "Третья", Lang: "ru", Authors: []string{"Петров,Пётр"}},
	}
	oldPath, err := inpxtest.WriteINPX(dir, "librusec_local_fb2.inpx", books)
	require.NoError(t, err)
	stats, err := imp.Run(ctx, oldPath)
	require.NoError(t, err)
	require.Equal(t, 3, stats.BooksInserted)
	before := bookIDs()

	// Новый выпуск под другим именем: те же книги + одна новая.
	next := append(append([]inpxtest.Book(nil), books...), inpxtest.Book{LibID: "910004", Title: "Новая", Lang: "ru"})
	mhl, err := inpxtest.WriteINPX(dir, "librusec_mhl.inpx", next)
	require.NoError(t, err)

	// Старый файл ещё лежит рядом — второй INPX той же библиотеки, пропуск.
	_, err = imp.Run(ctx, mhl)
	require.ErrorIs(t, err, importer.ErrOverlappingCollection)
	var ov *importer.OverlapError
	require.True(t, errors.As(err, &ov))
	require.Equal(t, "librusec_mhl.inpx", ov.File)
	require.Equal(t, "librusec_local_fb2.inpx", ov.CollectionFile)
	require.Equal(t, 3, ov.Matched)
	require.Equal(t, 4, ov.Sampled)
	require.Equal(t, 1, countRows(`SELECT count(*) FROM collections`), "коллекция не заводится")
	require.Equal(t, 3, countRows(`SELECT count(*) FROM books`))

	// Старого файла больше нет — раздача его переименовала: продолжаем те же книги.
	require.NoError(t, os.Remove(oldPath))
	stats, err = imp.Run(ctx, mhl)
	require.NoError(t, err)
	require.Equal(t, 1, stats.BooksInserted, "пришла только дельта")
	require.Equal(t, 3, stats.BooksUpdated)
	after := bookIDs()
	require.Len(t, after, 4)
	for lib, id := range before {
		require.Equal(t, id, after[lib], "id книги %s не изменился", lib)
	}
	require.Equal(t, 4, countRows(`SELECT count(*) FROM books b JOIN collections c ON c.id = b.collection_id
		WHERE c.inpx_filename = 'librusec_mhl.inpx'`), "книги числятся за новым INPX")
	require.Equal(t, 1, countRows(`SELECT count(*) FROM archives`), "архив один")

	// Рядом flib с теми же книгами, но в SKRIPTES_INPX_FILES выбран он, а не mhl —
	// тоже продолжение, не пропуск.
	flib, err := inpxtest.WriteINPX(dir, "librusec_flib.inpx", next)
	require.NoError(t, err)
	flibOnly := importer.New(importer.Deps{Pool: pool, Meili: mgr, InpxFiles: []string{"librusec_flib.inpx"}})
	_, err = flibOnly.Run(ctx, flib)
	require.NoError(t, err)
	require.Equal(t, 4, countRows(`SELECT count(*) FROM books`), "дублей нет")

	// Другая библиотека (другие lib_id) — своя коллекция.
	other, err := inpxtest.WriteINPX(dir, "other_library.inpx", []inpxtest.Book{
		{LibID: "920001", Title: "Чужая", Lang: "en", Authors: []string{"Smith,John"}},
	})
	require.NoError(t, err)
	stats, err = imp.Run(ctx, other)
	require.NoError(t, err)
	require.Equal(t, 1, stats.BooksInserted)
	require.Equal(t, 5, countRows(`SELECT count(*) FROM books`))
}

// TestPurgeDedupedDocs — после миграции 0039, схлопнувшей дубли, backend на
// старте убирает удалённые книги и работы из поиска и снимает запись.
func TestPurgeDedupedDocs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, _ := startPostgres(t, ctx)
	mgr := startMeilisearch(t, ctx)
	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr})

	path, err := inpxtest.WriteINPX(t.TempDir(), "lib.inpx", []inpxtest.Book{
		{LibID: "930001", Title: "Остаётся", Lang: "ru", Authors: []string{"Иванов,Иван"}},
		{LibID: "930002", Title: "Дубль", Lang: "ru", Authors: []string{"Петров,Пётр"}},
	})
	require.NoError(t, err)
	_, err = imp.Run(ctx, path)
	require.NoError(t, err)

	var keepBook, keepWork, goneBook, goneWork int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT id, work_id FROM books WHERE lib_id='930001'`).Scan(&keepBook, &keepWork))
	require.NoError(t, pool.QueryRow(ctx, `SELECT id, work_id FROM books WHERE lib_id='930002'`).Scan(&goneBook, &goneWork))
	// Как после миграции: книга и её работа удалены в PG, запись о них оставлена.
	_, err = pool.Exec(ctx, `DELETE FROM books WHERE id = $1`, goneBook)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM works WHERE id = $1`, goneWork)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO app_settings (key, value) VALUES ('book_identity_dedup_v1',
		jsonb_build_object('books', jsonb_build_array($1::bigint), 'works_gone', jsonb_build_array($2::bigint),
		                   'works_update', jsonb_build_array($3::bigint)))`, goneBook, goneWork, keepWork)
	require.NoError(t, err)

	n, err := imp.PurgeDedupedDocs(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	hasDoc := func(index string, id int64) bool {
		t.Helper()
		var doc map[string]any
		err := mgr.Index(index).GetDocumentWithContext(ctx, strconv.FormatInt(id, 10), nil, &doc)
		var me *meili.Error
		if errors.As(err, &me) && me.StatusCode == 404 {
			return false
		}
		require.NoError(t, err)
		return true
	}
	require.False(t, hasDoc("books", goneBook), "удалённая книга ушла из books")
	require.False(t, hasDoc("works", goneWork), "опустевшая работа ушла из works")
	require.True(t, hasDoc("books", keepBook))
	require.True(t, hasDoc("works", keepWork))

	var left int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM app_settings WHERE key='book_identity_dedup_v1'`).Scan(&left))
	require.Zero(t, left, "запись снята")
	n, err = imp.PurgeDedupedDocs(ctx)
	require.NoError(t, err)
	require.Zero(t, n, "повторный запуск — no-op")
}
