package importer_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/skriptes/skriptes/backend/internal/inpx/inpxtest"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestFixCatalogInvariants — мусор, который копит мягкое удаление книг (#307):
// основной автор у работы без него, счётчик живых изданий, работа без книг,
// серия у работы без живых изданий и опустевшая серия.
func TestFixCatalogInvariants(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, _ := testpg.Start(t, ctx)
	mgr := startMeilisearch(t, ctx)
	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr})

	path, err := inpxtest.WriteINPX(t.TempDir(), "lib.inpx", []inpxtest.Book{
		{LibID: "950001", Title: "Первая", Lang: "ru", Authors: []string{"Иванов,Иван"}, Series: "Сага", SerNo: 1},
		{LibID: "950002", Title: "Вторая", Lang: "ru", Authors: []string{"Петров,Пётр"}, Series: "Одиночная", SerNo: 1},
		{LibID: "950003", Title: "Третья", Lang: "ru", Authors: []string{"Сидоров,Сидор"}},
	})
	require.NoError(t, err)
	_, err = imp.Run(ctx, path)
	require.NoError(t, err)

	exec := func(q string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, q, args...)
		require.NoError(t, err)
	}
	scalar := func(q string, args ...any) int64 {
		t.Helper()
		var v int64
		require.NoError(t, pool.QueryRow(ctx, q, args...).Scan(&v))
		return v
	}
	work := func(lib string) int64 {
		return scalar(`SELECT work_id FROM books WHERE lib_id = $1`, lib)
	}
	w1, w2, w3 := work("950001"), work("950002"), work("950003")
	series2 := scalar(`SELECT series_id FROM works WHERE id = $1`, w2)

	// Вторая книга удалена выпуском и ушла из серии, работа держит серию и
	// счётчик 1; у третьей работы потерян основной автор; две работы без книг,
	// на одной — оценка пользователя.
	exec(`UPDATE books SET deleted = true, series_id = NULL WHERE lib_id = '950002'`)
	exec(`UPDATE works SET primary_author_id = NULL WHERE id = $1`, w3)
	exec(`UPDATE works SET edition_count = 5 WHERE id = $1`, w1)
	empty := scalar(`INSERT INTO works (title, normalized_title) VALUES ('Пустая', 'пустая') RETURNING id`)
	rated := scalar(`INSERT INTO works (title, normalized_title) VALUES ('С оценкой', 'с оценкой') RETURNING id`)
	user := scalar(`INSERT INTO users (email, display_name, password_hash, role)
		VALUES ('u@example.com', 'U', 'x', 'user') RETURNING id`)
	exec(`INSERT INTO book_ratings (user_id, work_id, rating) VALUES ($1, $2, 4)`, user, rated)

	fix, err := importer.FixCatalogInvariants(ctx, pool)
	require.NoError(t, err)

	require.Equal(t, 1, fix.PrimaryAuthors)
	require.Equal(t, scalar(`SELECT id FROM authors WHERE last_name = 'Сидоров'`),
		scalar(`SELECT primary_author_id FROM works WHERE id = $1`, w3))

	require.Equal(t, int64(1), scalar(`SELECT edition_count FROM works WHERE id = $1`, w1))
	require.Equal(t, int64(0), scalar(`SELECT edition_count FROM works WHERE id = $1`, w2), "живых изданий нет")

	require.Equal(t, 1, fix.BooklessDeleted)
	require.Zero(t, scalar(`SELECT count(*) FROM works WHERE id = $1`, empty))
	require.Equal(t, int64(1), scalar(`SELECT count(*) FROM works WHERE id = $1`, rated), "оценку не теряем")

	require.Equal(t, 1, fix.SeriesDetached)
	require.Zero(t, scalar(`SELECT count(*) FROM works WHERE id = $1 AND series_id IS NOT NULL`, w2))
	require.Equal(t, int64(1), fix.SeriesDeleted)
	require.Zero(t, scalar(`SELECT count(*) FROM series WHERE id = $1`, series2), "серия без книг удалена")
	require.Equal(t, int64(1), scalar(`SELECT count(*) FROM series WHERE title = 'Сага'`))

	require.True(t, slices.Contains(fix.Changed, w3))
	require.False(t, slices.Contains(fix.Changed, empty))

	// Повторный проход ничего не меняет.
	fix, err = importer.FixCatalogInvariants(ctx, pool)
	require.NoError(t, err)
	require.Zero(t, fix.PrimaryAuthors+fix.EditionCounts+fix.BooklessDeleted+fix.SeriesDetached)
	require.Zero(t, fix.SeriesDeleted)

	// Книга вернулась в следующем выпуске — серия заводится заново, работа снова в ней.
	exec(`UPDATE collections SET last_inpx_hash = NULL`)
	_, err = imp.Run(ctx, path)
	require.NoError(t, err)
	require.Equal(t, int64(1), scalar(`SELECT count(*) FROM works w JOIN series s ON s.id = w.series_id
		WHERE w.id = $1 AND s.title = 'Одиночная'`, w2))
	require.Equal(t, int64(1), scalar(`SELECT edition_count FROM works WHERE id = $1`, w2))
}
