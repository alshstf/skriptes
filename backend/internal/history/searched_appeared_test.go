package history_test

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/history"
	"github.com/skriptes/skriptes/backend/internal/recsys"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestService_SearchedAppeared — секция «появилось то, что вы искали»: карточка с
// причиной и текстом запроса; скрытая из ленты и открытая после совпадения —
// уходят; скрытая на инстансе — не показывается.
func TestService_SearchedAppeared(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)

	var userID, collID, archID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ('sa@e.com','SA','x','user') RETURNING id`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	mk := func(lib, title string) (int64, int64) {
		var wid, bid int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO works (title, normalized_title) VALUES ($1, lower($1)) RETURNING id`, title).Scan(&wid))
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id)
			VALUES ($1,$2,$3,$3,'fb2',$4, lower($4), $5) RETURNING id`, collID, archID, lib, title, wid).Scan(&bid))
		return wid, bid
	}
	w1, _ := mk("L1", "Пикник на обочине")
	w2, b2 := mk("L2", "Улитка на склоне")
	w3, _ := mk("L3", "Град обреченный")
	searched := time.Now().Add(-10 * 24 * time.Hour)
	for _, w := range []int64{w1, w2, w3} {
		_, err := pool.Exec(ctx, `INSERT INTO search_matches (user_id, work_id, q, searched_at) VALUES ($1,$2,'стругацкие',$3)`, userID, w, searched)
		require.NoError(t, err)
	}
	svc := history.New(pool)
	items, err := svc.SearchedAppeared(ctx, userID, 10)
	require.NoError(t, err)
	require.Len(t, items, 3)
	r, ok := items[0].Reason(recsys.ReasonSearchedAppeared)
	require.True(t, ok)
	require.Equal(t, "стругацкие", r.Query)
	require.WithinDuration(t, searched, *r.At, time.Second)

	// Скрыл из ленты w1; открыл карточку w2 после совпадения; w3 скрыт на инстансе.
	require.NoError(t, svc.DismissFeedItem(ctx, userID, w1, recsys.DismissLater))
	require.NoError(t, svc.RecordView(ctx, userID, b2))
	_, err = pool.Exec(ctx, `UPDATE works SET instance_hidden = true WHERE id = $1`, w3)
	require.NoError(t, err)
	items, err = svc.SearchedAppeared(ctx, userID, 10)
	require.NoError(t, err)
	require.Empty(t, items)
}
