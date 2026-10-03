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

// TestImport_UnchangedRecordsNotRewritten — повторный импорт не переписывает
// книги, авторов, серии, архивы и связи, в которых ничего не изменилось, и не
// шлёт их документы в Meili повторно (#301: на проде каждый импорт делал 1,7 млн
// UPDATE и 4,5 млн удалений/вставок связей за 53 минуты). Изменённые записи
// пишутся; документ неизменной книги, которого нет в индексе, отправляется.
func TestImport_UnchangedRecordsNotRewritten(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, _ := testpg.Start(t, ctx)
	mgr := startMeilisearch(t, ctx)
	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr})
	dir := t.TempDir()

	books := []inpxtest.Book{
		{LibID: "940001", Title: "Первая", Lang: "ru", Authors: []string{"Иванов,Иван,Иванович"},
			Genres: []string{"sf", "adventure"}, Series: "Цикл", SerNo: 1, Rating: 4},
		{LibID: "940002", Title: "Вторая", Lang: "ru", Authors: []string{"Иванов,Иван,Иванович", "Петров,Пётр"},
			Genres: []string{"sf"}, Series: "Цикл", SerNo: 2},
		{LibID: "940003", Title: "Третья", Lang: "en", Authors: []string{"Петров,Пётр"}, Genres: []string{"det_police"}},
	}
	run := func(bs []inpxtest.Book) importer.Stats {
		t.Helper()
		// Тот же файл повторно — сбросить хэш, иначе импорт пропустится целиком.
		_, err := pool.Exec(ctx, `UPDATE collections SET last_inpx_hash = NULL`)
		require.NoError(t, err)
		path, err := inpxtest.WriteINPX(dir, "lib.inpx", bs)
		require.NoError(t, err)
		st, err := imp.Run(ctx, path)
		require.NoError(t, err)
		require.Zero(t, st.Errors)
		return st
	}
	// Версии строк: xmin меняется при любом UPDATE, ctid связи — при delete+insert.
	snapshot := func() map[string]string {
		t.Helper()
		out := map[string]string{}
		for _, q := range []string{
			`SELECT 'b'||id, xmin::text FROM books`,
			`SELECT 'a'||id, xmin::text FROM authors`,
			`SELECT 's'||id, xmin::text FROM series`,
			`SELECT 'r'||id, xmin::text FROM archives`,
			`SELECT 'ba'||book_id||'/'||author_id, ctid::text FROM book_authors`,
			`SELECT 'bg'||book_id||'/'||genre_id, ctid::text FROM book_genres`,
		} {
			rows, err := pool.Query(ctx, q)
			require.NoError(t, err)
			for rows.Next() {
				var k, v string
				require.NoError(t, rows.Scan(&k, &v))
				out[k] = v
			}
			require.NoError(t, rows.Err())
			rows.Close()
		}
		return out
	}
	bookID := func(lib string) int64 {
		t.Helper()
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM books WHERE lib_id = $1`, lib).Scan(&id))
		return id
	}

	st := run(books)
	require.Equal(t, 3, st.BooksInserted)
	require.Equal(t, 3, st.BooksIndexed)
	before := snapshot()

	// Тот же INPX ещё раз: ничего не переписано, в Meili ничего не ушло.
	st = run(books)
	require.Equal(t, 0, st.BooksInserted)
	require.Equal(t, 0, st.BooksUpdated)
	require.Equal(t, 3, st.BooksUnchanged)
	require.Equal(t, 0, st.BooksIndexed)
	require.Equal(t, before, snapshot(), "ни одна строка не переписана")

	// Изменились название второй книги и жанры третьей — пишутся только они.
	// Год написания (его наполняет обогащение) отправленный документ несёт сам.
	_, err := pool.Exec(ctx, `UPDATE books SET written_year = 1985 WHERE lib_id = '940002'`)
	require.NoError(t, err)
	before = snapshot()
	books[1].Title = "Вторая (новая редакция)"
	books[2].Genres = []string{"det_police", "thriller"}
	st = run(books)
	require.Equal(t, 2, st.BooksUpdated)
	require.Equal(t, 1, st.BooksUnchanged)
	require.Equal(t, 2, st.BooksIndexed)
	after := snapshot()
	b1, b2, b3 := bookID("940001"), bookID("940002"), bookID("940003")
	require.Equal(t, before[bookKey(b1)], after[bookKey(b1)], "первая не переписана")
	require.NotEqual(t, before[bookKey(b2)], after[bookKey(b2)], "вторая переписана")
	require.Equal(t, before[bookKey(b3)], after[bookKey(b3)], "у третьей поменялись только жанры")
	var title string
	require.NoError(t, pool.QueryRow(ctx, `SELECT title FROM books WHERE id = $1`, b2).Scan(&title))
	require.Equal(t, "Вторая (новая редакция)", title)
	var genres int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM book_genres WHERE book_id = $1`, b3).Scan(&genres))
	require.Equal(t, 2, genres)
	var doc struct {
		Title string `json:"title"`
		Year  *int   `json:"year"`
	}
	require.NoError(t, mgr.Index("books").GetDocumentWithContext(ctx, strconv.FormatInt(b2, 10), nil, &doc))
	require.Equal(t, "Вторая (новая редакция)", doc.Title)
	require.NotNil(t, doc.Year)
	require.Equal(t, 1985, *doc.Year)

	// Порядок авторов — тоже изменение.
	books[1].Authors = []string{"Петров,Пётр", "Иванов,Иван,Иванович"}
	st = run(books)
	require.Equal(t, 1, st.BooksUpdated)
	var first string
	require.NoError(t, pool.QueryRow(ctx, `SELECT a.last_name FROM book_authors ba JOIN authors a ON a.id = ba.author_id
		WHERE ba.book_id = $1 ORDER BY ba.position LIMIT 1`, b2).Scan(&first))
	require.Equal(t, "Петров", first)

	// Индекс книг пуст (база восстановлена на чистый Meili, #305) — документы
	// неизменных книг импорт всё равно отправляет.
	task, err := mgr.Index("books").DeleteAllDocumentsWithContext(ctx, nil)
	require.NoError(t, err)
	_, err = mgr.WaitForTaskWithContext(ctx, task.TaskUID, 0)
	require.NoError(t, err)
	st = run(books)
	require.Equal(t, 3, st.BooksUnchanged)
	require.Equal(t, 3, st.BooksIndexed)
}

// bookKey — ключ книги в снимке версий строк.
func bookKey(id int64) string {
	return "b" + strconv.FormatInt(id, 10)
}
