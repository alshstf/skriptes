package settings_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/db"
	"github.com/skriptes/skriptes/backend/internal/settings"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestSettings_CoverRoundTrip — пустая БД → дефолты; после SetCover →
// сохранённые значения; повторный SetCover перезаписывает (upsert).
func TestSettings_CoverRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startSettingsPG(t, ctx)
	store := settings.New(pool)

	// Нет оверрайда → дефолты.
	got, err := store.Cover(ctx)
	require.NoError(t, err)
	require.Equal(t, settings.DefaultCoverConfig(), got)

	// Сохранили — читается обратно.
	want := settings.CoverConfig{CacheMaxMB: 4096, CacheMinFreeMB: 512, Prewarm: true}
	require.NoError(t, store.SetCover(ctx, want))
	got, err = store.Cover(ctx)
	require.NoError(t, err)
	require.Equal(t, want, got)

	// Upsert — перезапись.
	want2 := settings.CoverConfig{CacheMaxMB: 16384, CacheMinFreeMB: 2048, Prewarm: false}
	require.NoError(t, store.SetCover(ctx, want2))
	got, err = store.Cover(ctx)
	require.NoError(t, err)
	require.Equal(t, want2, got)
}

func startSettingsPG(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	pgC, err := postgres.Run(ctx,
		"postgres:17-alpine",
		postgres.WithDatabase("skriptes_test"),
		postgres.WithUsername("skriptes"),
		postgres.WithPassword("skriptes"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
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
