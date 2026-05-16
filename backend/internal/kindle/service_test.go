package kindle_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/db"
	"github.com/skriptes/skriptes/backend/internal/kindle"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestKindleService — полный CRUD-цикл на свежей PG-инстанс через
// testcontainers. Проверяет также UNIQUE (user_id, email) и обработку
// ErrInvalidEmail / ErrNotFound.
func TestKindleService_CRUD(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := startPostgres(t, ctx)
	svc := kindle.New(pool)

	// seed users (kindle_targets ON DELETE CASCADE)
	var userA, userB int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO users (email, display_name, password_hash, role)
		VALUES ('a@example.com', 'A', 'x', 'user') RETURNING id
	`).Scan(&userA))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO users (email, display_name, password_hash, role)
		VALUES ('b@example.com', 'B', 'x', 'user') RETURNING id
	`).Scan(&userB))

	// Add: happy path
	t1, err := svc.Add(ctx, userA, "My Kindle", "a-personal@kindle.com")
	require.NoError(t, err)
	require.NotZero(t, t1.ID)
	require.Equal(t, "My Kindle", t1.Label)

	// Add: invalid email
	_, err = svc.Add(ctx, userA, "Bad", "no-at-sign")
	require.ErrorIs(t, err, kindle.ErrInvalidEmail)

	// Add: empty label → default "Kindle"
	t2, err := svc.Add(ctx, userA, "  ", "spouse@kindle.com")
	require.NoError(t, err)
	require.Equal(t, "Kindle", t2.Label)

	// Add: duplicate email для same user
	_, err = svc.Add(ctx, userA, "Dup", "a-personal@kindle.com")
	require.ErrorIs(t, err, kindle.ErrDuplicate)

	// Same email допустим для другого user
	_, err = svc.Add(ctx, userB, "B's K", "a-personal@kindle.com")
	require.NoError(t, err)

	// List
	listA, err := svc.List(ctx, userA)
	require.NoError(t, err)
	require.Len(t, listA, 2)
	listB, err := svc.List(ctx, userB)
	require.NoError(t, err)
	require.Len(t, listB, 1)

	// Get: existing
	gotA1, err := svc.Get(ctx, userA, t1.ID)
	require.NoError(t, err)
	require.Equal(t, t1.Email, gotA1.Email)

	// Get: чужой target → NotFound
	_, err = svc.Get(ctx, userB, t1.ID)
	require.ErrorIs(t, err, kindle.ErrNotFound)

	// Update label/email
	updated, err := svc.Update(ctx, userA, t1.ID, "Renamed", "a-new@kindle.com")
	require.NoError(t, err)
	require.Equal(t, "Renamed", updated.Label)
	require.Equal(t, "a-new@kindle.com", updated.Email)

	// Update на чужой target — NotFound
	_, err = svc.Update(ctx, userB, t1.ID, "x", "anything@kindle.com")
	require.ErrorIs(t, err, kindle.ErrNotFound)

	// Update на занятый второй email → Duplicate
	_, err = svc.Update(ctx, userA, t1.ID, "x", "spouse@kindle.com")
	require.ErrorIs(t, err, kindle.ErrDuplicate)

	// Delete: existing
	require.NoError(t, svc.Delete(ctx, userA, t1.ID))
	// Delete: чужой → NotFound
	require.ErrorIs(t, svc.Delete(ctx, userB, t1.ID), kindle.ErrNotFound)

	// Финальный список
	listA, err = svc.List(ctx, userA)
	require.NoError(t, err)
	require.Len(t, listA, 1)
	require.Equal(t, "spouse@kindle.com", listA[0].Email)
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
