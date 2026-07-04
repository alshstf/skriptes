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

// «Продолжить чтение» дедуплицируется по РАБОТЕ: прогресс на двух изданиях
// одной книги — одна строка (самое свежее издание), а не дубли (прод-аудит
// P2 #6: «Евангелие от Афрания» показывалось дважды — 13% и 0%).
func TestContinueReading_DedupesByWork(t *testing.T) {
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
		VALUES ('cr@example.com', 'CR', 'x', 'user') RETURNING id`).Scan(&userID))

	// Два издания одной работы + отдельная книга-контроль.
	var e1, workID, collID, archID int64
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT id, work_id, collection_id, archive_id FROM books
		WHERE deleted = false ORDER BY id LIMIT 1`).Scan(&e1, &workID, &collID, &archID))
	var e2 int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, lang, work_id)
		VALUES ($1, $2, 'CR2', 'cr2.fb2', 'fb2', 'Второе издание', 'второе издание', 'ru', $3)
		RETURNING id`, collID, archID, workID).Scan(&e2))
	var other int64
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT id FROM books WHERE deleted = false AND work_id <> $1 ORDER BY id LIMIT 1`, workID).Scan(&other))

	svc := history.New(pool)
	fr1, fr2, fr3 := 0.13, 0.42, 0.5
	require.NoError(t, svc.SavePosition(ctx, userID, e1, "epubcfi(/6/2)", &fr1))
	require.NoError(t, svc.SavePosition(ctx, userID, e2, "epubcfi(/6/4)", &fr2))
	require.NoError(t, svc.SavePosition(ctx, userID, other, "epubcfi(/6/6)", &fr3))
	// e1 читали давно, e2 — только что: в ленте должна остаться e2.
	_, err = pool.Exec(ctx,
		`UPDATE reads SET updated_at = now() - interval '1 hour' WHERE user_id=$1 AND book_id=$2`, userID, e1)
	require.NoError(t, err)

	items, err := svc.ContinueReading(ctx, userID, 10)
	require.NoError(t, err)
	require.Len(t, items, 2, "одна строка на работу + книга-контроль (без дублей изданий)")

	var forWork *history.ContinueItem
	for i := range items {
		if items[i].WorkID == workID {
			require.Nil(t, forWork, "работа встретилась дважды — дедуп не сработал")
			forWork = &items[i]
		}
	}
	require.NotNil(t, forWork)
	require.Equal(t, e2, forWork.ID, "представитель — самое свежее издание")
	require.InDelta(t, 0.42, forWork.Fraction, 1e-6) // fraction в PG — real (float4)
}
