package importer_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/skriptes/skriptes/backend/internal/inpx"
	"github.com/skriptes/skriptes/backend/internal/inpx/inpxtest"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestImport_DuplicateAuthorInRecord — прод-кейс 2026-09-26. Запись с одним автором
// дважды откатывалась (PK book_authors), а новый автор из неё оставался в кэше с id из
// отката → следующая книга того же автора падала на FK. Теперь дубль схлопывается,
// обе книги импортируются, порядок авторов сохранён.
func TestImport_DuplicateAuthorInRecord(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, _ := testpg.Start(t, ctx)
	mgr := startMeilisearch(t, ctx)

	path, err := inpxtest.WriteINPX(t.TempDir(), "dup.inpx", []inpxtest.Book{
		{LibID: "900001", Title: "Неканонический классик", Lang: "ru", Genres: []string{"sci_philology"},
			Authors: []string{"Новиков,Первый,Автор", "Добренко,Евгений,Александрович", "Добренко,Евгений,Александрович"}},
		{LibID: "900002", Title: "Вторая книга нового автора", Lang: "ru", Genres: []string{"sci_philology"},
			Authors: []string{"Новиков,Первый,Автор"}},
	})
	require.NoError(t, err)

	stats, err := importer.New(importer.Deps{Pool: pool, Meili: mgr}).Run(ctx, path)
	require.NoError(t, err)
	require.Equal(t, 0, stats.Errors, "ни одна запись не должна упасть")
	require.Equal(t, 2, stats.BooksInserted)

	rows, err := pool.Query(ctx, `
		SELECT a.last_name FROM book_authors ba
		JOIN authors a ON a.id = ba.author_id
		JOIN books b ON b.id = ba.book_id
		WHERE b.lib_id = '900001' ORDER BY ba.position`)
	require.NoError(t, err)
	var got []string
	for rows.Next() {
		var n string
		require.NoError(t, rows.Scan(&n))
		got = append(got, n)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{"Новиков", "Добренко"}, got, "дубль схлопнут, порядок — как в записи")
}

// TestCaches_RolledBackIDNotCached — id, полученный в откаченной транзакции, не
// попадает в общий кэш: следующий вызов вставляет автора заново и получает живой id.
func TestCaches_RolledBackIDNotCached(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, _ := testpg.Start(t, ctx)
	c := importer.NewTestCaches()
	a := inpx.Author{LastName: "Откатов", FirstName: "Иван"}

	// Каждой транзакции — отложенный Rollback: при провале require открытая транзакция
	// держала бы соединение, и pool.Close в Cleanup висел бы вместо чистого FAIL.
	begin := func() pgx.Tx {
		tx, err := pool.Begin(ctx)
		require.NoError(t, err)
		t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
		return tx
	}

	tx := begin()
	rolledBack, err := c.EnsureAuthorTx(ctx, tx, a)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback(ctx))
	c.DropStaged() // так делает processRecord при откате

	tx = begin()
	id, err := c.EnsureAuthorTx(ctx, tx, a)
	require.NoError(t, err)
	require.NotEqual(t, rolledBack, id, "id из отката не должен вернуться из кэша")
	require.NoError(t, tx.Commit(ctx))
	c.CommitStaged()

	var exists bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM authors WHERE id = $1)`, id).Scan(&exists))
	require.True(t, exists)

	// После Commit id уже в общем кэше: в новой транзакции — тот же id без повторной вставки.
	tx = begin()
	again, err := c.EnsureAuthorTx(ctx, tx, a)
	require.NoError(t, err)
	require.Equal(t, id, again)
}
