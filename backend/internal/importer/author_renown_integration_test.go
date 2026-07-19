package importer_test

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/stretchr/testify/require"
)

// RecomputeAuthorRenown: max-семантика по работам, бонус за широту, сборники
// вне вклада, соавторы получают оба, идемпотентность, сброс выпавших в 0.
// Ожидаемые числа: pop работы с LIBRATE r = 40+24·r (computeWorkPopularity);
// renown = maxPop + 120·log2(1+N значимых, порог 120).
func TestRecomputeAuthorRenown(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, _ := startPostgres(t, ctx)
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
	classic := mkAuthor("Classic")
	coauthor := mkAuthor("Coauthor")
	compiler := mkAuthor("Compiler")
	unknown := mkAuthor("Unknown")

	mkWork := func(title, kind string, primary int64) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO works (title, normalized_title, primary_author_id, kind) VALUES ($1, lower($1), $2, NULLIF($3,'')) RETURNING id`,
			title, primary, kind).Scan(&id))
		return id
	}
	mkBook := func(lib string, workID int64, rating int, authors ...int64) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id, rating)
			VALUES ($1,$2,$3::text,'f','fb2',$3::text,$3::text,$4,NULLIF($5,0)) RETURNING id`,
			collID, archID, lib, workID, rating).Scan(&id))
		for i, a := range authors {
			_, err := pool.Exec(ctx,
				`INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,$3)`, id, a, i)
			require.NoError(t, err)
		}
		return id
	}

	// W1: хит (LIBRATE 5 → pop 160), соавторство classic+coauthor.
	w1 := mkWork("Хит", "", classic)
	b1 := mkBook("w1", w1, 5, classic, coauthor)
	// W2: крепкая работа classic (LIBRATE 4 → pop 136, значимая: ≥120).
	w2 := mkWork("Крепкая", "", classic)
	mkBook("w2", w2, 4, classic)
	// W3: сборник с сигналом — вклада НЕ даёт.
	w3 := mkWork("Сборник рассказов", "collection", compiler)
	mkBook("w3", w3, 5, compiler)
	// W4: без сигналов — pop 0.
	w4 := mkWork("Тишина", "", unknown)
	mkBook("w4", w4, 0, unknown)

	renown := func(id int64) int64 {
		var r int64
		require.NoError(t, pool.QueryRow(ctx, `SELECT renown FROM authors WHERE id = $1`, id).Scan(&r))
		return r
	}

	n, err := imp.RecomputeAuthorRenown(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 2, n, "обновлены только авторы с известностью (classic, coauthor)")

	// classic: maxPop 160, значимых 2 → 160 + round(120·log2(3)) = 160+190 = 350.
	require.EqualValues(t, 350, renown(classic))
	// coauthor: та же работа W1 даёт вклад ОБОИМ → 160 + 120·log2(2) = 280.
	require.EqualValues(t, 280, renown(coauthor))
	// Сборник вне вклада; без сигналов — ноль.
	require.Zero(t, renown(compiler))
	require.Zero(t, renown(unknown))

	// Идемпотентность: повторный прогон ничего не меняет.
	n, err = imp.RecomputeAuthorRenown(ctx)
	require.NoError(t, err)
	require.Zero(t, n)

	// Хит удалён → W1 без живых изданий: classic пересчитан (136+120=256),
	// coauthor выпал из множества «с известностью» → сброс в 0.
	_, err = pool.Exec(ctx, `UPDATE books SET deleted = true WHERE id = $1`, b1)
	require.NoError(t, err)
	_, err = imp.RecomputeAuthorRenown(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 256, renown(classic))
	require.Zero(t, renown(coauthor), "выпавший автор сброшен в 0")
}
