package importer_test

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	meili "github.com/meilisearch/meilisearch-go"
	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/stretchr/testify/require"
)

// seedEngagedBook — общий сетап: импорт фикстуры + пользователь + первая живая
// книга с её работой. Возвращает pool не нужен — вся запись через переданные
// хелперы вызывающего.
func popularitySetup(t *testing.T, ctx context.Context) (imp *importer.Importer, mgr meili.ServiceManager, userID, bookID, workID int64, execSQL func(sql string, args ...any)) {
	t.Helper()
	pool, _ := startPostgres(t, ctx)
	mgr = startMeilisearch(t, ctx)
	imp = importer.New(importer.Deps{Pool: pool, Meili: mgr})
	abs, err := filepath.Abs(fixtureINPX)
	require.NoError(t, err)
	_, err = imp.Run(ctx, abs)
	require.NoError(t, err)

	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO users (email, display_name, password_hash, role)
		VALUES ('pop@example.com', 'Pop', 'x', 'user')
		RETURNING id`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id, work_id FROM books WHERE deleted = false ORDER BY id LIMIT 1`,
	).Scan(&bookID, &workID))

	execSQL = func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, sql, args...)
		require.NoError(t, err)
	}
	return imp, mgr, userID, bookID, workID, execSQL
}

// worksDocPopularity читает поле popularity дока работы прямо из works-индекса.
func worksDocPopularity(ctx context.Context, mgr meili.ServiceManager, workID int64) (int64, error) {
	var doc struct {
		Popularity int64 `json:"popularity"`
	}
	err := mgr.Index("works").GetDocumentWithContext(ctx, strconv.FormatInt(workID, 10),
		&meili.DocumentQuery{Fields: []string{"id", "popularity"}}, &doc)
	return doc.Popularity, err
}

// Полный ресинк обязан пересчитывать popularity из сигналов известности,
// включая живую вовлечённость — регресс на «мёртвый sort=popularity»:
// поле добавили в workDocSelect без бампа гейта, и на стабильном деплое
// ресинк не запускался, оставляя во всех доках 0. Проверка относительная
// (база после импорта + дельта вовлечённости): у книг фикстуры есть свои
// LIBRATE-сигналы, абсолют зависел бы от того, какая книга первая по id.
func TestResyncWorksIndex_PopularityFromEngagement(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	imp, mgr, userID, bookID, workID, execSQL := popularitySetup(t, ctx)

	popBefore, err := worksDocPopularity(ctx, mgr, workID)
	require.NoError(t, err)

	// 2 просмотра (·20) + 1 read (·60) + 1 оценка (·100) → +200 к базе работы
	// (веса — константы popW* в popularity.go; оценка заодно проверяет сигнал
	// book_ratings в workDocSelect).
	execSQL(`INSERT INTO views (user_id, book_id) VALUES ($1, $2), ($1, $2)`, userID, bookID)
	execSQL(`INSERT INTO reads (user_id, book_id, updated_at) VALUES ($1, $2, now())`, userID, bookID)
	execSQL(`INSERT INTO book_ratings (user_id, work_id, rating) VALUES ($1, $2, 5)`, userID, workID)

	n, err := imp.ResyncWorksIndex(ctx)
	require.NoError(t, err)
	require.Greater(t, n, 0)

	pop, err := worksDocPopularity(ctx, mgr, workID)
	require.NoError(t, err)
	require.EqualValues(t, popBefore+200, pop,
		"дельта вовлечённости = 2·view + read + оценка = 2·20 + 60 + 100")

	// И сортировка выносит эту работу наверх: +200 вовлечённости больше максимума
	// базовых сигналов фикстуры (синглтон с LIBRATE 5 → 160, без экранизаций).
	res, err := mgr.Index("works").SearchWithContext(ctx, "", &meili.SearchRequest{
		Limit: 1,
		Sort:  []string{"popularity:desc"},
	})
	require.NoError(t, err)
	require.NotEmpty(t, res.Hits)
	var top struct {
		ID int64 `json:"id"`
	}
	require.NoError(t, res.Hits[0].DecodeInto(&top))
	require.Equal(t, workID, top.ID)
}

// Живой трекер: MarkBook + Run-цикл доносят новую вовлечённость до works-индекса
// без полного ресинка (таргетный upsert работы тем же workDocSelect).
func TestPopularityTracker_FlushUpsertsWork(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	imp, mgr, userID, bookID, workID, execSQL := popularitySetup(t, ctx)

	popBefore, err := worksDocPopularity(ctx, mgr, workID)
	require.NoError(t, err)

	// Вовлечённость появилась ПОСЛЕ импорта → док держит популярность импорта.
	execSQL(`INSERT INTO views (user_id, book_id) VALUES ($1, $2)`, userID, bookID)
	pop, err := worksDocPopularity(ctx, mgr, workID)
	require.NoError(t, err)
	require.EqualValues(t, popBefore, pop, "до флаша трекера док держит популярность импорта")

	tr := importer.NewPopularityTracker(imp, nil)
	tctx, tcancel := context.WithCancel(ctx)
	defer tcancel()
	go tr.Run(tctx, 50*time.Millisecond)
	tr.MarkBook(bookID)

	require.Eventually(t, func() bool {
		p, err := worksDocPopularity(ctx, mgr, workID)
		return err == nil && p >= popBefore+20
	}, 20*time.Second, 200*time.Millisecond, "трекер должен доапсертить работу (+20 за просмотр)")
}
