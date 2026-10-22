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

// Профиль из одних негативов — не пустой: пересортировка нужна и тому, кто
// только заблокировал автора.
func TestPersonaProfile_NegativesOnlyNotEmpty(t *testing.T) {
	p := history.PersonaProfile{BlockedAuthors: map[int64]struct{}{1: {}}}
	require.False(t, p.IsEmpty())
	p = history.PersonaProfile{DroppedWorks: map[int64]struct{}{1: {}}}
	require.False(t, p.IsEmpty())
	require.True(t, history.PersonaProfile{}.IsEmpty())
}

// TestService_PersonaNegatives — негативы персоны поиска (план recsys/03, задача
// 3.5) грузятся по тем же определениям, что у модели близости: блок-лист
// (3.2), скрытия «не интересно» и без причины (не «уже читал»/«потом»), «бросил»
// по recsys.DefaultWeights (доля < 0.25, движение старше 180 дней, не дочитано
// ни в одном издании). Позитивная часть профиля от негативов не меняется.
func TestService_PersonaNegatives(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)

	var userID, collID, archID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ('neg@e.com','Neg','x','user') RETURNING id`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	var authorID, serID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO authors (last_name, first_name, normalized_name) VALUES ('Надоел','Автор','надоел автор') RETURNING id`).Scan(&authorID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO series (title, normalized_title) VALUES ('Цикл','цикл') RETURNING id`).Scan(&serID))

	mkWork := func(title string) int64 {
		var wid int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO works (title, normalized_title) VALUES ($1, lower($1)) RETURNING id`, title).Scan(&wid))
		return wid
	}
	mkEdition := func(lib string, wid int64) int64 {
		var bid int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id, date_added)
			VALUES ($1,$2,$3,$3,'fb2','Книга','книга',$4,'2024-06-01') RETURNING id`,
			collID, archID, lib, wid).Scan(&bid))
		_, err := pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id) VALUES ($1,$2)`, bid, authorID)
		require.NoError(t, err)
		return bid
	}
	read := func(bid int64, fraction float64, age time.Duration, completed bool) {
		var completedAt *time.Time
		if completed {
			at := time.Now().Add(-age)
			completedAt = &at
		}
		_, err := pool.Exec(ctx,
			`INSERT INTO reads (user_id, book_id, fraction, updated_at, completed_at) VALUES ($1,$2,$3, now() - $4::interval, $5)`,
			userID, bid, fraction, age.String(), completedAt)
		require.NoError(t, err)
	}
	dismiss := func(wid int64, reason *string) {
		_, err := pool.Exec(ctx, `INSERT INTO feed_dismissals (user_id, work_id, reason) VALUES ($1,$2,$3)`, userID, wid, reason)
		require.NoError(t, err)
	}
	str := func(s string) *string { return &s }
	old := recsys.DefaultWeights.DroppedMinAge + 24*time.Hour
	fresh := 10 * 24 * time.Hour

	svc := history.New(pool)

	// Пусто — ничего не грузится.
	p, err := svc.PersonaProfile(ctx, userID)
	require.NoError(t, err)
	require.True(t, p.IsEmpty())

	// Блок-лист.
	require.NoError(t, svc.BlockAuthor(ctx, userID, authorID))
	require.NoError(t, svc.BlockSeries(ctx, userID, serID))

	// Скрытия: «не интересно», без причины — негатив; «уже читал», «потом» — нет.
	wNotInterested, wNoReason, wAlreadyRead, wLater := mkWork("Н1"), mkWork("Н2"), mkWork("Н3"), mkWork("Н4")
	dismiss(wNotInterested, str(string(recsys.DismissNotInterested)))
	dismiss(wNoReason, nil)
	dismiss(wAlreadyRead, str(string(recsys.DismissAlreadyRead)))
	dismiss(wLater, str(string(recsys.DismissLater)))

	// «Бросил»: доля 0.1 и давно — да; недавно — нет; доля 0.8 и давно — нет;
	// дочитано (в любом издании работы) — нет.
	wDropped, wRecent, wFar, wDone, wMixed := mkWork("Б1"), mkWork("Б2"), mkWork("Б3"), mkWork("Б4"), mkWork("Б5")
	read(mkEdition("L1", wDropped), 0.1, old, false)
	read(mkEdition("L2", wRecent), 0.1, fresh, false)
	read(mkEdition("L3", wFar), 0.8, old, false)
	read(mkEdition("L4", wDone), 0.1, old, true)
	read(mkEdition("L5a", wMixed), 0.1, old, false)
	read(mkEdition("L5b", wMixed), 0.9, old, true)

	p, err = svc.PersonaProfile(ctx, userID)
	require.NoError(t, err)
	require.False(t, p.IsEmpty())
	require.Equal(t, map[int64]struct{}{authorID: {}}, p.BlockedAuthors)
	require.Equal(t, map[int64]struct{}{serID: {}}, p.BlockedSeries)
	require.Equal(t, map[int64]struct{}{wNotInterested: {}, wNoReason: {}}, p.NotInterestedWorks)
	require.Equal(t, map[int64]struct{}{wDropped: {}}, p.DroppedWorks)

	// Позитивная часть: чтение даёт активность как раньше (6 изданий × 3).
	require.Len(t, p.BookActivity, 6)
	require.InDelta(t, 18.0, p.AuthorActivity[authorID], 1e-9)
	require.Empty(t, p.FavoriteAuthors)

	// Отмена блока — негатив уходит.
	require.NoError(t, svc.UnblockAuthor(ctx, userID, authorID))
	p, err = svc.PersonaProfile(ctx, userID)
	require.NoError(t, err)
	require.Empty(t, p.BlockedAuthors)
	require.Len(t, p.BlockedSeries, 1)
}
