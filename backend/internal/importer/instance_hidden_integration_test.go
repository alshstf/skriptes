package importer_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/skriptes/skriptes/backend/internal/testpg"
)

// RecomputeInstanceHidden: скрытый автор уносит свои и совместные работы и
// книги, чужая работа с его изданием-антологией остаётся (скрыто только это
// издание), явно скрытая работа скрывает свои издания, новое издание скрытого
// автора получает флаг, пересчёт идемпотентен, возврат открывает всё обратно.
func TestRecomputeInstanceHidden(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, _ := testpg.Start(t, ctx)
	imp := importer.New(importer.Deps{Pool: pool})

	var collID, archID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	mkAuthor := func(last string) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO authors (last_name, normalized_name) VALUES ($1, lower($1)) RETURNING id`, last).Scan(&id))
		return id
	}
	mkWork := func(title string, primary int64) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO works (title, normalized_title, primary_author_id) VALUES ($1, lower($1), $2) RETURNING id`,
			title, primary).Scan(&id))
		return id
	}
	mkBook := func(lib string, workID int64, authors ...int64) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id)
			VALUES ($1,$2,$3::text,'f','fb2',$3::text,$3::text,$4) RETURNING id`,
			collID, archID, lib, workID).Scan(&id))
		for i, a := range authors {
			_, err := pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,$3)`, id, a, i)
			require.NoError(t, err)
		}
		return id
	}
	flag := func(table string, id int64) bool {
		var h bool
		require.NoError(t, pool.QueryRow(ctx, `SELECT instance_hidden FROM `+table+` WHERE id = $1`, id).Scan(&h))
		return h
	}

	x, y, z := mkAuthor("Скрытый"), mkAuthor("Соавтор"), mkAuthor("Другой")
	wSolo := mkWork("Своя", x) // два издания X
	bSolo1, bSolo2 := mkBook("s1", wSolo, x), mkBook("s2", wSolo, x)
	wCo := mkWork("Совместная", x) // X + Y
	bCo := mkBook("c1", wCo, x, y)
	wY := mkWork("Соавтора", y) // только Y — видна
	bY := mkBook("y1", wY, y)
	wZ := mkWork("Чужая", z) // три издания Z, одно — антология с X
	bZ1, bZ2, bZanth := mkBook("z1", wZ, z), mkBook("z2", wZ, z), mkBook("z3", wZ, z, x)
	wExplicit := mkWork("Явно скрытая", z)
	bExplicit := mkBook("e1", wExplicit, z)

	// Ничего не скрыто — пересчёт пустой.
	books, works, err := imp.RecomputeInstanceHidden(ctx)
	require.NoError(t, err)
	require.Empty(t, books)
	require.Empty(t, works)

	_, err = pool.Exec(ctx, `INSERT INTO hidden_authors (author_id) VALUES ($1)`, x)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO hidden_works (work_id) VALUES ($1)`, wExplicit)
	require.NoError(t, err)
	books, works, err = imp.RecomputeInstanceHidden(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []int64{wSolo, wCo, wExplicit}, works)
	require.ElementsMatch(t, []int64{bSolo1, bSolo2, bCo, bZanth, bExplicit}, books)
	require.True(t, flag("authors", x))
	require.False(t, flag("authors", y), "соавтор не скрыт")
	require.False(t, flag("works", wY), "работа соавтора без X видна")
	require.False(t, flag("books", bY))
	require.False(t, flag("works", wZ), "чужая работа с изданием-антологией X видна")
	require.False(t, flag("books", bZ1))
	require.False(t, flag("books", bZ2))
	require.True(t, flag("books", bZanth), "издание с X скрыто")

	// Идемпотентность.
	books, works, err = imp.RecomputeInstanceHidden(ctx)
	require.NoError(t, err)
	require.Empty(t, books)
	require.Empty(t, works)

	// «Импорт» новой книги скрытого автора в новой работе — получает флаг.
	wNew := mkWork("Новая", x)
	bNew := mkBook("n1", wNew, x)
	books, works, err = imp.RecomputeInstanceHidden(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []int64{wNew}, works)
	require.ElementsMatch(t, []int64{bNew}, books)

	// Возврат автора: всё его — видно; явно скрытая работа — по-прежнему скрыта.
	_, err = pool.Exec(ctx, `DELETE FROM hidden_authors WHERE author_id = $1`, x)
	require.NoError(t, err)
	books, works, err = imp.RecomputeInstanceHidden(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []int64{wSolo, wCo, wNew}, works)
	require.ElementsMatch(t, []int64{bSolo1, bSolo2, bCo, bZanth, bNew}, books)
	require.False(t, flag("authors", x))
	require.True(t, flag("works", wExplicit))
	require.True(t, flag("books", bExplicit))

	// Возврат работы.
	_, err = pool.Exec(ctx, `DELETE FROM hidden_works WHERE work_id = $1`, wExplicit)
	require.NoError(t, err)
	books, works, err = imp.RecomputeInstanceHidden(ctx)
	require.NoError(t, err)
	require.ElementsMatch(t, []int64{wExplicit}, works)
	require.ElementsMatch(t, []int64{bExplicit}, books)
}
