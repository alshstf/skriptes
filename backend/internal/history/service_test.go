package history_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	meili "github.com/meilisearch/meilisearch-go"
	"github.com/skriptes/skriptes/backend/internal/db"
	"github.com/skriptes/skriptes/backend/internal/history"
	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcmeili "github.com/testcontainers/testcontainers-go/modules/meilisearch"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const fixtureINPX = "../inpx/testdata/test.inpx"

// TestService_HistoryFlow — реальный PG + Meili через testcontainers.
// Сценарий:
//  1. импортируем фикстуру → есть >=1 живой книга и хотя бы 1 пользователь;
//  2. создаём seed-пользователя (admin), вызываем все методы Service.
func TestService_HistoryFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := startPostgres(t, ctx)
	mgr := startMeilisearch(t, ctx)

	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr})
	abs, _ := filepath.Abs(fixtureINPX)
	_, err := imp.Run(ctx, abs)
	require.NoError(t, err)

	// seed user
	var userID int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO users (email, display_name, password_hash, role)
		VALUES ('test@example.com', 'Test User', 'x', 'user')
		RETURNING id
	`).Scan(&userID))

	// возьмём какую-нибудь живую книгу из фикстуры
	var bookID int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id FROM books WHERE deleted = false ORDER BY id LIMIT 1`,
	).Scan(&bookID))

	svc := history.New(pool)

	// view → recent должен показать ровно одну запись
	require.NoError(t, svc.RecordView(ctx, userID, bookID))
	recent, err := svc.RecentViews(ctx, userID, 10)
	require.NoError(t, err)
	require.Len(t, recent, 1)
	require.Equal(t, bookID, recent[0].ID)

	// read (upsert): два вызова не должны добавить строки
	require.NoError(t, svc.RecordRead(ctx, userID, bookID))
	require.NoError(t, svc.RecordRead(ctx, userID, bookID))
	var readsCount int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM reads WHERE user_id = $1 AND book_id = $2`,
		userID, bookID,
	).Scan(&readsCount))
	require.Equal(t, 1, readsCount)

	// favorites: add — повторный no-op — IsFavorite=true — List вернёт книгу
	require.NoError(t, svc.AddFavorite(ctx, userID, bookID))
	require.NoError(t, svc.AddFavorite(ctx, userID, bookID))
	fav, err := svc.IsFavorite(ctx, userID, bookID)
	require.NoError(t, err)
	require.True(t, fav)

	list, err := svc.ListFavorites(ctx, userID, 50, 0)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, bookID, list[0].ID)

	cnt, err := svc.FavoritesCount(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, 1, cnt)

	// remove → idempotent
	require.NoError(t, svc.RemoveFavorite(ctx, userID, bookID))
	require.NoError(t, svc.RemoveFavorite(ctx, userID, bookID))
	fav, err = svc.IsFavorite(ctx, userID, bookID)
	require.NoError(t, err)
	require.False(t, fav)
	cnt, err = svc.FavoritesCount(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, 0, cnt)
}

// ── helpers (повтор из других пакетов) ─────────────────────────

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

func startMeilisearch(t *testing.T, ctx context.Context) meili.ServiceManager {
	t.Helper()
	const masterKey = "test-master-key-1234567890"
	mC, err := tcmeili.Run(ctx, "getmeili/meilisearch:v1.13", tcmeili.WithMasterKey(masterKey))
	require.NoError(t, err)
	t.Cleanup(func() { _ = mC.Terminate(context.Background()) })
	addr, err := mC.Address(ctx)
	require.NoError(t, err)
	return meili.New(addr, meili.WithAPIKey(masterKey))
}
