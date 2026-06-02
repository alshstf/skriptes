package db_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/db"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestMigrateAndPool поднимает реальный postgres через testcontainers,
// прогоняет миграции, проверяет что все ожидаемые таблицы созданы,
// и что повторный Migrate — идемпотентен.
func TestMigrateAndPool(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

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
	t.Cleanup(func() {
		// Не возвращаем ошибку из t.Cleanup — testcontainers сам логирует.
		_ = pgC.Terminate(context.Background())
	})

	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// Миграции — идемпотентны: прогоняем дважды.
	require.NoError(t, db.Migrate(dsn))
	require.NoError(t, db.Migrate(dsn))

	pool, err := db.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	require.NoError(t, pool.Ping(ctx))

	// Проверка: все ключевые таблицы на месте.
	expected := []string{
		"app_settings",
		"archives", "authors", "book_adaptations", "book_authors", "book_cover_lookups", "book_genres", "book_year_lookups", "books",
		"collections", "favorite_authors", "favorite_series", "favorites",
		"genres", "import_jobs", "kindle_targets", "metadata_cache",
		"reads", "series", "sessions", "user_settings", "users", "views",
	}
	rows, err := pool.Query(ctx, `
		SELECT tablename FROM pg_tables
		WHERE schemaname = 'public'
		  AND tablename NOT LIKE 'schema_migrations%'
	`)
	require.NoError(t, err)
	defer rows.Close()

	var got []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		got = append(got, name)
	}
	require.NoError(t, rows.Err())
	sort.Strings(got)
	require.Equal(t, expected, got)

	// Проверка: расширения подключены.
	for _, ext := range []string{"pg_trgm", "citext", "btree_gin"} {
		var exists bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = $1)`, ext,
		).Scan(&exists)
		require.NoError(t, err)
		require.True(t, exists, "extension %s must be enabled", ext)
	}
}
