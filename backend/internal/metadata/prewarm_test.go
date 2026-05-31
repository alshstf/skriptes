package metadata

import (
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/db"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestLocalProviderFilter — чисто, без docker. Гарантирует ключевой
// инвариант прогрева: local-фильтр пропускает только fb2-провайдер и
// НИКОГДА не внешние (Open Library / Google Books), у которых rate-limit.
func TestLocalProviderFilter(t *testing.T) {
	httpClient := &http.Client{}
	fb2 := NewFb2Provider()
	ol := NewOpenLibraryProvider(httpClient)
	gb := NewGoogleBooksProvider(httpClient)

	// Cover-провайдеры.
	require.True(t, isLocalCover(fb2), "fb2 должен считаться local")
	require.False(t, isLocalCover(ol), "OpenLibrary НЕ local — внешний API")
	require.False(t, isLocalCover(gb), "GoogleBooks НЕ local — внешний API")

	// Annotation-провайдеры (те же fb2/ol/gb реализуют AnnotationProvider).
	require.True(t, isLocalAnnotation(fb2), "fb2 должен считаться local")
	require.False(t, isLocalAnnotation(ol), "OpenLibrary НЕ local")
	require.False(t, isLocalAnnotation(gb), "GoogleBooks НЕ local")
}

// spyCoverProvider — внешний (НЕ local) cover-провайдер, считающий
// вызовы. Прогрев не должен его дёргать.
type spyCoverProvider struct{ calls int }

func (s *spyCoverProvider) Name() string { return "spy" }
func (s *spyCoverProvider) FetchCover(context.Context, BookQuery) (*CoverImage, error) {
	s.calls++
	return nil, ErrNotFound
}

// TestPrewarmer_FillsCoverAndAnnotationFromFb2 — интеграционный
// (testcontainers PG). Прогрев должен достать обложку и аннотацию из
// fb2-архива, проставить cover_path/annotation/metadata_fetched_at и
// НЕ дёрнуть внешний провайдер.
func TestPrewarmer_FillsCoverAndAnnotationFromFb2(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := startPGForPrewarm(t, ctx)

	// fb2 с annotation + coverpage внутри zip.
	rawJPEG := []byte("JPEG-COVER-BYTES")
	encoded := base64.StdEncoding.EncodeToString(rawJPEG)
	fb2 := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<FictionBook xmlns:l="http://www.w3.org/1999/xlink">
  <description>
    <title-info>
      <annotation><p>Тестовое описание книги.</p></annotation>
      <coverpage><image l:href="#cover.jpg"/></coverpage>
    </title-info>
  </description>
  <body><p>text</p></body>
  <binary id="cover.jpg" content-type="image/jpeg">` + encoded + `</binary>
</FictionBook>`)
	zipPath := makeFB2Archive(t, fb2) // <tmp>/test.zip с book.fb2 внутри
	booksRoot := filepath.Dir(zipPath)
	archiveName := filepath.Base(zipPath) // "test.zip"

	// Вставляем минимальные строки: collection → archive → book.
	var collID, archID, bookID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('test','test.inpx') RETURNING id`,
	).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1, $2) RETURNING id`,
		collID, archiveName,
	).Scan(&archID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title)
		 VALUES ($1, $2, 'L1', 'book', 'fb2', 'Test', 'test') RETURNING id`,
		collID, archID,
	).Scan(&bookID))

	spy := &spyCoverProvider{}
	fb2p := NewFb2Provider()
	enricher, err := New(
		pool,
		t.TempDir(),                // coverRoot
		[]CoverProvider{fb2p, spy}, // fb2 (local) + внешний spy
		[]AnnotationProvider{fb2p}, // только fb2
		nil, nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	require.NoError(t, err)

	prewarmer := NewPrewarmer(enricher, pool, booksRoot,
		PrewarmConfig{Covers: true, Annotations: true, Years: false, Workers: 2}, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	n := prewarmer.drain(ctx)
	require.Equal(t, 1, n, "должна обработаться ровно одна книга")

	var coverPath, annotation *string
	var fetchedAt *time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT cover_path, annotation, metadata_fetched_at FROM books WHERE id = $1`, bookID,
	).Scan(&coverPath, &annotation, &fetchedAt))

	require.NotNil(t, coverPath, "cover_path должен быть проставлен из fb2")
	require.NotEmpty(t, *coverPath)
	require.NotNil(t, annotation, "annotation должна быть проставлена из fb2")
	require.Contains(t, *annotation, "Тестовое описание")
	require.NotNil(t, fetchedAt, "metadata_fetched_at должен пометиться")
	require.Equal(t, 0, spy.calls, "прогрев НЕ должен дёргать внешний провайдер")

	// Повторный проход не должен ничего находить (книга уже помечена).
	require.Equal(t, 0, prewarmer.drain(ctx), "уже прогретая книга не переобрабатывается")
}

// TestCandidateCond — кандидатное условие зависит от включённых под-типов.
func TestCandidateCond(t *testing.T) {
	require.Equal(t, "(b.metadata_fetched_at IS NULL OR b.year_local_scanned_at IS NULL)",
		candidateCond(PrewarmConfig{Covers: true, Annotations: true, Years: true}))
	require.Equal(t, "(b.metadata_fetched_at IS NULL)", candidateCond(PrewarmConfig{Covers: true}))
	require.Equal(t, "(b.metadata_fetched_at IS NULL)", candidateCond(PrewarmConfig{Annotations: true}))
	require.Equal(t, "(b.year_local_scanned_at IS NULL)", candidateCond(PrewarmConfig{Years: true}))
	require.Equal(t, "false", candidateCond(PrewarmConfig{}))
}

type fakeResyncer struct{ calls int }

func (f *fakeResyncer) ResyncYears(context.Context) (int, error) { f.calls++; return 0, nil }

// TestPrewarmer_AutoResyncsYears — при включённом под-тумблере годов прогрев
// извлекает written_year из fb2 и, раз год появился, сам зовёт ResyncYears.
func TestPrewarmer_AutoResyncsYears(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := startPGForPrewarm(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	fb2 := []byte(`<?xml version="1.0" encoding="UTF-8"?>
<FictionBook><description><title-info>
  <date value="1990-01-01">1990</date>
</title-info></description><body><p>text</p></body></FictionBook>`)
	zipPath := makeFB2Archive(t, fb2)
	booksRoot := filepath.Dir(zipPath)
	archiveName := filepath.Base(zipPath)

	var collID, archID, bookID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,$2) RETURNING id`, collID, archiveName).Scan(&archID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title)
		VALUES ($1,$2,'L1','book','fb2','T','t') RETURNING id`, collID, archID).Scan(&bookID))

	fb2p := NewFb2Provider()
	enricher, err := New(pool, t.TempDir(), nil, nil, nil, nil, nil, quiet)
	require.NoError(t, err)
	enricher.WithLocalYear(fb2p)

	res := &fakeResyncer{}
	pw := NewPrewarmer(enricher, pool, booksRoot,
		PrewarmConfig{Years: true, Workers: 1}, res, quiet)
	require.Equal(t, 1, pw.drain(ctx))

	var wy *int16
	require.NoError(t, pool.QueryRow(ctx, `SELECT written_year FROM books WHERE id=$1`, bookID).Scan(&wy))
	require.NotNil(t, wy, "written_year извлечён из fb2")
	require.Equal(t, int16(1990), *wy)
	require.Equal(t, 1, res.calls, "раз год появился — авто-ресинк вызван")

	// Обложки выключены → cover_path не трогали.
	var cover *string
	require.NoError(t, pool.QueryRow(ctx, `SELECT cover_path FROM books WHERE id=$1`, bookID).Scan(&cover))
	require.Nil(t, cover, "под-тумблер обложек выключен — обложку не извлекаем")
}

func startPGForPrewarm(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	pgC, err := postgres.Run(ctx,
		"postgres:17-alpine",
		postgres.WithDatabase("skriptes_test"),
		postgres.WithUsername("skriptes"),
		postgres.WithPassword("skriptes"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pgC.Terminate(context.Background()) })

	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, db.Migrate(dsn))

	pool, err := db.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}
