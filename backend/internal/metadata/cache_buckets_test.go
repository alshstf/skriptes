package metadata

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestEnricher_SeparateBuckets — обложки книг / постеры / фото в РАЗНЫХ
// каталогах; «Очистить кэш обложек» сносит только обложки книг, постеры и фото
// не трогает; ResolveCachedFile находит файл в любом бакете. Чисто, без docker.
func TestEnricher_SeparateBuckets(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	dir := t.TempDir()
	e, err := New(nil, filepath.Join(dir, "covers"), nil, nil, nil, nil, nil, quiet)
	require.NoError(t, err)

	require.NotEqual(t, e.cache.Root(), e.posterCache.Root(), "постеры в своём каталоге")
	require.NotEqual(t, e.cache.Root(), e.photoCache.Root(), "фото в своём каталоге")
	require.NotEqual(t, e.posterCache.Root(), e.photoCache.Root())

	coverName, err := e.cache.Save(strings.NewReader("COVER-BYTES"), "image/jpeg")
	require.NoError(t, err)
	posterName, err := e.posterCache.Save(strings.NewReader("POSTER-BYTES"), "image/jpeg")
	require.NoError(t, err)
	photoName, err := e.photoCache.Save(strings.NewReader("PHOTO-BYTES"), "image/jpeg")
	require.NoError(t, err)

	for _, n := range []string{coverName, posterName, photoName} {
		_, ok := e.ResolveCachedFile(n)
		require.True(t, ok, "ResolveCachedFile находит %s", n)
	}

	// «Очистить кэш обложек» — только обложки книг.
	_, err = e.ClearCoverCache()
	require.NoError(t, err)
	_, ok := e.ResolveCachedFile(coverName)
	require.False(t, ok, "обложка книги удалена")
	_, ok = e.ResolveCachedFile(posterName)
	require.True(t, ok, "постер НЕ тронут очисткой обложек")
	_, ok = e.ResolveCachedFile(photoName)
	require.True(t, ok, "фото НЕ тронуто очисткой обложек")
}

// TestEnricher_HealDanglingAssets — висячие указатели (файла нет) зануляются +
// сбрасывается маркер попытки, чтобы дозаполнение их перекачало.
func TestEnricher_HealDanglingAssets(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := startPGForPrewarm(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	e, err := New(pool, filepath.Join(t.TempDir(), "covers"), nil, nil, nil, nil, nil, quiet)
	require.NoError(t, err)

	// Автор с фото-указателем на несуществующий файл.
	var authorID int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO authors (last_name, normalized_name, photo_path, bio, metadata_fetched_at)
		VALUES ('Призрак','призрак','ghost-photo.jpg','есть био', now()) RETURNING id`).Scan(&authorID))

	// Книга + экранизация с постером-указателем на несуществующий файл.
	var collID, archID, bookID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, adaptations_fetched_at)
		VALUES ($1,$2,'L1','f','fb2','T','t', now()) RETURNING id`, collID, archID).Scan(&bookID))
	_, err = pool.Exec(ctx, `
		INSERT INTO book_adaptations (book_id, provider, ext_id, title, kind, poster_path)
		VALUES ($1,'wikidata','Q1','Фильм','film','ghost-poster.jpg')`, bookID)
	require.NoError(t, err)

	e.HealDanglingAssets(ctx)

	// Автор: фото занулено, маркер сброшен (перекачается), bio сохранилось.
	var photo, bio *string
	var aFetched *time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT photo_path, bio, metadata_fetched_at FROM authors WHERE id=$1`, authorID).Scan(&photo, &bio, &aFetched))
	require.Nil(t, photo, "висячее фото занулено")
	require.Nil(t, aFetched, "маркер сброшен → дозаполнение перекачает")
	require.NotNil(t, bio, "bio не тронуто")

	// Экранизация: постер занулён, маркер книги сброшен.
	var poster *string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT poster_path FROM book_adaptations WHERE book_id=$1`, bookID).Scan(&poster))
	require.Nil(t, poster, "висячий постер занулён")
	var bFetched *time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT adaptations_fetched_at FROM books WHERE id=$1`, bookID).Scan(&bFetched))
	require.Nil(t, bFetched, "маркер экранизаций сброшен → перекачаются")
}
