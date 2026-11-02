package metadata

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

type fixedCoverProvider struct {
	name string
	err  error
}

func (f *fixedCoverProvider) Name() string { return f.name }
func (f *fixedCoverProvider) FetchCover(context.Context, BookQuery) (*CoverImage, error) {
	return nil, f.err
}

// Промах (metadata_fetched_at) ставится, только когда все источники ответили
// «нет обложки» или на паузе. Сбой источника — не промах: без тома с книгами
// fb2 падал на «open archive», и книга навсегда оставалась без обложки (#632).
func TestEnsureCover_MarksMissOnlyOnNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	var collID, archID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	book := func(lib string) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title)
			VALUES ($1,$2,$3,$3,'fb2','T','t') RETURNING id`, collID, archID, lib).Scan(&id))
		return id
	}
	fetched := func(id int64) bool {
		var at *time.Time
		require.NoError(t, pool.QueryRow(ctx, `SELECT metadata_fetched_at FROM books WHERE id=$1`, id).Scan(&at))
		return at != nil
	}

	cases := []struct {
		name      string
		providers []CoverProvider
		marked    bool
	}{
		{"все «нет обложки»", []CoverProvider{&fixedCoverProvider{"fb2", ErrNotFound}, &fixedCoverProvider{"openlibrary", ErrNotFound}}, true},
		{"источник на паузе", []CoverProvider{&fixedCoverProvider{"fb2", ErrNotFound}, &fixedCoverProvider{"googlebooks", ErrSourcePaused}}, true},
		{"fb2 не открыл архив", []CoverProvider{&fixedCoverProvider{"fb2", errors.New("open archive: no such file")}, &fixedCoverProvider{"openlibrary", ErrNotFound}}, false},
		{"внешний 5xx", []CoverProvider{&fixedCoverProvider{"fb2", ErrNotFound}, &fixedCoverProvider{"openlibrary", ErrUpstream}}, false},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, err := New(pool, t.TempDir(), tc.providers, nil, nil, nil, nil, quiet)
			require.NoError(t, err)
			id := book(string(rune('A' + i)))
			e.EnsureCover(ctx, BookQuery{ID: id})
			require.Equal(t, tc.marked, fetched(id))
		})
	}
}
