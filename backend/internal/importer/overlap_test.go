package importer_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/skriptes/skriptes/backend/internal/inpx/inpxtest"
	"github.com/stretchr/testify/require"
)

// TestImport_OverlappingCollectionSkipped — #250: раздача переименовала INPX
// (или рядом лежит второй INPX той же библиотеки). Новый файл с теми же
// (архив, lib_id) не заводит вторую коллекцию и не дублирует книги; INPX другой
// библиотеки и повторный импорт известного файла работают как раньше.
func TestImport_OverlappingCollectionSkipped(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, _ := startPostgres(t, ctx)
	mgr := startMeilisearch(t, ctx)
	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr})
	dir := t.TempDir()

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

	countRows := func(q string) int {
		t.Helper()
		var n int
		require.NoError(t, pool.QueryRow(ctx, q).Scan(&n))
		return n
	}

	// Тот же набор книг под новым именем — пропуск, коллекция не заводится.
	// Одна книга новая: пересечение 3 из 4 — всё ещё «та же библиотека».
	renamed, err := inpxtest.WriteINPX(dir, "librusec_flib.inpx",
		append(append([]inpxtest.Book(nil), books...), inpxtest.Book{LibID: "910004", Title: "Новая", Lang: "ru"}))
	require.NoError(t, err)
	_, err = imp.Run(ctx, renamed)
	require.ErrorIs(t, err, importer.ErrOverlappingCollection)
	var ov *importer.OverlapError
	require.True(t, errors.As(err, &ov))
	require.Equal(t, "librusec_flib.inpx", ov.File)
	require.Equal(t, "librusec_local_fb2.inpx", ov.CollectionFile)
	require.Equal(t, 3, ov.Matched)
	require.Equal(t, 4, ov.Sampled)
	require.Equal(t, 1, countRows(`SELECT count(*) FROM collections`))
	require.Equal(t, 3, countRows(`SELECT count(*) FROM books`))

	// Другая библиотека (другие lib_id) импортируется отдельной коллекцией.
	other, err := inpxtest.WriteINPX(dir, "other_library.inpx", []inpxtest.Book{
		{LibID: "920001", Title: "Чужая", Lang: "en", Authors: []string{"Smith,John"}},
	})
	require.NoError(t, err)
	stats, err = imp.Run(ctx, other)
	require.NoError(t, err)
	require.Equal(t, 1, stats.BooksInserted)
	require.Equal(t, 2, countRows(`SELECT count(*) FROM collections`))

	// Новая версия известного файла — обычный повторный импорт, без проверки.
	_, err = inpxtest.WriteINPX(dir, "librusec_local_fb2.inpx",
		append(append([]inpxtest.Book(nil), books...), inpxtest.Book{LibID: "910005", Title: "Свежая", Lang: "ru"}))
	require.NoError(t, err)
	stats, err = imp.Run(ctx, oldPath)
	require.NoError(t, err)
	require.False(t, stats.Skipped)
	require.Equal(t, 1, stats.BooksInserted)
	require.Equal(t, 5, countRows(`SELECT count(*) FROM books`))
}
