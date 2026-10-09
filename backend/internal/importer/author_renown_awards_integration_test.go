package importer_test

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/awards"
	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// #420: премия работы поднимает её известность и через неё — автора; премия,
// врученная лично автору, — отдельным бонусом, даже без других сигналов;
// кинопремия экранизации в известность не идёт.
func TestRecomputeAuthorRenown_Awards(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, _ := testpg.Start(t, ctx)
	imp := importer.New(importer.Deps{Pool: pool, AwardTier: awards.PopularityTier})
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, sql, args...)
		require.NoError(t, err)
	}
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
	mkWork := func(lib string, author int64, rating int) int64 {
		var w, b int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO works (title, normalized_title, primary_author_id) VALUES ($1::text, $1::text, $2) RETURNING id`,
			lib, author).Scan(&w))
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id, rating)
			VALUES ($1,$2,$3::text,'f','fb2',$3::text,$3::text,$4,NULLIF($5,0)) RETURNING id`, collID, archID, lib, w, rating).Scan(&b))
		exec(`INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,0)`, b, author)
		return w
	}
	win := func(award, kind, ref string, work, author *int64) {
		exec(`INSERT INTO award_wins (award, year, kind, title, author, source, source_ref, work_id, author_id)
			VALUES ($1, 2000, $2, '', '', 'manual', $3, $4, $5)`, award, kind, ref, work, author)
	}
	renown := func(id int64) int64 {
		var r int64
		require.NoError(t, pool.QueryRow(ctx, `SELECT renown FROM authors WHERE id = $1`, id).Scan(&r))
		return r
	}

	sf := mkAuthor("Laureate")
	w := mkWork("novel", sf, 5) // LIBRATE 5 → 160
	win("hugo", "work", "h1", &w, nil)
	win("oscar", "work", "o1", &w, nil) // кинопремия — мимо
	poet := mkAuthor("Poet")            // без книг с сигналами
	mkWork("verse", poet, 0)
	win("nobel", "author", "n1", nil, &poet)
	win("bely", "author", "b1", nil, &poet)

	_, err := imp.RecomputeAuthorRenown(ctx)
	require.NoError(t, err)
	// Работа: 160 + «Хьюго» (главная) 200 = 360, значимая → +120·log2(2) = 480.
	require.EqualValues(t, 480, renown(sf))
	// Поэт: только личные премии — Нобелевская (главная) 200 + ещё одна 25.
	require.EqualValues(t, 225, renown(poet))

	// Инкрементальный пересчёт берёт тот же бонус.
	exec(`UPDATE authors SET renown = 0`)
	_, err = imp.RecomputeAuthorRenownFor(ctx, []int64{w})
	require.NoError(t, err)
	require.EqualValues(t, 480, renown(sf))
}
