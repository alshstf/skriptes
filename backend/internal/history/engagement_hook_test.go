package history_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/history"
	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/stretchr/testify/require"
)

// Контракт engagement-хука: КАЖДЫЙ метод, пишущий в views/reads, обязан звать
// mark — иначе PopularityTracker не узнаёт об изменении и популярность работы
// в works-индексе протухает до следующего полного ресинка. Именно так чтение
// в веб-ридере (SavePosition) и отметка «прочитано» (MarkRead) не двигали
// популярность (прод-аудит 2026-07). Добавляешь новый метод-писатель — включи
// его сюда.
func TestEngagementHook_CoversAllWriters(t *testing.T) {
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

	var userID int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO users (email, display_name, password_hash, role)
		VALUES ('hook@example.com', 'Hook', 'x', 'user')
		RETURNING id`).Scan(&userID))
	var bookID int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id FROM books WHERE deleted = false ORDER BY id LIMIT 1`,
	).Scan(&bookID))

	svc := history.New(pool)
	var marked []int64
	svc.SetEngagementHook(func(id int64) { marked = append(marked, id) })

	require.NoError(t, svc.RecordView(ctx, userID, bookID))
	require.NoError(t, svc.RecordAcquisition(ctx, userID, bookID))
	require.NoError(t, svc.MarkRead(ctx, userID, bookID))
	fr := 0.42
	require.NoError(t, svc.SavePosition(ctx, userID, bookID, "epubcfi(/6/2)", &fr))

	require.Len(t, marked, 4, "каждый писатель в views/reads должен дёрнуть хук")
	for _, id := range marked {
		require.Equal(t, bookID, id)
	}
}
