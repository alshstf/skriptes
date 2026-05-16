package adaptations_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/adaptations"
	"github.com/skriptes/skriptes/backend/internal/db"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestAdaptationsService_List — проверяет:
//   - ErrBookNotFound для несуществующей книги
//   - enrichment_status: pending (NULL fetched_at) → "pending", иначе "done"
//   - сортировку: сначала с известным годом по возрастанию, потом NULL year в конце
//   - корректное чтение NULL-полей (director, poster_path, ext_url)
func TestAdaptationsService_List(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPostgres(t, ctx)

	svc := adaptations.New(pool)

	// Несуществующая книга → ErrBookNotFound.
	_, err := svc.List(ctx, 99999)
	require.ErrorIs(t, err, adaptations.ErrBookNotFound)

	// Подготовка: collection + archive + book (минимально, чтобы FK совпали).
	var collID, archID, bookID int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO collections (name, inpx_filename) VALUES ('test', 'test.inpx') RETURNING id
	`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO archives (collection_id, filename) VALUES ($1, 'a.zip') RETURNING id
	`, collID).Scan(&archID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title)
		VALUES ($1, $2, 'L1', 'f', 'fb2', 'Test Book', 'test book')
		RETURNING id
	`, collID, archID).Scan(&bookID))

	// Книга есть, enrichment ещё не запускался → status=pending, items=[].
	res, err := svc.List(ctx, bookID)
	require.NoError(t, err)
	require.Equal(t, "pending", res.EnrichmentStatus)
	require.Empty(t, res.Items)

	// Помечаем "обогащение завершилось", но без записей: status=done, items=[].
	_, err = pool.Exec(ctx, `UPDATE books SET adaptations_fetched_at = now() WHERE id = $1`, bookID)
	require.NoError(t, err)
	res, err = svc.List(ctx, bookID)
	require.NoError(t, err)
	require.Equal(t, "done", res.EnrichmentStatus)
	require.Empty(t, res.Items)

	// Вставляем три экранизации: одну с известным годом 1956, одну с
	// годом 1965, и одну без года. Должны вернуться в порядке
	// 1956 → 1965 → (без года в конце).
	_, err = pool.Exec(ctx, `
		INSERT INTO book_adaptations (book_id, provider, ext_id, title, year, director, kind, poster_path, ext_url)
		VALUES
		  ($1, 'wikidata', 'Q1', 'Adaptation 1965', 1965, 'Director B', 'film', NULL, 'https://wd/Q1'),
		  ($1, 'wikidata', 'Q2', 'Adaptation 1956', 1956, NULL, 'film', 'poster.jpg', NULL),
		  ($1, 'wikidata', 'Q3', 'Adaptation Unknown Year', NULL, 'Director X', 'tv_series', NULL, NULL)
	`, bookID)
	require.NoError(t, err)

	res, err = svc.List(ctx, bookID)
	require.NoError(t, err)
	require.Equal(t, "done", res.EnrichmentStatus)
	require.Len(t, res.Items, 3)

	require.Equal(t, "Q2", res.Items[0].ExtID)
	require.NotNil(t, res.Items[0].Year)
	require.Equal(t, 1956, *res.Items[0].Year)
	require.NotNil(t, res.Items[0].PosterPath)
	require.Equal(t, "poster.jpg", *res.Items[0].PosterPath)
	require.Empty(t, res.Items[0].Director) // NULL → пустая строка
	require.Empty(t, res.Items[0].ExtURL)

	require.Equal(t, "Q1", res.Items[1].ExtID)
	require.Equal(t, "Director B", res.Items[1].Director)
	require.Equal(t, "https://wd/Q1", res.Items[1].ExtURL)

	require.Equal(t, "Q3", res.Items[2].ExtID)
	require.Nil(t, res.Items[2].Year)
	require.Equal(t, "tv_series", res.Items[2].Kind)
}

func startPostgres(t *testing.T, ctx context.Context) *pgxpool.Pool {
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
