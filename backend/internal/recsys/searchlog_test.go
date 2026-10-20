package recsys_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/recsys"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestNormalizeQuery(t *testing.T) {
	require.Equal(t, "пикник на обочине", recsys.NormalizeQuery("  Пикник   на обочине! "))
	require.Equal(t, "ежик в тумане", recsys.NormalizeQuery("Ёжик в тумане"))
	require.Equal(t, "1984", recsys.NormalizeQuery("«1984»"))
	require.Equal(t, "", recsys.NormalizeQuery("..."))
}

// fakeSearcher — «совпадение», если запрос равен названию работы из таблицы.
type fakeSearcher struct {
	titles map[int64]string
	calls  int
}

func (f *fakeSearcher) SearchWorkIDs(_ context.Context, q string, within []int64) ([]int64, error) {
	f.calls++
	var out []int64
	for _, id := range within {
		if recsys.NormalizeQuery(f.titles[id]) == recsys.NormalizeQuery(q) {
			out = append(out, id)
		}
	}
	return out, nil
}

func seedUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email string) int64 {
	var id int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ($1,'U','x','user') RETURNING id`, email).Scan(&id))
	return id
}

// TestSearchLog — запись со схлопыванием набора, клик, история, удаление,
// срок хранения, агрегат ненайденного, сопоставление с новыми работами.
func TestSearchLog(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	me, other := seedUser(t, ctx, pool, "sl@e.com"), seedUser(t, ctx, pool, "sl2@e.com")
	log := recsys.NewSearchLog(pool, 365)

	// Набор «пикн» → «пикник» → «пикник на обочине» — одна строка (итог).
	id1, err := log.Record(ctx, me, "пикн", nil, 0)
	require.NoError(t, err)
	require.NotZero(t, id1)
	id2, err := log.Record(ctx, me, "пикник", nil, 0)
	require.NoError(t, err)
	require.Equal(t, id1, id2, "продолжение набора обновляет строку")
	id3, err := log.Record(ctx, me, "Пикник на обочине", map[string]any{"lang": "ru"}, 0)
	require.NoError(t, err)
	require.Equal(t, id1, id3)
	// Откат (стёр до «пикник») — тоже та же строка; новый запрос — новая.
	id4, err := log.Record(ctx, me, "пикник", nil, 0)
	require.NoError(t, err)
	require.Equal(t, id1, id4)
	idB, err := log.Record(ctx, me, "Мастер и Маргарита", nil, 12)
	require.NoError(t, err)
	require.NotEqual(t, id1, idB)
	// Короткое — не пишем.
	id0, err := log.Record(ctx, me, "ab", nil, 3)
	require.NoError(t, err)
	require.Zero(t, id0)

	// Клик по чужой строке — no-op; по своей — записан.
	var work int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO works (title, normalized_title) VALUES ('Мастер и Маргарита','мастер и маргарита') RETURNING id`).Scan(&work))
	require.NoError(t, log.Click(ctx, other, idB, work))
	require.NoError(t, log.Click(ctx, me, idB, work))
	items, total, err := log.List(ctx, me, 50, 0)
	require.NoError(t, err)
	require.Equal(t, 2, total)
	require.Len(t, items, 2)
	require.Equal(t, "Мастер и Маргарита", items[0].Q, "новые сверху")
	require.Equal(t, work, items[0].ClickedWork)
	require.Equal(t, "Мастер и Маргарита", items[0].ClickedTitle)
	require.Equal(t, 12, items[0].Hits)
	require.Equal(t, "пикник", items[1].Q)
	require.Equal(t, 0, items[1].Hits)
	_, otherTotal, err := log.List(ctx, other, 50, 0)
	require.NoError(t, err)
	require.Zero(t, otherTotal, "чужую историю не видно")

	// Агрегат ненайденного: «пикник» (me) + «пикник» (other) = 2 раза, 2 пользователя.
	_, err = pool.Exec(ctx, `UPDATE search_log SET at = at - interval '2 minutes'`) // вне окна схлопывания
	require.NoError(t, err)
	_, err = log.Record(ctx, other, "ПИКНИК", nil, 0)
	require.NoError(t, err)
	misses, err := log.Misses(ctx, 90, 10)
	require.NoError(t, err)
	require.Len(t, misses, 1)
	require.Equal(t, recsys.Miss{Q: "ПИКНИК", Count: 2, Users: 2}, misses[0])

	// Сопоставление: новая работа «Пикник» — обоим, «Чужая» — никому.
	var wPicnic, wOther int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO works (title, normalized_title) VALUES ('Пикник','пикник') RETURNING id`).Scan(&wPicnic))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO works (title, normalized_title) VALUES ('Чужая','чужая') RETURNING id`).Scan(&wOther))
	fs := &fakeSearcher{titles: map[int64]string{wPicnic: "Пикник", wOther: "Чужая"}}
	m := recsys.NewMatcher(pool, fs, log)
	n, err := m.MatchNewWorks(ctx, []int64{wPicnic, wOther})
	require.NoError(t, err)
	require.Equal(t, 2, n)
	require.Equal(t, 2, fs.calls, "по запросу на пользователя")
	n, err = m.MatchNewWorks(ctx, []int64{wPicnic, wOther})
	require.NoError(t, err)
	require.Zero(t, n, "повтор — no-op")
	var mq string
	require.NoError(t, pool.QueryRow(ctx, `SELECT q FROM search_matches WHERE user_id=$1 AND work_id=$2`, me, wPicnic).Scan(&mq))
	require.Equal(t, "пикник", mq)

	// Удаление строки и всей истории (вместе с «появилось»).
	require.NoError(t, log.Delete(ctx, other, idB)) // чужая — no-op
	require.NoError(t, log.Delete(ctx, me, idB))
	_, total, err = log.List(ctx, me, 50, 0)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.NoError(t, log.DeleteAll(ctx, me))
	_, total, err = log.List(ctx, me, 50, 0)
	require.NoError(t, err)
	require.Zero(t, total)
	var matches int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM search_matches WHERE user_id=$1`, me).Scan(&matches))
	require.Zero(t, matches)

	// Срок хранения: строка другого пользователя старше года — удаляется.
	_, err = pool.Exec(ctx, `UPDATE search_log SET at = now() - interval '400 days' WHERE user_id=$1`, other)
	require.NoError(t, err)
	purged, err := log.Purge(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, purged)

	// Выключенный лог — ничего не пишет.
	off := recsys.NewSearchLog(pool, 0)
	require.False(t, off.Enabled())
	id, err := off.Record(ctx, me, "что угодно", nil, 0)
	require.NoError(t, err)
	require.Zero(t, id)
}
