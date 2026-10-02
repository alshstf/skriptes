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

// srcPhotoProvider — фото по адресу, без скачивания (AuthorPhotoSource).
type srcPhotoProvider struct{ src map[string]string }

func (m *srcPhotoProvider) Name() string { return "openlibrary" }
func (m *srcPhotoProvider) FetchAuthorPhoto(context.Context, AuthorQuery) (*CoverImage, error) {
	panic("сухой прогон не должен скачивать фото")
}
func (m *srcPhotoProvider) AuthorPhotoSource(ctx context.Context, q AuthorQuery) (string, error) {
	if s, ok := m.src[q.LastName]; ok {
		traceStep(ctx, TraceStep{Source: "openlibrary", Stage: "accept", Outcome: TracePass, Value: s})
		return s, nil
	}
	return "", ErrNotFound
}

// TestEnricher_ExplainAuthor — сухой прогон (#280): что нашли бы провайдеры и
// почему; база не меняется, фото не скачивается.
func TestEnricher_ExplainAuthor(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	bios := &mapBioProvider{bio: map[string]string{"Найден": "Найден Имя — писатель."}}
	photos := &srcPhotoProvider{src: map[string]string{"Найден": "https://example.org/a.jpg"}}
	e, err := New(pool, t.TempDir(), nil, nil, []AuthorPhotoProvider{photos}, []AuthorBioProvider{bios}, nil, quiet)
	require.NoError(t, err)

	mk := func(last, bio string) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO authors (last_name, first_name, normalized_name, bio, metadata_fetched_at)
			VALUES ($1, 'Имя', lower($1) || ' имя', NULLIF($2, ''), '2026-01-01') RETURNING id`, last, bio).Scan(&id))
		return id
	}
	found := mk("Найден", "Старая биография")
	missed := mk("Чужой", "Биография однофамильца")

	ex, err := e.ExplainAuthor(ctx, found)
	require.NoError(t, err)
	require.Equal(t, "Найден", ex.LastName)
	require.Equal(t, "Старая биография", ex.CurrentBio)
	require.Equal(t, "wikipedia", ex.Bio.Provider)
	require.Equal(t, "Найден Имя — писатель.", ex.Bio.Value)
	require.Equal(t, "wikipedia/ru: pass accept «Найден»", ex.Bio.Reason)
	require.Equal(t, "openlibrary", ex.Photo.Provider)
	require.Equal(t, "https://example.org/a.jpg", ex.Photo.Value)

	ex, err = e.ExplainAuthor(ctx, missed)
	require.NoError(t, err)
	require.Empty(t, ex.Bio.Provider)
	require.Empty(t, ex.Bio.Value)
	require.Equal(t, "wikipedia/ru: reject name_gate «Однофамилец»", ex.Bio.Reason)
	require.NotEmpty(t, ex.Bio.Steps)

	// Сухой прогон ничего не пишет.
	var bio string
	var fetched time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT bio, metadata_fetched_at FROM authors WHERE id = $1`, missed).Scan(&bio, &fetched))
	require.Equal(t, "Биография однофамильца", bio)
	require.Equal(t, 2026, fetched.Year())
	var journal int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM author_meta_recheck`).Scan(&journal))
	require.Zero(t, journal)

	_, err = e.ExplainAuthor(ctx, 999999)
	require.Error(t, err, "нет автора — ошибка, а не пустой результат")
}
