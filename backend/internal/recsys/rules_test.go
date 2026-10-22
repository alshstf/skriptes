package recsys_test

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/recsys"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestNextInSeries — «Дальше в цикле»: ближайший больший номер после самого
// старшего прочитанного (с дыркой), следующая «в руках» блокирует цикл,
// межавторская серия не предлагается, скрытое на инстансе и работа без
// живого издания выпадают; порядок — недавно прочитанные циклы сверху.
func TestNextInSeries(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	f := newFixture(t, ctx, pool)
	me := f.user("me@example.com")
	author := f.author("Циклов")

	cycle, multi := f.series("Цикл", nil), f.series("Межавторская", sp("multi"))
	mkNumbered := func(series int64, title string, no int) (int64, int64) {
		w := f.work(title, nil, lp(series))
		f.exec(`UPDATE works SET ser_no = $2 WHERE id = $1`, w, no)
		e := f.edition(w, nil, nil)
		f.exec(`INSERT INTO book_authors (book_id, author_id) VALUES ($1,$2)`, e, author)
		return w, e
	}
	w1, e1 := mkNumbered(cycle, "Том 1", 1)
	w2, e2 := mkNumbered(cycle, "Том 2", 2)
	w3, e3 := mkNumbered(cycle, "Том 3", 3)
	w5, _ := mkNumbered(cycle, "Том 5", 5)
	w7, _ := mkNumbered(cycle, "Том 7", 7)
	m1, me1 := mkNumbered(multi, "Антология 1", 1)
	_, _ = mkNumbered(multi, "Антология 2", 2)
	_ = w1

	now := time.Now()
	profile := func() *recsys.Profile {
		p, err := recsys.BuildProfile(ctx, pool, me, now, recsys.DefaultWeights)
		require.NoError(t, err)
		return p
	}
	rule := recsys.NextInSeries{}

	// Ничего не прочитано — пусто.
	cands, err := rule.Candidates(ctx, pool, profile(), 10)
	require.NoError(t, err)
	require.Empty(t, cands)

	// Прочитаны №1 и №2 (второй — оценкой без отметки), антология №1 — тоже.
	f.exec(`INSERT INTO reads (user_id, book_id, completed_at, updated_at) VALUES ($1,$2,now() - interval '10 days',now())`, me, e1)
	f.exec(`INSERT INTO book_ratings (user_id, work_id, rating, rated_at) VALUES ($1,$2,5,now() - interval '5 days')`, me, w2)
	f.exec(`INSERT INTO reads (user_id, book_id, completed_at, updated_at) VALUES ($1,$2,now(),now())`, me, me1)
	cands, err = rule.Candidates(ctx, pool, profile(), 10)
	require.NoError(t, err)
	require.Len(t, cands, 1, "межавторская серия — не цикл")
	require.Equal(t, w3, cands[0].WorkID)
	r := cands[0].Reason
	require.Equal(t, recsys.ReasonNextInSeries, r.Kind)
	require.Equal(t, cycle, r.SeriesID)
	require.Equal(t, "Цикл", r.Series)
	require.Equal(t, w2, r.WorkID, "основание — старшая прочитанная")
	require.Equal(t, "Том 2", r.Title)
	require.Equal(t, 3, r.SerNo)
	require.NotNil(t, r.At)
	require.WithinDuration(t, now.Add(-5*24*time.Hour), *r.At, time.Minute)
	_, _ = m1, e2

	// №3 открыт в ридере — цикл блокируется (через читаемое не прыгаем).
	f.exec(`INSERT INTO reads (user_id, book_id, fraction, last_pos, updated_at) VALUES ($1,$2,0.3,'p',now())`, me, e3)
	cands, err = rule.Candidates(ctx, pool, profile(), 10)
	require.NoError(t, err)
	require.Empty(t, cands)

	// №3 дочитан — следующий №5 (дырка в нумерации).
	f.exec(`UPDATE reads SET completed_at = now() - interval '1 hour' WHERE user_id = $1 AND book_id = $2`, me, e3)
	cands, err = rule.Candidates(ctx, pool, profile(), 10)
	require.NoError(t, err)
	require.Len(t, cands, 1)
	require.Equal(t, w5, cands[0].WorkID)
	require.Equal(t, 5, cands[0].Reason.SerNo)

	// №5 скрыт на инстансе — №7 не предлагаем вместо него: ближайший больший
	// номер исчез из каталога, значит, следующего нет.
	f.exec(`UPDATE works SET instance_hidden = true WHERE id = $1`, w5)
	cands, err = rule.Candidates(ctx, pool, profile(), 10)
	require.NoError(t, err)
	require.Len(t, cands, 1)
	require.Equal(t, w7, cands[0].WorkID, "скрытого в каталоге нет — ближайший больший теперь №7")
	f.exec(`UPDATE works SET instance_hidden = false WHERE id = $1`, w5)

	// Второй цикл, прочитанный позже, — выше в секции; харнесс видит те же id.
	cycle2 := f.series("Второй", nil)
	_, c1 := mkNumbered(cycle2, "В-1", 1)
	c2w, _ := mkNumbered(cycle2, "В-2", 2)
	f.exec(`INSERT INTO reads (user_id, book_id, completed_at, updated_at) VALUES ($1,$2,now(),now())`, me, c1)
	cands, err = rule.Candidates(ctx, pool, profile(), 10)
	require.NoError(t, err)
	require.Len(t, cands, 2)
	require.Equal(t, []int64{c2w, w5}, []int64{cands[0].WorkID, cands[1].WorkID})
	ids, err := recsys.RuleRecommender{Rule: rule, Q: pool}.Recommend(ctx, profile(), 1)
	require.NoError(t, err)
	require.Equal(t, []int64{c2w}, ids)

	// Скрыл №5 из ленты — цикл «Цикл» выпадает.
	f.exec(`INSERT INTO feed_dismissals (user_id, work_id, reason) VALUES ($1,$2,'later')`, me, w5)
	cands, err = rule.Candidates(ctx, pool, profile(), 10)
	require.NoError(t, err)
	require.Len(t, cands, 1)
	require.Equal(t, c2w, cands[0].WorkID)
}

