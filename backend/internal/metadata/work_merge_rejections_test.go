package metadata

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestClusterTier1_RejectedPairs — #517: union-find не склеивает компоненты с
// отклонённой парой работ, в том числе транзитивно через третье издание.
func TestClusterTier1_RejectedPairs(t *testing.T) {
	books := []groupBook{
		{id: 1, workID: 10, normTitle: "война и мир", lang: "ru"},
		{id: 2, workID: 20, normTitle: "война и мир", lang: "ru"},
		{id: 3, workID: 30, normTitle: "война и мир", lang: "ru"},
	}
	require.Equal(t, [][]int64{{1, 2, 3}}, clustersOf(books, clusterTier1(books)))
	rejected := workPairs{workPair(20, 10): {}}
	clusters := clustersOf(books, clusterTier1With(books, rejected))
	require.Len(t, clusters, 2, "10 и 20 порознь; 30 — к одному из них")
	for _, c := range clusters {
		require.False(t, len(c) == 3)
	}
}

// TestWorkGrouper_MergeRejection_Integration — #517: «не объединять» запоминает
// пары; Tier-1.5 (один том серии) их больше не склеивает; слияние вручную
// снимает пару каскадом.
func TestWorkGrouper_MergeRejection_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	var collID, archID, seriesID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	author := seedGroupAuthor(t, ctx, pool, "Гэлбрейт", "гэлбрейт роберт")
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO series (title, normalized_title, author_id) VALUES ('Механизмы радости','механизмы радости',$1) RETURNING id`,
		author).Scan(&seriesID))
	mk := func(lib, title, norm, srcTitle string) (int64, int64) {
		var workID, bookID int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO works (title, normalized_title, primary_author_id, series_id, ser_no) VALUES ($1,$2,$3,$4,3) RETURNING id`,
			title, norm, author, seriesID).Scan(&workID))
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title,
			                   lang, series_id, ser_no, src_title, src_lang, work_id, edition_meta_scanned_at)
			VALUES ($1,$2,$3,'f','fb2',$4,$5,'ru',$6,3,NULLIF($7,''),'en',$8,now()) RETURNING id`,
			collID, archID, lib, title, norm, seriesID, srcTitle, workID).Scan(&bookID))
		_, err := pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,0)`, bookID, author)
		require.NoError(t, err)
		return bookID, workID
	}
	// Том #3 серии: два РАЗНЫХ произведения, которые Tier-1.5 склеил бы по
	// (серия, ser_no) — как два перевода в TestWorkGrouper_Tier15_Series_Integration.
	aBook, aWork := mk("A", "Шестерёнки", "шестерёнки", "Gears")
	bBook, bWork := mk("B", "Пружина", "пружина", "")

	c := NewWorkGroupController(pool, nil, nil, WorkGroupConfig{}, nil, quiet)
	n, err := c.RejectMerge(ctx, []int64{bWork, aWork, aWork}, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, n, "одна пара, дубли id схлопнуты")
	n, err = c.RejectMerge(ctx, []int64{aWork, bWork}, 0)
	require.NoError(t, err)
	require.Zero(t, n, "повтор — без новых пар")
	_, err = c.RejectMerge(ctx, []int64{aWork}, 0)
	require.Error(t, err)

	g := NewWorkGrouper(pool, nil, nil, WorkGroupConfig{}, nil, quiet)
	g.drainAll(ctx)
	require.NotEqual(t, workIDOf(t, ctx, pool, aBook), workIDOf(t, ctx, pool, bBook),
		"отклонённая пара не склеена фоновой группировкой")

	// Ручное слияние админом — поглощённая работа удалена, пара уходит каскадом.
	_, err = c.MergeWorks(ctx, []int64{aWork, bWork}, aWork)
	require.NoError(t, err)
	var left int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM work_merge_rejections`).Scan(&left))
	require.Zero(t, left)
}
