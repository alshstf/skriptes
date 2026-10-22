package recsys_test

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/recsys"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestFeedbackSignals — фаза 3 (план recsys/03): полка «Хочу прочитать» и
// причины низкой оценки попадают в сигналы и близость (3.3, 3.1); «не
// предлагать» автора/цикл — в профиль и отсев Hydrate/Feed (3.2); правило
// ToReadReminder напоминает о пролежавшем ≥ 60 дней и молчит о тронутом.
func TestFeedbackSignals(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	f := newFixture(t, ctx, pool)
	me := f.user("me@example.com")
	liked, blocked := f.author("Любимый"), f.author("Блокированный")
	cycle := f.series("Цикл", nil)
	gSF := f.genre("sf")

	mk := func(title string, author int64, series *int64) (int64, int64) {
		w := f.work(title, ip(2000), series)
		e := f.edition(w, sp("en"), nil)
		f.exec(`INSERT INTO book_authors (book_id, author_id) VALUES ($1,$2)`, e, author)
		f.exec(`INSERT INTO book_genres (book_id, genre_id) VALUES ($1,$2)`, e, gSF)
		return w, e
	}
	wA, eA := mk("A", liked, nil)       // на полке 90 дней — напомнить
	_, eB := mk("B", liked, nil)        // на полке 10 дней — рано
	wC, _ := mk("C", liked, nil)        // оценка 2: «не моё», «язык/перевод»
	wD, eD := mk("D", blocked, nil)     // автор в блок-листе
	wE, eE := mk("E", liked, lp(cycle)) // цикл в блок-листе
	_, eF := mk("F", liked, nil)        // на полке 100 дней, но открыта в ридере

	var shelf int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO user_collections (user_id, name, kind) VALUES ($1,'Хочу прочитать','to_read') RETURNING id`, me).Scan(&shelf))
	f.exec(`INSERT INTO user_collection_books (collection_id, book_id, added_at) VALUES
		($1,$2,now() - interval '90 days'), ($1,$3,now() - interval '10 days'),
		($1,$4,now() - interval '95 days'), ($1,$5,now() - interval '97 days'), ($1,$6,now() - interval '100 days')`,
		shelf, eA, eB, eD, eE, eF)
	f.exec(`INSERT INTO reads (user_id, book_id, fraction, last_pos, updated_at) VALUES ($1,$2,0.1,'p',now())`, me, eF)
	f.exec(`INSERT INTO book_ratings (user_id, work_id, rating, rated_at) VALUES ($1,$2,2,now())`, me, wC)
	f.exec(`INSERT INTO rating_reasons (user_id, work_id, reason) VALUES ($1,$2,'not_mine'), ($1,$2,'language')`, me, wC)
	f.exec(`INSERT INTO blocked_authors (user_id, author_id) VALUES ($1,$2)`, me, blocked)
	f.exec(`INSERT INTO blocked_series (user_id, series_id) VALUES ($1,$2)`, me, cycle)

	now := time.Now()
	p, err := recsys.BuildProfile(ctx, pool, me, now, recsys.DefaultWeights)
	require.NoError(t, err)

	// 3.3: полка — сигнал и слагаемое близости.
	sA := p.Signals[wA]
	require.True(t, sA.ToRead)
	require.WithinDuration(t, now.Add(-90*24*time.Hour), sA.ToReadAt, time.Minute)
	require.Equal(t, recsys.DefaultWeights.ToRead, p.Works[wA].Parts[recsys.KindToRead])
	require.False(t, p.Engaged(wA), "полка — не «в руках»")

	// 3.1: причины — в сигналах, «не моё» — минус работе (и через неё автору),
	// «язык/перевод» — минус языку оригинала, не работе; счётчики — в профиле.
	sC := p.Signals[wC]
	require.ElementsMatch(t, []recsys.RatingReason{recsys.ReasonNotMine, recsys.ReasonLanguage}, sC.RatingReasons)
	require.True(t, sC.HasReason(recsys.ReasonNotMine))
	require.False(t, sC.HasReason(recsys.ReasonTooLong))
	require.Equal(t, recsys.DefaultWeights.ReasonNotMine, p.Works[wC].Parts[recsys.KindNotMine])
	require.InDelta(t, recsys.DefaultWeights.Rating[2]+recsys.DefaultWeights.ReasonNotMine, p.Works[wC].Score, 0.01)
	require.Equal(t, 1, p.Reasons[recsys.ReasonNotMine])
	require.Equal(t, 1, p.Reasons[recsys.ReasonLanguage])
	require.Equal(t, 0, p.Reasons[recsys.ReasonTooLong])
	// Все работы с сигналами (полка, оценка, чтение) — английские.
	sum := 0.0
	for _, a := range p.Works {
		sum += a.Score
	}
	require.Len(t, p.Works, 6)
	require.InDelta(t, sum+recsys.DefaultWeights.ReasonLanguage, p.Langs["en"], 1e-6, "язык: сумма близостей + малус причины")
	require.Contains(t, p.Explain(wC), "not_mine")

	// 3.2: блок-лист в профиле; не сигнал (в суммы по авторам не входит).
	require.Contains(t, p.BlockedAuthors, blocked)
	require.Contains(t, p.BlockedSeries, cycle)
	require.InDelta(t, p.Works[wD].Score, p.Authors[blocked], 1e-9, "блок — не сигнал: у автора только близость его работы с полки")
	require.True(t, p.Blocked([]int64{blocked}, nil))
	require.True(t, p.Blocked(nil, []int64{cycle}))
	require.False(t, p.Blocked([]int64{liked}, nil))

	// Hydrate: без блок-листа — все, с ним — ни автора, ни цикла; id авторов и
	// цикла — в карточке.
	cands := []recsys.Candidate{
		{WorkID: wA, Reason: recsys.Reason{Kind: recsys.ReasonToRead}},
		{WorkID: wD, Reason: recsys.Reason{Kind: recsys.ReasonToRead}},
		{WorkID: wE, Reason: recsys.Reason{Kind: recsys.ReasonToRead}},
	}
	items, err := recsys.Hydrate(ctx, pool, cands, recsys.Exclusions{}, 10)
	require.NoError(t, err)
	require.Len(t, items, 3)
	require.Equal(t, []int64{liked}, items[0].AuthorIDs)
	require.Equal(t, []string{"Любимый А"}, items[0].Authors)
	require.Equal(t, cycle, items[2].SeriesID)
	require.Equal(t, "Цикл", items[2].Series)
	items, err = recsys.Hydrate(ctx, pool, cands, recsys.Exclusions{}.WithBlocklist(p), 10)
	require.NoError(t, err)
	require.Len(t, items, 1, "блокированный автор и цикл выпали")
	require.Equal(t, wA, items[0].WorkID)

	// Правило «Вы хотели прочитать»: ≥ 60 дней на полке и не тронуто (блок-лист
	// — дело Hydrate, правило его не знает).
	cs, err := recsys.ToReadReminder{}.Candidates(ctx, pool, p, 10)
	require.NoError(t, err)
	got := make([]int64, 0, len(cs))
	for _, c := range cs {
		got = append(got, c.WorkID)
		require.Equal(t, recsys.ReasonToRead, c.Reason.Kind)
		require.NotNil(t, c.Reason.At)
	}
	require.ElementsMatch(t, []int64{wA, wD, wE}, got, "B рано, F открыта")

	// Лента: секция есть в реестре, блок-лист отсеян.
	_, ok := recsys.RuleFor(recsys.SectionToRead)
	require.True(t, ok)
	require.True(t, recsys.IsSectionKind(string(recsys.SectionToRead)))
	feed := &recsys.Feed{Q: pool, Weights: recsys.DefaultWeights, Now: func() time.Time { return now }}
	fi, err := feed.Section(ctx, recsys.SectionToRead, me, recsys.Exclusions{}, 10, "")
	require.NoError(t, err)
	require.Len(t, fi, 1)
	require.Equal(t, wA, fi[0].WorkID)
	r, ok := fi[0].Reason(recsys.ReasonToRead)
	require.True(t, ok)
	require.WithinDuration(t, now.Add(-90*24*time.Hour), *r.At, time.Minute)

	// Открыли A в ридере — напоминание уходит.
	f.exec(`INSERT INTO reads (user_id, book_id, fraction, last_pos, updated_at) VALUES ($1,$2,0.2,'p',now())`, me, eA)
	fi, err = feed.Section(ctx, recsys.SectionToRead, me, recsys.Exclusions{}, 10, "")
	require.NoError(t, err)
	require.Empty(t, fi)

	// Закрытый список причин.
	require.True(t, recsys.IsRatingReason("boring"))
	require.False(t, recsys.IsRatingReason("meh"))
	require.Len(t, recsys.AllRatingReasons, 7)
}
