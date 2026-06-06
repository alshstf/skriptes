package importer_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	meili "github.com/meilisearch/meilisearch-go"
	"github.com/skriptes/skriptes/backend/internal/db"
	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcmeili "github.com/testcontainers/testcontainers-go/modules/meilisearch"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// fixtureINPX живёт в backend/internal/inpx/testdata/test.inpx.
// Тест на importer_test берёт её через относительный путь, чтобы не дублировать.
const fixtureINPX = "../inpx/testdata/test.inpx"

// TestRun_FullPipeline_OnFixture поднимает реальный postgres + meilisearch
// через testcontainers, прогоняет импорт на тестовом INPX (20 записей)
// и проверяет состояние и БД, и Meili.
func TestRun_FullPipeline_OnFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool, dsn := startPostgres(t, ctx)
	mgr := startMeilisearch(t, ctx)

	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr})

	abs, err := filepath.Abs(fixtureINPX)
	require.NoError(t, err)

	stats, err := imp.Run(ctx, abs)
	require.NoError(t, err)

	require.False(t, stats.Skipped)
	require.Equal(t, 20, stats.Records)
	require.Equal(t, 20, stats.BooksInserted, "первый запуск должен создать всё впервые")
	require.Equal(t, 0, stats.BooksUpdated)
	require.Equal(t, 0, stats.Errors)
	require.Greater(t, stats.Authors, 0)
	require.Equal(t, stats.BooksInserted-stats.BooksDeleted, stats.BooksIndexed,
		"в Meili идут все недёлёнки")

	// ── проверки PostgreSQL ──────────────────────────────────────
	var bookCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM books`).Scan(&bookCount))
	require.Equal(t, 20, bookCount)

	var collectionCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM collections`).Scan(&collectionCount))
	require.Equal(t, 1, collectionCount)

	var archiveCount int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM archives`).Scan(&archiveCount))
	require.Equal(t, 4, archiveCount, "4 .inp → 4 archives")

	// Конкретная книга (LIBID=749080, Алексеев)
	var (
		title             string
		seriesTitle       *string
		serNo             *int
		hasAuthorAlekseev bool
	)
	row := pool.QueryRow(ctx, `
		SELECT b.title, s.title, b.ser_no,
		       EXISTS (
		           SELECT 1
		           FROM book_authors ba
		           JOIN authors a ON a.id = ba.author_id
		           WHERE ba.book_id = b.id
		             AND a.normalized_name = 'алексеев евгений артёмович'
		       )
		FROM books b
		LEFT JOIN series s ON s.id = b.series_id
		WHERE b.lib_id = '749080'
	`)
	require.NoError(t, row.Scan(&title, &seriesTitle, &serNo, &hasAuthorAlekseev))
	require.Equal(t, "Кадетский корпус. Книга 2", title)
	require.NotNil(t, seriesTitle)
	require.Equal(t, "Петля [Алексеев]", *seriesTitle)
	require.NotNil(t, serNo)
	require.Equal(t, 2, *serNo)
	require.True(t, hasAuthorAlekseev, "автор Алексеев должен быть привязан к книге")

	// 3 жанра у этой книги
	var genreCount int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*)
		FROM book_genres bg
		JOIN books b ON b.id = bg.book_id
		WHERE b.lib_id = '749080'
	`).Scan(&genreCount))
	require.Equal(t, 3, genreCount)

	// ── проверки works (Phase 1: singleton-работа на каждую книгу) ──
	// Группировки изданий ещё нет → works == books, по 1 изданию на работу,
	// инвариант work_id != NULL.
	var worksCount, booksWithWork, maxEditionsPerWork int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM works`).Scan(&worksCount))
	require.Equal(t, 20, worksCount, "каждая книга получает свою singleton-работу")
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM books WHERE work_id IS NOT NULL`).Scan(&booksWithWork))
	require.Equal(t, 20, booksWithWork, "инвариант: у каждой книги есть work_id")
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT COALESCE(max(c), 0) FROM (
			SELECT count(*) c FROM books WHERE work_id IS NOT NULL GROUP BY work_id
		) t
	`).Scan(&maxEditionsPerWork))
	require.Equal(t, 1, maxEditionsPerWork, "пока по одному изданию на работу")

	// Work поднял Work-level поля книги (серия/ser_no/primary-автор).
	var wSeriesOK, wHasPrimaryAuthor bool
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT (w.series_id IS NOT DISTINCT FROM b.series_id) AND (w.ser_no IS NOT DISTINCT FROM b.ser_no),
		       (w.primary_author_id IS NOT NULL)
		FROM books b JOIN works w ON w.id = b.work_id
		WHERE b.lib_id = '749080'
	`).Scan(&wSeriesOK, &wHasPrimaryAuthor))
	require.True(t, wSeriesOK, "work поднял series_id/ser_no книги")
	require.True(t, wHasPrimaryAuthor, "work.primary_author_id заполнен из book_authors")

	// ── проверки Meilisearch ─────────────────────────────────────
	// Indexer ждёт завершения task'а Meili в самом flush — поэтому здесь
	// проверка детерминированная, без Eventually.
	mst, err := mgr.Index("books").GetStatsWithContext(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(stats.BooksIndexed), mst.NumberOfDocuments,
		"Meili должен содержать ровно столько же документов сколько отправил importer")

	// Поиск по названию.
	res, err := mgr.Index("books").SearchWithContext(ctx, "Кадетский", &meili.SearchRequest{Limit: 5})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(res.Hits), 1)

	// ── идемпотентность: повторный Run на том же файле — Skipped ─
	stats2, err := imp.Run(ctx, abs)
	require.NoError(t, err)
	require.True(t, stats2.Skipped, "повторный импорт того же файла должен быть пропущен по хэшу")

	// Проверяем: число строк не изменилось.
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM books`).Scan(&bookCount))
	require.Equal(t, 20, bookCount)

	_ = dsn // dsn используется внутри startPostgres, оставлено для отладки
}

// TestResyncYears_PushesWrittenYearToMeili — после импорта Meili-поле year
// пустое (берётся из written_year, а он наполняется обогащением позже).
// Проставляем written_year двум книгам, синкаем и проверяем, что фильтр по
// year в Meili работает по году написания.
func TestResyncYears_PushesWrittenYearToMeili(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool, _ := startPostgres(t, ctx)
	mgr := startMeilisearch(t, ctx)
	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr})

	abs, err := filepath.Abs(fixtureINPX)
	require.NoError(t, err)
	_, err = imp.Run(ctx, abs)
	require.NoError(t, err)

	// Сразу после импорта год не выставлен → фильтр по году пуст.
	res0, err := mgr.Index("books").SearchWithContext(ctx, "", &meili.SearchRequest{Filter: "year >= 1000", Limit: 50})
	require.NoError(t, err)
	require.Len(t, res0.Hits, 0, "после импорта year пуст (written_year ещё NULL)")

	// Проставляем written_year двум разным книгам.
	var id1, id2 int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM books WHERE lib_id='749080'`).Scan(&id1))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id FROM books WHERE deleted=false AND lib_id<>'749080' ORDER BY id LIMIT 1`).Scan(&id2))
	_, err = pool.Exec(ctx, `UPDATE books SET written_year=1990 WHERE id=$1`, id1)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE books SET written_year=2010 WHERE id=$1`, id2)
	require.NoError(t, err)

	var live int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM books WHERE deleted=false`).Scan(&live))
	n, err := imp.ResyncYears(ctx)
	require.NoError(t, err)
	require.Equal(t, live, n, "синкаются все живые книги")

	// Теперь фильтр по году отражает written_year.
	resAll, err := mgr.Index("books").SearchWithContext(ctx, "", &meili.SearchRequest{Filter: "year >= 1000", Limit: 50})
	require.NoError(t, err)
	require.Len(t, resAll.Hits, 2, "год известен ровно у двух книг")

	res2000, err := mgr.Index("books").SearchWithContext(ctx, "", &meili.SearchRequest{Filter: "year >= 2000", Limit: 50})
	require.NoError(t, err)
	require.Len(t, res2000.Hits, 1, "год >= 2000 — только книга 2010")
}

// ── helpers ────────────────────────────────────────────────────

func startPostgres(t *testing.T, ctx context.Context) (*pgxpool.Pool, string) {
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

	return pool, dsn
}

func startMeilisearch(t *testing.T, ctx context.Context) meili.ServiceManager {
	t.Helper()
	const masterKey = "test-master-key-1234567890" // длиной ≥16 как требует Meili
	mC, err := tcmeili.Run(ctx, "getmeili/meilisearch:v1.13", tcmeili.WithMasterKey(masterKey))
	require.NoError(t, err)
	t.Cleanup(func() { _ = mC.Terminate(context.Background()) })

	addr, err := mC.Address(ctx)
	require.NoError(t, err)
	return meili.New(addr, meili.WithAPIKey(masterKey))
}
