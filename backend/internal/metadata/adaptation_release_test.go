package metadata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestPickReleaseDate(t *testing.T) {
	// «Скотный двор» Серкиса: фестиваль 2025, прокат 2026 — год в базе 2026.
	animalFarm := []releaseValue{
		{on: day(2025, 6, 9), precision: 11},
		{on: day(2025, 10, 4), precision: 11},
		{on: day(2026, 7, 24), precision: 11},
		{on: day(2026, 5, 1), precision: 11},
	}
	d, ok := pickReleaseDate(animalFarm, 2026)
	require.True(t, ok)
	require.Equal(t, ReleaseDate{On: day(2026, 5, 1), Precision: 11}, d, "самая ранняя в пределах года фильма")

	// Точнее — лучше: день в том же году важнее месяца, даже если месяц раньше.
	d, ok = pickReleaseDate([]releaseValue{{on: day(2026, 2, 1), precision: 10}, {on: day(2026, 9, 3), precision: 11}}, 2026)
	require.True(t, ok)
	require.Equal(t, day(2026, 9, 3), d.On)

	// Только месяц — 1-е число и точность «месяц».
	d, ok = pickReleaseDate([]releaseValue{{on: day(2027, 3, 15), precision: 10}}, 2027)
	require.True(t, ok)
	require.Equal(t, ReleaseDate{On: day(2027, 3, 1), Precision: 10}, d)

	// Только год или ничего в году фильма — даты нет.
	_, ok = pickReleaseDate([]releaseValue{{on: day(2026, 1, 1), precision: 9}}, 2026)
	require.False(t, ok)
	_, ok = pickReleaseDate([]releaseValue{{on: day(2025, 6, 9), precision: 11}}, 2026)
	require.False(t, ok)
	_, ok = pickReleaseDate(nil, 2026)
	require.False(t, ok)
}

// TestRefreshReleaseDates — фаза дат выхода (#581): будущие фильмы окна, дата —
// на все строки фильма, без даты — NULL и TTL; сбой Wikidata — без отметки.
func TestRefreshReleaseDates(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)

	dates := map[string][][2]string{
		"Q100": {{"2025-06-09T00:00:00Z", "11"}, {"2026-05-01T00:00:00Z", "11"}},
		"Q200": {{"2027-03-15T00:00:00Z", "10"}},
		"Q300": {{"2026-01-01T00:00:00Z", "9"}},
	}
	var fail atomic.Bool
	var asked atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		var bindings []map[string]map[string]string
		for _, q := range extractAllQIDs(r.FormValue("query")) {
			asked.Add(1)
			for _, d := range dates[q] {
				bindings = append(bindings, map[string]map[string]string{
					"film": {"value": "http://www.wikidata.org/entity/" + q},
					"date": {"value": d[0]}, "prec": {"value": d[1]},
				})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": map[string]any{"bindings": bindings}})
	}))
	defer srv.Close()
	wd := NewWikidataAdaptationsProvider(nil).WithEndpoints("", srv.URL, "")

	collID, archID := seedTitleFixture(t, ctx, pool)
	a := seedGroupAuthor(t, ctx, pool, "Оруэлл", "оруэлл джордж")
	ru := seedGroupBook(t, ctx, pool, collID, archID, a, "ru", "Скотный двор", "скотный двор", "ru", "", "", "")
	lv := seedGroupBook(t, ctx, pool, collID, archID, a, "lv", "Dzīvnieku ferma", "dzivnieku ferma", "lv", "", "", "")
	other := seedGroupBook(t, ctx, pool, collID, archID, a, "o", "Другая", "другая", "ru", "", "", "")
	ins := func(book int64, qid string, year int) {
		_, err := pool.Exec(ctx, `INSERT INTO book_adaptations (book_id, provider, ext_id, title, year, kind)
			VALUES ($1, 'wikidata', $2, 'Фильм', $3, 'film')`, book, qid, year)
		require.NoError(t, err)
	}
	ins(ru, "Q100", 2026)
	ins(lv, "Q100", 2026)
	ins(other, "Q200", 2027)
	ins(other, "Q300", 2026)
	ins(ru, "Q400", 2019) // прошлое — вне окна

	now := day(2026, 10, 23)
	fail.Store(true)
	_, _, err := refreshReleaseDates(ctx, pool, wd, now)
	require.Error(t, err, "сбой Wikidata — ошибка")
	var checked int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM book_adaptations WHERE released_checked_at IS NOT NULL`).Scan(&checked))
	require.Zero(t, checked, "сбой — без отметки, ретрай в следующем цикле")

	fail.Store(false)
	n, dated, err := refreshReleaseDates(ctx, pool, wd, now)
	require.NoError(t, err)
	require.Equal(t, 3, n, "три будущих фильма; 2019 — вне окна")
	require.Equal(t, 2, dated)

	get := func(book int64, qid string) (pgtype.Date, pgtype.Int2, bool) {
		var (
			on      pgtype.Date
			prec    pgtype.Int2
			checked bool
		)
		require.NoError(t, pool.QueryRow(ctx, `SELECT released_on, released_precision, released_checked_at IS NOT NULL
			FROM book_adaptations WHERE book_id = $1 AND ext_id = $2`, book, qid).Scan(&on, &prec, &checked))
		return on, prec, checked
	}
	for _, b := range []int64{ru, lv} {
		on, prec, ok := get(b, "Q100")
		require.True(t, ok)
		require.Equal(t, day(2026, 5, 1), on.Time, "дата — на все строки фильма")
		require.EqualValues(t, 11, prec.Int16)
	}
	on, prec, _ := get(other, "Q200")
	require.Equal(t, day(2027, 3, 1), on.Time)
	require.EqualValues(t, 10, prec.Int16)
	on, _, ok := get(other, "Q300")
	require.True(t, ok, "проверен, хоть даты и нет")
	require.False(t, on.Valid, "только год — не дата")
	_, _, ok = get(ru, "Q400")
	require.False(t, ok)

	// В пределах TTL не перепрашиваем.
	asked.Store(0)
	n, _, err = refreshReleaseDates(ctx, pool, wd, now)
	require.NoError(t, err)
	require.Zero(t, n)
	require.Zero(t, asked.Load())
	// TTL вышел — перепрос (checked_at ставит now() базы — сдвигаем его).
	_, err = pool.Exec(ctx, `UPDATE book_adaptations SET released_checked_at = $1`, now.Add(-releaseDateTTL-time.Hour))
	require.NoError(t, err)
	n, _, err = refreshReleaseDates(ctx, pool, wd, now)
	require.NoError(t, err)
	require.Equal(t, 3, n)
}
