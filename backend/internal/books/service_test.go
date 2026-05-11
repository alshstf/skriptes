package books_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	meili "github.com/meilisearch/meilisearch-go"
	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/db"
	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcmeili "github.com/testcontainers/testcontainers-go/modules/meilisearch"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// fixtureINPX — наш постоянный 19-записный фикстура.
// Лежит в backend/internal/inpx/testdata/test.inpx.
const fixtureINPX = "../inpx/testdata/test.inpx"

// TestService_ListAndGet — поднимает PG + Meili через testcontainers,
// импортирует фикстуру через тот же importer что и прод, проверяет:
//   - List() с пустым query вернёт ровно столько, сколько в Meili (18, без DEL=1)
//   - List(q="Кадетский") вернёт хотя бы один hit
//   - Get(id) вернёт книгу LIBID=749080 со всеми связями (1 автор, 3 жанра, серия)
func TestService_ListAndGet(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := startPostgres(t, ctx)
	mgr := startMeilisearch(t, ctx)

	// Импортируем фикстуру в БД и Meili — теми же путями что прод.
	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr})
	abs, err := filepath.Abs(fixtureINPX)
	require.NoError(t, err)
	stats, err := imp.Run(ctx, abs)
	require.NoError(t, err)
	require.Equal(t, 19, stats.BooksInserted)

	svc := books.New(pool, mgr)

	// ── List без query: должно вернуться 18 (минус DEL=1)
	res, err := svc.List(ctx, books.ListParams{Limit: 50})
	require.NoError(t, err)
	require.Len(t, res.Items, stats.BooksIndexed)
	require.Equal(t, int64(stats.BooksIndexed), res.Total)
	require.Equal(t, 50, res.Limit)
	require.Equal(t, 0, res.Offset)
	for _, it := range res.Items {
		require.NotZero(t, it.ID)
		require.NotEmpty(t, it.Title)
	}

	// ── Search by title
	res, err = svc.List(ctx, books.ListParams{Query: "Кадетский", Limit: 5})
	require.NoError(t, err)
	require.NotEmpty(t, res.Items, "поиск по 'Кадетский' должен вернуть хотя бы одну книгу")
	require.Equal(t, "Кадетский", res.Query)
	hit := res.Items[0]
	require.Equal(t, "Кадетский корпус. Книга 2", hit.Title)

	// ── Get: детальная карточка LIBID=749080
	bookID := hit.ID
	book, err := svc.Get(ctx, bookID)
	require.NoError(t, err)
	require.Equal(t, "Кадетский корпус. Книга 2", book.Title)
	require.Equal(t, "749080", book.LibID)
	require.Equal(t, "fb2", book.Ext)
	require.Equal(t, "fb2-749080-749080.zip", book.Archive)
	require.Equal(t, int64(849047), book.SizeBytes)
	require.False(t, book.Deleted)

	require.NotNil(t, book.Series)
	require.Equal(t, "Петля [Алексеев]", book.Series.Title)
	require.NotNil(t, book.SerNo)
	require.Equal(t, 2, *book.SerNo)

	require.Len(t, book.Authors, 1)
	require.Equal(t, "Алексеев", book.Authors[0].LastName)
	require.Equal(t, "Алексеев Евгений Артёмович", book.Authors[0].FullName)

	require.Len(t, book.Genres, 3)
	codes := make([]string, 0, 3)
	for _, g := range book.Genres {
		codes = append(codes, g.Code)
	}
	require.ElementsMatch(t, []string{"network_literature", "popadanec", "sf_action"}, codes)

	// ── Get: несуществующий id → ErrNotFound
	_, err = svc.Get(ctx, 99999999)
	require.ErrorIs(t, err, books.ErrNotFound)

	// ── Suggest: typeahead с лимитом, по той же фикстуре.
	sugg, err := svc.Suggest(ctx, "Кадетский", 5)
	require.NoError(t, err)
	require.NotEmpty(t, sugg)
	require.Equal(t, "Кадетский корпус. Книга 2", sugg[0].Title)
	require.LessOrEqual(t, len(sugg), 5)

	// Пустой query → пустой срез без ошибки.
	empty, err := svc.Suggest(ctx, "  ", 5)
	require.NoError(t, err)
	require.Empty(t, empty)

	// ── Фильтр по жанру: только sf_action → должна вернуться Кадетский корпус
	//    (она единственная в фикстуре с этим жанром).
	res, err = svc.List(ctx, books.ListParams{Genres: []string{"sf_action"}, Limit: 50})
	require.NoError(t, err)
	require.NotEmpty(t, res.Items)
	for _, it := range res.Items {
		require.Contains(t, it.Genres, "sf_action", "при фильтре genres=sf_action все книги должны иметь этот жанр")
	}

	// ── Фильтр по языку: должны вернуться только русские.
	res, err = svc.List(ctx, books.ListParams{Lang: "ru", Limit: 50})
	require.NoError(t, err)
	for _, it := range res.Items {
		require.Equal(t, "ru", it.Lang)
	}

	// ── Сортировка по году убывающая: первая книга должна быть новейшей.
	res, err = svc.List(ctx, books.ListParams{Sort: "year_desc", Limit: 50})
	require.NoError(t, err)
	for i := 1; i < len(res.Items); i++ {
		// Пропускаем книги без year — Meili их кладёт в конец при desc.
		prev := res.Items[i-1].Year
		cur := res.Items[i].Year
		if prev != nil && cur != nil {
			require.GreaterOrEqual(t, *prev, *cur, "year_desc нарушен")
		}
	}

	// ── Facets: запросили genres и lang — должны получить распределения.
	res, err = svc.List(ctx, books.ListParams{Facets: []string{"genres", "lang"}, Limit: 50})
	require.NoError(t, err)
	require.NotNil(t, res.Facets)
	require.Contains(t, res.Facets, "genres")
	require.Contains(t, res.Facets, "lang")
	// Хотя бы один из жанров фикстуры должен присутствовать.
	require.NotEmpty(t, res.Facets["genres"])
	require.GreaterOrEqual(t, res.Facets["lang"]["ru"], int64(1))
}

// ── helpers ────────────────────────────────────────────────────

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
