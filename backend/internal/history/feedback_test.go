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

// TestService_Feedback — явная обратная связь (план recsys/03): причины низкой
// оценки живут с оценкой (3.1), «не предлагать» автора/цикл чистит новинки по
// подпискам (3.2), полка «Хочу прочитать» — служебная, уровня работы (3.3).
func TestService_Feedback(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)

	var userID, collID, archID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ('fb@e.com','FB','x','user') RETURNING id`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	var subAuthor, badAuthor int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO authors (last_name, first_name, normalized_name) VALUES ('Подписан','Автор','подписан автор') RETURNING id`).Scan(&subAuthor))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO authors (last_name, first_name, normalized_name) VALUES ('Надоел','Автор','надоел автор') RETURNING id`).Scan(&badAuthor))
	var serID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO series (title, normalized_title) VALUES ('Цикл','цикл') RETURNING id`).Scan(&serID))

	mkBook := func(lib, title string, authorID int64, series *int64) (int64, int64) {
		var wid, bid int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO works (title, normalized_title) VALUES ($1, lower($1)) RETURNING id`, title).Scan(&wid))
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id, date_added, series_id)
			VALUES ($1,$2,$3,$3,'fb2',$4, lower($4), $5, '2024-06-01', $6) RETURNING id`,
			collID, archID, lib, title, wid, series).Scan(&bid))
		_, err := pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id) VALUES ($1,$2)`, bid, authorID)
		require.NoError(t, err)
		return bid, wid
	}
	svc := history.New(pool)

	// ── 3.1 Причины низкой оценки ──
	b1, w1 := mkBook("L1", "Книга", subAuthor, nil)
	require.NoError(t, svc.SetRating(ctx, userID, w1, 2))
	require.ErrorIs(t, svc.SetRatingReasons(ctx, userID, w1, []recsys.RatingReason{"meh"}), history.ErrInvalidRatingReason)
	require.NoError(t, svc.SetRatingReasons(ctx, userID, w1,
		[]recsys.RatingReason{recsys.ReasonBoring, recsys.ReasonTooLong, recsys.ReasonBoring}))
	rs, err := svc.RatingReasons(ctx, userID, w1)
	require.NoError(t, err)
	require.ElementsMatch(t, []recsys.RatingReason{recsys.ReasonBoring, recsys.ReasonTooLong}, rs, "дубль схлопнут")
	// Замена набора: «скучно» ушло, «не моё» пришло.
	require.NoError(t, svc.SetRatingReasons(ctx, userID, w1, []recsys.RatingReason{recsys.ReasonTooLong, recsys.ReasonNotMine}))
	rs, err = svc.RatingReasons(ctx, userID, w1)
	require.NoError(t, err)
	require.ElementsMatch(t, []recsys.RatingReason{recsys.ReasonTooLong, recsys.ReasonNotMine}, rs)
	// Уточнение балла 2 → 3 причины оставляет, 3 → 4 — снимает.
	require.NoError(t, svc.SetRating(ctx, userID, w1, 3))
	rs, err = svc.RatingReasons(ctx, userID, w1)
	require.NoError(t, err)
	require.Len(t, rs, 2)
	require.NoError(t, svc.SetRating(ctx, userID, w1, 4))
	rs, err = svc.RatingReasons(ctx, userID, w1)
	require.NoError(t, err)
	require.Empty(t, rs)
	// Снятие оценки снимает причины.
	require.NoError(t, svc.SetRating(ctx, userID, w1, 1))
	require.NoError(t, svc.SetRatingReasons(ctx, userID, w1, []recsys.RatingReason{recsys.ReasonGloomy}))
	require.NoError(t, svc.RemoveRating(ctx, userID, w1))
	rs, err = svc.RatingReasons(ctx, userID, w1)
	require.NoError(t, err)
	require.Empty(t, rs)
	// Пустой набор — убрать все.
	require.NoError(t, svc.SetRatingReasons(ctx, userID, w1, []recsys.RatingReason{recsys.ReasonDated}))
	require.NoError(t, svc.SetRatingReasons(ctx, userID, w1, nil))
	rs, err = svc.RatingReasons(ctx, userID, w1)
	require.NoError(t, err)
	require.Empty(t, rs)

	// ── 3.3 «Хочу прочитать» ──
	toRead, err := svc.IsWorkToRead(ctx, userID, w1)
	require.NoError(t, err)
	require.False(t, toRead)
	require.NoError(t, svc.AddToRead(ctx, userID, b1))
	require.NoError(t, svc.AddToRead(ctx, userID, b1), "идемпотентно")
	toRead, err = svc.IsWorkToRead(ctx, userID, w1)
	require.NoError(t, err)
	require.True(t, toRead)
	var kind string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT c.kind FROM user_collection_books cb JOIN user_collections c ON c.id = cb.collection_id
		WHERE c.user_id = $1 AND cb.book_id = $2`, userID, b1).Scan(&kind))
	require.Equal(t, "to_read", kind)
	// Второе издание той же работы — снимается вместе с первым (уровень работы).
	var b1b int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id)
		VALUES ($1,$2,'L1b','L1b','fb2','Книга','книга',$3) RETURNING id`, collID, archID, w1).Scan(&b1b))
	require.NoError(t, svc.AddToRead(ctx, userID, b1b))
	require.NoError(t, svc.RemoveToRead(ctx, userID, b1))
	toRead, err = svc.IsWorkToRead(ctx, userID, w1)
	require.NoError(t, err)
	require.False(t, toRead)

	// ── 3.2 «Не предлагать»: новинки по подпискам ──
	_, wSub := mkBook("L2", "Новинка", subAuthor, nil)
	_, wBad := mkBook("L3", "Надоевшая", badAuthor, nil)
	_, wSer := mkBook("L4", "Из цикла", subAuthor, &serID)
	_, err = pool.Exec(ctx, `INSERT INTO favorite_authors (user_id, author_id, added_at) VALUES ($1,$2,'2023-01-01'), ($1,$3,'2023-01-01')`,
		userID, subAuthor, badAuthor)
	require.NoError(t, err)
	items, err := svc.SubscriptionFeed(ctx, userID, 20)
	require.NoError(t, err)
	ids := func(items []recsys.Item) []int64 {
		out := make([]int64, 0, len(items))
		for _, it := range items {
			out = append(out, it.WorkID)
		}
		return out
	}
	// «Книга» (w1) — тоже подписанного автора и добавлена после подписки.
	require.ElementsMatch(t, []int64{w1, wSub, wBad, wSer}, ids(items))

	require.NoError(t, svc.BlockAuthor(ctx, userID, badAuthor))
	require.NoError(t, svc.BlockAuthor(ctx, userID, badAuthor), "идемпотентно")
	require.NoError(t, svc.BlockSeries(ctx, userID, serID))
	blocked, err := svc.IsAuthorBlocked(ctx, userID, badAuthor)
	require.NoError(t, err)
	require.True(t, blocked)
	blocked, err = svc.IsSeriesBlocked(ctx, userID, serID)
	require.NoError(t, err)
	require.True(t, blocked)
	items, err = svc.SubscriptionFeed(ctx, userID, 20)
	require.NoError(t, err)
	require.ElementsMatch(t, []int64{w1, wSub}, ids(items), "надоевший автор и блокированный цикл не в новинках")

	list, err := svc.ListBlocklist(ctx, userID)
	require.NoError(t, err)
	require.Len(t, list.Authors, 1)
	require.Equal(t, badAuthor, list.Authors[0].ID)
	require.Equal(t, "Надоел Автор", list.Authors[0].FullName)
	require.Len(t, list.Series, 1)
	require.Equal(t, serID, list.Series[0].ID)
	require.Equal(t, "Цикл", list.Series[0].Title)

	require.NoError(t, svc.UnblockAuthor(ctx, userID, badAuthor))
	require.NoError(t, svc.UnblockSeries(ctx, userID, serID))
	items, err = svc.SubscriptionFeed(ctx, userID, 20)
	require.NoError(t, err)
	require.ElementsMatch(t, []int64{w1, wSub, wBad, wSer}, ids(items))
	list, err = svc.ListBlocklist(ctx, userID)
	require.NoError(t, err)
	require.Empty(t, list.Authors)
	require.Empty(t, list.Series)

	// 3.4: лог настроения — append-only, неизвестное — ошибка.
	require.ErrorIs(t, svc.LogMood(ctx, userID, recsys.Mood("sad")), history.ErrInvalidMood)
	require.NoError(t, svc.LogMood(ctx, userID, recsys.MoodFunny))
	require.NoError(t, svc.LogMood(ctx, userID, recsys.MoodFunny))
	var moods []string
	rows, err := pool.Query(ctx, `SELECT mood FROM session_mood WHERE user_id = $1 ORDER BY at, id`, userID)
	require.NoError(t, err)
	for rows.Next() {
		var m string
		require.NoError(t, rows.Scan(&m))
		moods = append(moods, m)
	}
	rows.Close()
	require.Equal(t, []string{"funny", "funny"}, moods)
}
