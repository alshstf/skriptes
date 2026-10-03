package metadata

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestCleanupNonScreenAdaptations(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)

	var collID, archID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	mkBook := func(lib string) (bookID, workID int64) {
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO works (title, normalized_title) VALUES ($1, $2) RETURNING id`, lib, lib).Scan(&workID))
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id, adaptations_fetched_at)
			VALUES ($1,$2,$3,'f','fb2',$4,$5,$6,now()) RETURNING id`, collID, archID, lib, lib, lib, workID).Scan(&bookID))
		return
	}
	add := func(book int64, ext, title, kind, url string) {
		_, err := pool.Exec(ctx, `INSERT INTO book_adaptations (book_id, provider, ext_id, title, kind, ext_url)
			VALUES ($1, 'wikidata', $2, $3, $4, NULLIF($5, ''))`, book, ext, title, kind, url)
		require.NoError(t, err)
	}
	film, filmWork := mkBook("film")
	add(film, "Q1", "Хоббит", "film", "https://www.imdb.com/title/tt1/")
	opera, operaWork := mkBook("opera")
	add(opera, "Q2", "Кармен", "other", "https://www.wikidata.org/wiki/Q2")
	series, seriesWork := mkBook("series")
	add(series, "Q3", "Дом Дракона", "other", "https://www.kinopoisk.ru/film/1316601/")
	untitled, _ := mkBook("untitled")
	add(untitled, "Q4", "Q4", "film", "https://www.imdb.com/title/tt4/")

	works, refetch, err := CleanupNonScreenAdaptations(ctx, pool)
	require.NoError(t, err)
	require.EqualValues(t, 2, refetch, "«другое» со ссылкой на Кинопоиск/IMDb и запись без названия — перепросить")
	require.NotContains(t, works, filmWork)
	require.Contains(t, works, operaWork)
	require.Contains(t, works, seriesWork)

	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM book_adaptations`).Scan(&n))
	require.Equal(t, 1, n, "остался только фильм")
	var fetched *time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT adaptations_fetched_at FROM books WHERE id=$1`, series).Scan(&fetched))
	require.Nil(t, fetched, "сериал с ссылкой на Кинопоиск перепросится")
	require.NoError(t, pool.QueryRow(ctx, `SELECT adaptations_fetched_at FROM books WHERE id=$1`, opera).Scan(&fetched))
	require.NotNil(t, fetched, "опера без ссылок на кино — не перепрашиваем")
}