// TestAuthorFinished — «Автор, которого вы дочитали»: следующая по году после
// последней прочитанной; без хронологии — самая известная; пороги «понравился»
// (средняя ≥ 4 или два прочитанных без оценок); сборники, антологии и
// служебные авторы не в счёт; порядок — по близости к автору.
func TestAuthorFinished(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	f := newFixture(t, ctx, pool)
	me := f.user("me@example.com")

	mk := func(author int64, title string, year *int, editions int) (int64, int64) {
		w := f.work(title, year, nil)
		f.exec(`UPDATE works SET edition_count = $2 WHERE id = $1`, w, editions)
		e := f.edition(w, nil, nil)
		f.exec(`INSERT INTO book_authors (book_id, author_id) VALUES ($1,$2)`, e, author)
		return w, e
	}
	// Автор A: четыре работы по годам, одна без года, но самая издаваемая.
	a := f.author("Хронологичный")
	a60, e60 := mk(a, "1960", ip(1960), 1)
	a70, e70 := mk(a, "1970", ip(1970), 1)
	a80, e80 := mk(a, "1980", ip(1980), 1)
	aNoYear, _ := mk(a, "Без года", nil, 3)
	// Автор B: без оценок — нужно два прочитанных.
	b := f.author("Нечитаный")
	_, be1 := mk(b, "B1", ip(2000), 1)
	_, be2 := mk(b, "B2", ip(2001), 1)
	b3, _ := mk(b, "B3", ip(2002), 1)
	// Автор C: средняя оценка 3 — не понравился.
	c := f.author("Средний")
	c1, _ := mk(c, "C1", ip(1990), 1)
	c2, _ := mk(c, "C2", ip(1991), 1)
	_, _ = mk(c, "C3", ip(1992), 1)
	// Служебный автор: прочитано — но не предлагаем.
	svc := f.author("Коллектив")
	f.exec(`UPDATE authors SET is_service = true WHERE id = $1`, svc)
	s1, se1 := mk(svc, "S1", ip(1950), 1)
	_, _ = mk(svc, "S2", ip(1951), 1)
	_ = s1

	now := time.Now()
	profile := func() *recsys.Profile {
		p, err := recsys.BuildProfile(ctx, pool, me, now, recsys.DefaultWeights)
		require.NoError(t, err)
		return p
	}
	rule := recsys.AuthorFinished{}

	// A: оценка 5 за 1960 → следующая по хронологии — 1970.
	f.exec(`INSERT INTO book_ratings (user_id, work_id, rating, rated_at) VALUES ($1,$2,5,now() - interval '30 days')`, me, a60)
	// C: 2 и 4 — средняя 3. Служебный — дочитан дважды.
	f.exec(`INSERT INTO book_ratings (user_id, work_id, rating, rated_at) VALUES ($1,$2,2,now()), ($1,$3,4,now())`, me, c1, c2)
	f.exec(`INSERT INTO reads (user_id, book_id, completed_at, updated_at) VALUES ($1,$2,now(),now())`, me, se1)
	cands, err := rule.Candidates(ctx, pool, profile(), 10)
	require.NoError(t, err)
	require.Len(t, cands, 1)
	require.Equal(t, a70, cands[0].WorkID)
	r := cands[0].Reason
	require.Equal(t, recsys.ReasonAuthorFinished, r.Kind)
	require.Equal(t, a, r.AuthorID)
	require.Equal(t, "Хронологичный А", r.Author)
	require.Equal(t, a60, r.WorkID)
	require.Equal(t, "1960", r.Title)
	require.NotNil(t, r.At)

	// Дочитал 1970 позже оценки → последняя прочитанная 1970 → кандидат 1980.
	f.exec(`INSERT INTO reads (user_id, book_id, completed_at, updated_at) VALUES ($1,$2,now() - interval '1 day',now())`, me, e70)
	cands, err = rule.Candidates(ctx, pool, profile(), 10)
	require.NoError(t, err)
	require.Len(t, cands, 1)
	require.Equal(t, a80, cands[0].WorkID)
	require.Equal(t, a70, cands[0].Reason.WorkID)

	// Дочитал и 1980 — позже ничего нет → самая известная непрочитанная (без года).
	f.exec(`INSERT INTO reads (user_id, book_id, completed_at, updated_at) VALUES ($1,$2,now(),now())`, me, e80)
	cands, err = rule.Candidates(ctx, pool, profile(), 10)
	require.NoError(t, err)
	require.Len(t, cands, 1)
	require.Equal(t, aNoYear, cands[0].WorkID)
	_ = e60

	// B: одно прочитанное без оценки — мало; два — автор понравился, кандидат B3.
	f.exec(`INSERT INTO reads (user_id, book_id, completed_at, updated_at) VALUES ($1,$2,now(),now())`, me, be1)
	cands, err = rule.Candidates(ctx, pool, profile(), 10)
	require.NoError(t, err)
	require.Len(t, cands, 1)
	f.exec(`INSERT INTO reads (user_id, book_id, completed_at, updated_at) VALUES ($1,$2,now(),now())`, me, be2)
	cands, err = rule.Candidates(ctx, pool, profile(), 10)
	require.NoError(t, err)
	require.Len(t, cands, 2)
	// Порядок — по близости: A (оценка 5 + три дочитанных) выше B.
	require.Equal(t, []int64{aNoYear, b3}, []int64{cands[0].WorkID, cands[1].WorkID})
	require.Len(t, cands, 2)

	// Сборник автора A — не кандидат; антология с > MaxAuthors авторами — не прочитанное.
	comp := f.work("Избранное", ip(1999), nil)
	f.exec(`UPDATE works SET kind = 'collection' WHERE id = $1`, comp)
	ce := f.edition(comp, nil, nil)
	f.exec(`INSERT INTO book_authors (book_id, author_id) VALUES ($1,$2)`, ce, a)
	anth := f.work("Антология", ip(2010), nil)
	ae := f.edition(anth, nil, nil)
	for i := 0; i <= recsys.MaxAuthors; i++ {
		f.exec(`INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,$3)`, ae, f.author("Антолог"+string(rune('a'+i))), i)
	}
	f.exec(`INSERT INTO reads (user_id, book_id, completed_at, updated_at) VALUES ($1,$2,now(),now())`, me, ae)
	// Все обычные работы A прочитаны → у A кандидатов нет (сборник не в счёт).
	f.exec(`INSERT INTO book_ratings (user_id, work_id, rating) VALUES ($1,$2,5)`, me, aNoYear)
	cands, err = rule.Candidates(ctx, pool, profile(), 10)
	require.NoError(t, err)
	require.Len(t, cands, 1)
	require.Equal(t, b3, cands[0].WorkID)
}

