package recsys_test

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/recsys"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestBaselines — «тот же автор» берёт работы авторов с положительной близостью,
// не предлагает известное, антологий не считает; «случайное» детерминировано
// по seed и не предлагает известное; «популярное» пропускает известное.
func TestBaselines(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	f := newFixture(t, ctx, pool)
	me := f.user("bl@example.com")
	liked, disliked := f.author("Любимый"), f.author("Нелюбимый")

	mk := func(title string, year int, authors ...int64) int64 {
		w := f.work(title, ip(year), nil)
		e := f.edition(w, nil, nil)
		for i, a := range authors {
			f.exec(`INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,$3)`, e, a, i)
		}
		return w
	}
	wRead := mk("Прочитанная", 1970, liked)
	wNext := mk("Следующая", 1975, liked)
	wNext2 := mk("Ещё одна", 1980, liked)
	wBad := mk("Чужая", 1990, disliked)
	anth := mk("Антология", 2000, liked, f.author("А1"), f.author("А2"), f.author("А3"))
	f.exec(`INSERT INTO book_ratings (user_id, work_id, rating) VALUES ($1,$2,5), ($1,$3,1)`, me, wRead, wBad)

	p, err := recsys.BuildProfile(ctx, pool, me, time.Now(), recsys.DefaultWeights)
	require.NoError(t, err)
	require.Positive(t, p.Authors[liked])
	require.Negative(t, p.Authors[disliked])

	same := recsys.SameAuthorRecommender{Q: pool}
	list, err := same.Recommend(ctx, p, 10)
	require.NoError(t, err)
	require.ElementsMatch(t, []int64{wNext, wNext2}, list, "работы любимого автора, без прочитанной, без антологии и без чужого")
	_ = anth

	rnd := recsys.RandomRecommender{Q: pool, Seed: 7}
	a, err := rnd.Recommend(ctx, p, 10)
	require.NoError(t, err)
	b, err := rnd.Recommend(ctx, p, 10)
	require.NoError(t, err)
	require.Equal(t, a, b, "детерминировано по seed")
	require.NotContains(t, a, wRead)
	require.NotContains(t, a, wBad)
	require.Len(t, a, 3, "все видимые работы кроме известных")

	pop := recsys.PopularityRecommender{Top: func(_ context.Context, offset, n int) ([]int64, error) {
		all := []int64{wRead, wBad, wNext, anth, wNext2}
		if offset >= len(all) {
			return nil, nil
		}
		return all[offset:min(offset+n, len(all))], nil
	}}
	list, err = pop.Recommend(ctx, p, 2)
	require.NoError(t, err)
	require.Equal(t, []int64{wNext, anth}, list, "известное пропущено, порядок топа сохранён")
	require.Equal(t, "popularity, same_author, random", recsys.Names([]recsys.Recommender{pop, same, rnd}))
}
