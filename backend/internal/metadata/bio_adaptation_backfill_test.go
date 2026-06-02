package metadata

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ── фейковые внешние провайдеры ─────────────────────────────────

type fakeBioProvider struct {
	bio   string
	calls int
}

func (f *fakeBioProvider) Name() string { return "wikipedia" }
func (f *fakeBioProvider) FetchAuthorBio(context.Context, AuthorQuery) (string, error) {
	f.calls++
	if f.bio == "" {
		return "", ErrNotFound
	}
	return f.bio, nil
}

type fakePhotoProvider struct{ calls int }

func (f *fakePhotoProvider) Name() string { return "wikipedia" }
func (f *fakePhotoProvider) FetchAuthorPhoto(context.Context, AuthorQuery) (*CoverImage, error) {
	f.calls++
	return nil, ErrNotFound
}

type fakeAdaptProvider struct {
	items []Adaptation
	calls int
}

func (f *fakeAdaptProvider) Name() string { return "wikidata" }
func (f *fakeAdaptProvider) FetchAdaptations(context.Context, BookQuery) ([]Adaptation, error) {
	f.calls++
	return f.items, nil
}

// ── AuthorBackfiller integration ────────────────────────────────

func TestAuthorBackfiller_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := startPGForPrewarm(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	bioProv := &fakeBioProvider{bio: "Биография автора."}
	photoProv := &fakePhotoProvider{}
	enricher, err := New(pool, t.TempDir(), nil, nil,
		[]AuthorPhotoProvider{photoProv}, []AuthorBioProvider{bioProv}, nil, quiet)
	require.NoError(t, err)

	mkAuthor := func(name string) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO authors (last_name, normalized_name) VALUES ($1, $2) RETURNING id`,
			name, strings.ToLower(name)).Scan(&id))
		return id
	}
	a1 := mkAuthor("Толстой")
	a2 := mkAuthor("Достоевский")

	bf := NewAuthorBackfiller(pool, enricher, 0, quiet)
	require.Equal(t, 2, bf.drain(ctx), "оба автора-кандидата обработаны")

	for _, id := range []int64{a1, a2} {
		var bio *string
		var fetched *time.Time
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT bio, metadata_fetched_at FROM authors WHERE id=$1`, id).Scan(&bio, &fetched))
		require.NotNil(t, bio, "bio проставлена")
		require.Equal(t, "Биография автора.", *bio)
		require.NotNil(t, fetched, "metadata_fetched_at помечен (кандидат больше не выбирается)")
	}
	require.Greater(t, photoProv.calls, 0, "фото-провайдер тоже опрошен")

	// Повторный проход — кандидатов нет (маркер стоит).
	require.Equal(t, 0, bf.drain(ctx), "уже обработанные авторы не переобрабатываются")

	// Coverage.
	ctl := NewAuthorBackfillController(pool, enricher, 0, quiet)
	cov, err := ctl.Coverage(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, cov.Total)
	require.Equal(t, 2, cov.WithBio)
	require.Equal(t, 0, cov.WithPhoto, "фото не нашлось (ErrNotFound)")
}

// ── AdaptationBackfiller integration ────────────────────────────

func TestAdaptationBackfiller_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := startPGForPrewarm(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	year := 1972
	adaptProv := &fakeAdaptProvider{items: []Adaptation{
		{Provider: "wikidata", ExtID: "Q1", Title: "Фильм", Year: &year, Kind: "film"},
	}}
	enricher, err := New(pool, t.TempDir(), nil, nil, nil, nil,
		[]AdaptationProvider{adaptProv}, quiet)
	require.NoError(t, err)

	var collID, archID, bookID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title)
		VALUES ($1,$2,'L1','f','fb2','Война и мир','война и мир') RETURNING id`,
		collID, archID).Scan(&bookID))

	bf := NewAdaptationBackfiller(pool, enricher, 0, quiet)
	require.Equal(t, 1, bf.drain(ctx), "книга-кандидат обработана")

	var nAdapt int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM book_adaptations WHERE book_id=$1`, bookID).Scan(&nAdapt))
	require.Equal(t, 1, nAdapt, "экранизация записана")

	var fetched *time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT adaptations_fetched_at FROM books WHERE id=$1`, bookID).Scan(&fetched))
	require.NotNil(t, fetched, "adaptations_fetched_at помечен")

	// Повторный проход — кандидатов нет.
	require.Equal(t, 0, bf.drain(ctx), "обработанная книга не переобрабатывается")

	// Coverage.
	ctl := NewAdaptationBackfillController(pool, enricher, 0, quiet)
	cov, err := ctl.Coverage(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, cov.Total)
	require.Equal(t, 1, cov.WithAdaptations)
}