// TestFeedSectionAndHydrate — лента: профиль → правило → карточки с учётом
// скрытого у пользователя (язык, жанр, сборники, скрытое на инстансе);
// карточка несёт представительное издание, авторов, цикл и причину;
// неизвестная секция — ошибка; пустой профиль — пустая секция.
func TestFeedSectionAndHydrate(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	f := newFixture(t, ctx, pool)
	me := f.user("me@example.com")
	author := f.author("Автор")
	cycle := f.series("Цикл", nil)
	gHidden := f.genre("hidden_genre")

	mk := func(title string, no int, lang string, genre *int64) (int64, int64) {
		w := f.work(title, nil, lp(cycle))
		f.exec(`UPDATE works SET ser_no = $2 WHERE id = $1`, w, no)
		e := f.edition(w, nil, nil)
		f.exec(`UPDATE books SET lang = $2, title = $3, cover_path = 'c.jpg' WHERE id = $1`, e, lang, title+" (изд.)")
		f.exec(`INSERT INTO book_authors (book_id, author_id) VALUES ($1,$2)`, e, author)
		if genre != nil {
			f.exec(`INSERT INTO book_genres (book_id, genre_id) VALUES ($1,$2)`, e, *genre)
		}
		return w, e
	}
	w1, e1 := mk("Том 1", 1, "ru", nil)
	w2, e2 := mk("Том 2", 2, "en", nil)
	_ = w1
	f.exec(`INSERT INTO reads (user_id, book_id, completed_at, updated_at) VALUES ($1,$2,now(),now())`, me, e1)

	feed := recsys.NewFeed(pool)
	_, err := feed.Section(ctx, recsys.SectionKind("nope"), me, recsys.Exclusions{}, 10, "")
	require.ErrorIs(t, err, recsys.ErrUnknownSection)

	items, err := feed.Section(ctx, recsys.SectionNextInSeries, me, recsys.Exclusions{}, 10, "")
	require.NoError(t, err)
	require.Len(t, items, 1)
	it := items[0]
	require.Equal(t, w2, it.WorkID)
	require.Equal(t, e2, it.ID, "представитель — живое издание")
	require.Equal(t, "Том 2", it.Title, "название — работы")
	require.Equal(t, []string{"Автор А"}, it.Authors)
	require.Equal(t, "Цикл", it.Series)
	require.Equal(t, "c.jpg", it.CoverPath)
	require.Len(t, it.Reasons, 1)
	require.Equal(t, recsys.ReasonNextInSeries, it.Reasons[0].Kind)

	// Скрытый язык издания → карточки нет; скрытый жанр → тоже; «скрывать
	// сборники» — работа-сборник выпадает.
	items, err = feed.Section(ctx, recsys.SectionNextInSeries, me, recsys.Exclusions{Langs: []string{"en"}}, 10, "")
	require.NoError(t, err)
	require.Empty(t, items)
	f.exec(`INSERT INTO book_genres (book_id, genre_id) VALUES ($1,$2)`, e2, gHidden)
	items, err = feed.Section(ctx, recsys.SectionNextInSeries, me, recsys.Exclusions{Genres: []string{"hidden_genre"}}, 10, "")
	require.NoError(t, err)
	require.Empty(t, items)
	items, err = feed.Section(ctx, recsys.SectionNextInSeries, me, recsys.Exclusions{Genres: []string{"other"}, Langs: []string{"fr"}}, 10, "")
	require.NoError(t, err)
	require.Len(t, items, 1, "чужие исключения не мешают")

	// Hydrate напрямую: порядок кандидатов, limit, скрытое на инстансе и без
	// живого издания выпадают, сборник — только при HideCompilations.
	w3, e3 := mk("Том 3", 3, "ru", nil)
	f.exec(`UPDATE works SET kind = 'collection' WHERE id = $1`, w3)
	w4, e4 := mk("Том 4", 4, "ru", nil)
	f.exec(`UPDATE books SET deleted = true WHERE id = $1`, e4)
	w5, _ := mk("Том 5", 5, "ru", nil)
	f.exec(`UPDATE works SET instance_hidden = true WHERE id = $1`, w5)
	cands := []recsys.Candidate{
		{WorkID: w5, Reason: recsys.Reason{Kind: recsys.ReasonNextInSeries}},
		{WorkID: w4, Reason: recsys.Reason{Kind: recsys.ReasonNextInSeries}},
		{WorkID: w3, Reason: recsys.Reason{Kind: recsys.ReasonNextInSeries, SerNo: 3}},
		{WorkID: w2, Reason: recsys.Reason{Kind: recsys.ReasonNextInSeries, SerNo: 2}},
	}
	items, err = recsys.Hydrate(ctx, pool, cands, recsys.Exclusions{}, 10)
	require.NoError(t, err)
	require.Equal(t, []int64{w3, w2}, []int64{items[0].WorkID, items[1].WorkID})
	require.Equal(t, e3, items[0].ID)
	require.Equal(t, 3, items[0].Reasons[0].SerNo)
	items, err = recsys.Hydrate(ctx, pool, cands, recsys.Exclusions{HideCompilations: true}, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, w2, items[0].WorkID)
	items, err = recsys.Hydrate(ctx, pool, cands, recsys.Exclusions{}, 1)
	require.NoError(t, err)
	require.Len(t, items, 1)

	// Пустой профиль — пустая секция, без ошибки.
	items, err = feed.Section(ctx, recsys.SectionAuthorFinished, f.user("nobody@example.com"), recsys.Exclusions{}, 10, "")
	require.NoError(t, err)
	require.Empty(t, items)

	// Все правила вместе — без повторов.
	p, err := recsys.BuildProfile(ctx, pool, me, time.Now(), recsys.DefaultWeights)
	require.NoError(t, err)
	ids, err := recsys.RulesRecommender{Rules: recsys.Rules, Q: pool}.Recommend(ctx, p, 10)
	require.NoError(t, err)
	require.Equal(t, []int64{w2}, ids, "«автор» без оценок и с одной прочитанной молчит; «цикл» даёт №2")
}
