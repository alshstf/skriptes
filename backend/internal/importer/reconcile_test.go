package importer_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/skriptes/skriptes/backend/internal/inpx/inpxtest"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestReconcileIndexes — #283: документы удалённых книг и опустевших работ
// уходят из поиска, фантомы удаляются, выпавшие работы возвращаются.
func TestReconcileIndexes(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, _ := testpg.Start(t, ctx)
	mgr := startMeilisearch(t, ctx)
	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr})
	dir := t.TempDir()

	run := func(books []inpxtest.Book) {
		t.Helper()
		path, err := inpxtest.WriteINPX(dir, "lib.inpx", books)
		require.NoError(t, err)
		_, err = imp.Run(ctx, path)
		require.NoError(t, err)
	}
	has := func(index string, id int64) bool {
		t.Helper()
		var doc map[string]any
		return mgr.Index(index).GetDocumentWithContext(ctx, strconv.FormatInt(id, 10), nil, &doc) == nil
	}
	ids := func(lib string) (bookID, workID int64) {
		t.Helper()
		require.NoError(t, pool.QueryRow(ctx, `SELECT id, work_id FROM books WHERE lib_id = $1`, lib).Scan(&bookID, &workID))
		return
	}

	books := []inpxtest.Book{
		{LibID: "830001", Title: "Первая", Authors: []string{"Сверка,Тест"}},
		{LibID: "830002", Title: "Вторая", Authors: []string{"Сверка,Тест"}},
		{LibID: "830003", Title: "Третья", Authors: []string{"Сверка,Тест"}},
	}
	run(books)
	b2, w2 := ids("830002")
	require.True(t, has("books", b2))
	require.True(t, has("works", w2))

	// Следующий выпуск пометил вторую книгу удалённой — из поиска она уходит.
	books[1].Deleted = true
	run(books)
	require.False(t, has("books", b2), "документ удалённой книги убран из books")
	require.False(t, has("works", w2), "работа без живых изданий убрана из works")

	// Фантом (работы нет в PG) удаляется; выпавшая живая работа возвращается.
	_, w1 := ids("830001")
	task, err := mgr.Index("works").AddDocumentsWithContext(ctx, []map[string]any{{"id": 9_999_999, "title": "фантом"}}, nil)
	require.NoError(t, err)
	_, err = mgr.WaitForTaskWithContext(ctx, task.TaskUID, 0)
	require.NoError(t, err)
	require.NoError(t, imp.DeleteWorksFromIndex(ctx, []int64{w1}))
	require.True(t, has("works", 9_999_999))
	require.False(t, has("works", w1))

	res, err := imp.ReconcileIndexes(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, res.WorksRemoved)
	require.Equal(t, 1, res.WorksAdded)
	require.False(t, has("works", 9_999_999), "фантом удалён")
	require.True(t, has("works", w1), "живая работа вернулась в индекс")

	// Повторная сверка — ничего не меняет.
	res, err = imp.ReconcileIndexes(ctx)
	require.NoError(t, err)
	require.Equal(t, importer.ReconcileResult{BooksLive: res.BooksLive}, res)
	require.Positive(t, res.BooksLive)
	require.False(t, res.BooksNeedReimport())
}
