package recsys_test

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/recsys"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestFitsTonight — «На вечер»: размер ≤ минуты × скорость, только любимые
// жанры, тронутое — нет, порядок — известность + жанр; без личной скорости —
// DefaultBytesPerMin; причина — минуты и жанр.
func TestFitsTonight(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	f := newFixture(t, ctx, pool)
	me := f.user("me@example.com")
	a := f.author("Автор")
	gSF, gDet, gPoetry := f.genre("sf"), f.genre("detective"), f.genre("poetry")
	mk := func(title string, size int64, editions int, genres ...int64) (int64, int64) {
		w := f.work(title, nil, nil)
		f.exec(`UPDATE works SET edition_count = $2 WHERE id = $1`, w, editions)
		e := f.edition(w, nil, nil)
		f.exec(`UPDATE books SET size_bytes = $2 WHERE id = $1`, e, size)
		f.exec(`INSERT INTO book_authors (book_id, author_id) VALUES ($1,$2)`, e, a)
		for _, g := range genres {
			f.exec(`INSERT INTO book_genres (book_id, genre_id) VALUES ($1,$2)`, e, g)
		}
		return w, e
	}
	// Вкус: оценка 5 фантастике (sf) и 4 детективу — оба любимые; поэзия — нет.
	liked, _ := mk("Любимая фантастика", 500_000, 1, gSF)
	likedDet, _ := mk("Любимый детектив", 500_000, 1, gDet)
	f.exec(`INSERT INTO book_ratings (user_id, work_id, rating) VALUES ($1,$2,5), ($1,$3,4)`, me, liked, likedDet)

	short, _ := mk("Короткая sf", 60_000, 2, gSF)               // 60 КБ: 24 мин при 2500 Б/мин
	shortBoth, _ := mk("Короткая sf+det", 80_000, 2, gSF, gDet) // 32 мин, два жанра — выше при равной известности
	long, _ := mk("Длинная sf", 400_000, 10, gSF)               // 160 мин — не влезает в 40
	poem, _ := mk("Стихи", 10_000, 5, gPoetry)                  // не любимый жанр
	opened, oe := mk("Открытая sf", 50_000, 9, gSF)             // в руках
	f.exec(`INSERT INTO reads (user_id, book_id, fraction, last_pos, updated_at) VALUES ($1,$2,0.5,'p',now())`, me, oe)
	_, _ = long, poem

	p, err := recsys.BuildProfile(ctx, pool, me, time.Now(), recsys.DefaultWeights)
	require.NoError(t, err)
	require.Positive(t, p.Genres["sf"])

	rule := recsys.FitsTonight{Minutes: 40}
	require.Equal(t, int64(100_000), rule.MaxBytes())
	cands, err := rule.Candidates(ctx, pool, p, 10)
	require.NoError(t, err)
	require.Equal(t, []int64{shortBoth, short}, []int64{cands[0].WorkID, cands[1].WorkID})
	require.Len(t, cands, 2)
	r := cands[0].Reason
	require.Equal(t, recsys.ReasonFitsTonight, r.Kind)
	require.Equal(t, 32, r.Minutes)
	require.Equal(t, "sf", r.Genre, "сильнейший жанр — sf (оценка 5)")
	require.Equal(t, 24, cands[1].Reason.Minutes)
	_ = opened

	// Личная скорость быстрее → влезает и длинная; минуты пересчитаны.
	fast := recsys.FitsTonight{Minutes: 40, BytesPerMin: 10_000}
	cands, err = fast.Candidates(ctx, pool, p, 10)
	require.NoError(t, err)
	require.Len(t, cands, 3)
	require.Equal(t, long, cands[0].WorkID, "самая известная из влезающих — первая")
	require.Equal(t, 40, cands[0].Reason.Minutes)

	// Лента: минуты вне диапазона → 40; скорость неизвестна → по умолчанию.
	feed := recsys.NewFeed(pool)
	items, err := feed.Tonight(ctx, me, 0, recsys.Speed{}, recsys.Exclusions{}, 10)
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, "Короткая sf+det", items[0].Title)
	items, err = feed.Tonight(ctx, me, 20, recsys.Speed{BytesPerMin: 10_000, Sessions: 5}, recsys.Exclusions{}, 10)
	require.NoError(t, err)
	require.Len(t, items, 2, "20 мин × 10 000 = 200 КБ: длинная не влезает")

	// Нет любимых жанров — пусто.
	nobody := f.user("nobody@example.com")
	items, err = feed.Tonight(ctx, nobody, 40, recsys.Speed{}, recsys.Exclusions{}, 10)
	require.NoError(t, err)
	require.Empty(t, items)
	none, err := recsys.FitsTonight{Minutes: 40}.Candidates(ctx, pool, &recsys.Profile{}, 10)
	require.NoError(t, err)
	require.Empty(t, none)
}
