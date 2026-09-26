// Package testpg поднимает одноразовый Postgres (testcontainers) для
// интеграционных тестов: контейнер на тест, миграции применены, пул и контейнер
// закрываются через t.Cleanup.
package testpg

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/db"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// Pool — Postgres с применёнными миграциями.
func Pool(t testing.TB, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	pool, _ := Start(t, ctx)
	return pool
}

// Start — то же, что Pool, плюс DSN (для кода, который сам открывает соединения).
func Start(t testing.TB, ctx context.Context) (*pgxpool.Pool, string) {
	t.Helper()
	dsn := DSN(t, ctx)
	require.NoError(t, db.Migrate(dsn))
	pool, err := db.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool, dsn
}

// DSN — только контейнер, без миграций (тесты самих миграций).
func DSN(t testing.TB, ctx context.Context) string {
	t.Helper()
	pgC, err := postgres.Run(ctx,
		"postgres:17-alpine",
		postgres.WithDatabase("skriptes_test"),
		postgres.WithUsername("skriptes"),
		postgres.WithPassword("skriptes"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				// Дважды: после init-скриптов образ перезапускает сервер.
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
				// Порт ждём отдельно: go test гоняет пакеты параллельно, у каждого свой
				// контейнер, и под нагрузкой Docker публикует порт позже, чем PG пишет
				// лог. Без этого ConnectionString падал «port "5432/tcp" not found».
				wait.ForListeningPort("5432/tcp"),
			).WithStartupTimeoutDefault(60*time.Second),
		),
	)
	// До проверки ошибки: контейнер мог создаться, даже если ожидание упало.
	testcontainers.CleanupContainer(t, pgC)
	require.NoError(t, err)

	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	return dsn
}
