package recsys_test

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/recsys"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestDroppedThenLoved — «Отложенное, к которому доросли»: брошенная в начале
// работа возвращается, когда после бросания у того же автора ≥ 2 работ с
// оценкой ≥ 4 или дочитанных; полюбленное ДО бросания не считается; скрытая из
// ленты брошенная — нет; служебный автор — нет.
func TestDroppedThenLoved(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	f := newFixture(t, ctx, pool)
	me := f.user("me@example.com")
	a := f.author("Полюбленный")
	mk := func(title string) (int64, int64) {
		w := f.work(title, nil, nil)
		e := f.edition(w, nil, nil)
		f.exec(`INSERT INTO book_authors (book_id, author_id) VALUES ($1,$2)`, e, a)
		return w, e
	}
	dropped, de := mk("Брошенная")
	w1, e1 := mk("Полюбленная 1")
	w2, e2 := mk("Полюбленная 2")
	early, _ := mk("Ранняя")

	// Бросил 200 дней назад на 10 %.
	f.exec(`INSERT INTO reads (user_id, book_id, fraction, last_pos, updated_at) VALUES ($1,$2,0.1,'p',now() - interval '200 days')`, me, de)
	// Оценка 5 ДО бросания — не в счёт.
	f.exec(`INSERT INTO book_ratings (user_id, work_id, rating, rated_at) VALUES ($1,$2,5,now() - interval '300 days')`, me, early)
	rule := recsys.DroppedThenLoved{}
	profile := func() *recsys.Profile {
		p, err := recsys.BuildProfile(ctx, pool, me, time.Now(), recsys.DefaultWeights)
		require.NoError(t, err)
		return p
	}
	cands, err := rule.Candidates(ctx, pool, profile(), 10)
	require.NoError(t, err)
	require.Empty(t, cands, "до бросания — не считается")

	// После бросания: одна оценка 4 — мало; плюс дочитанная — хватает.
	f.exec(`INSERT INTO book_ratings (user_id, work_id, rating, rated_at) VALUES ($1,$2,4,now() - interval '30 days')`, me, w1)
	cands, err = rule.Candidates(ctx, pool, profile(), 10)
	require.NoError(t, err)
	require.Empty(t, cands)
	f.exec(`INSERT INTO reads (user_id, book_id, completed_at, updated_at) VALUES ($1,$2,now() - interval '10 days',now())`, me, e2)
	cands, err = rule.Candidates(ctx, pool, profile(), 10)
	require.NoError(t, err)
	require.Len(t, cands, 1)
	require.Equal(t, dropped, cands[0].WorkID)
	r := cands[0].Reason
	require.Equal(t, recsys.ReasonDropped, r.Kind)
	require.Equal(t, a, r.AuthorID)
	require.Equal(t, "Полюбленный А", r.Author)
	require.Equal(t, 2, r.Count)
	require.NotNil(t, r.At)
	require.WithinDuration(t, time.Now().Add(-200*24*time.Hour), *r.At, time.Hour)
	_, _, _ = w2, e1, w1

	// Лента: карточка с причиной.
	items, err := recsys.NewFeed(pool).Section(ctx, recsys.SectionDropped, me, recsys.Exclusions{}, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "Брошенная", items[0].Title)

	// Скрыл из ленты — нет; служебный автор — нет.
	f.exec(`INSERT INTO feed_dismissals (user_id, work_id, reason) VALUES ($1,$2,'later')`, me, dropped)
	cands, err = rule.Candidates(ctx, pool, profile(), 10)
	require.NoError(t, err)
	require.Empty(t, cands)
	f.exec(`DELETE FROM feed_dismissals WHERE user_id = $1`, me)
	f.exec(`UPDATE authors SET is_service = true WHERE id = $1`, a)
	cands, err = rule.Candidates(ctx, pool, profile(), 10)
	require.NoError(t, err)
	require.Empty(t, cands)
}

// TestReread — «Перечитать»: оценка 5, прочитано ≥ 3 лет назад, с тех пор не
// открывалось; одна карточка, выбор по неделе — детерминированный.
func TestReread(t *testing.T) {
	now := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	old := now.Add(-4 * 365 * 24 * time.Hour)
	p := &recsys.Profile{BuiltAt: now, Signals: map[int64]*recsys.WorkSignals{
		1: {WorkID: 1, Rating: 5, RatedAt: old, Completed: true, CompletedAt: old, ReadAt: old},
		2: {WorkID: 2, Rating: 4, RatedAt: old, ReadAt: old},                                                       // не 5
		3: {WorkID: 3, Rating: 5, RatedAt: now.Add(-365 * 24 * time.Hour), ReadAt: now.Add(-365 * 24 * time.Hour)}, // недавно
		4: {WorkID: 4, Rating: 5, RatedAt: old, ReadAt: now.Add(-24 * time.Hour)},                                  // открывалась вчера
		5: {WorkID: 5, Rating: 5, RatedAt: old, ReadAt: old, Dismissed: true},                                      // скрыта
		6: {WorkID: 6, Rating: 5, RatedAt: old, ReadAt: old},
	}}
	cands, err := recsys.Reread{}.Candidates(context.Background(), nil, p, 1)
	require.NoError(t, err)
	require.Len(t, cands, 1)
	require.Contains(t, []int64{1, 6}, cands[0].WorkID)
	require.Equal(t, recsys.ReasonReread, cands[0].Reason.Kind)
	require.NotNil(t, cands[0].Reason.At)
	// Та же неделя — тот же выбор; другая неделя — другой.
	again, err := recsys.Reread{}.Candidates(context.Background(), nil, p, 1)
	require.NoError(t, err)
	require.Equal(t, cands[0].WorkID, again[0].WorkID)
	p.BuiltAt = now.Add(7 * 24 * time.Hour)
	next, err := recsys.Reread{}.Candidates(context.Background(), nil, p, 1)
	require.NoError(t, err)
	require.NotEqual(t, cands[0].WorkID, next[0].WorkID)
	// n=2 — обе.
	both, err := recsys.Reread{}.Candidates(context.Background(), nil, p, 2)
	require.NoError(t, err)
	require.Len(t, both, 2)
	// Пусто без профиля.
	none, err := recsys.Reread{}.Candidates(context.Background(), nil, nil, 1)
	require.NoError(t, err)
	require.Empty(t, none)
}

// TestForgottenClassic — пул по каталогу (пороги, «никто не открывал»,
// известность автора) и ранжирование под пользователя (жанр, язык, своё — нет).
func TestForgottenClassic(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	f := newFixture(t, ctx, pool)
	me, other := f.user("me@example.com"), f.user("other@example.com")
	gSF, gDet := f.genre("sf"), f.genre("detective")

	// Известность: три автора — 1000, 500, 10; порог 66,7 % = между 500 и 1000.
	famous, mid, obscure := f.author("Известный"), f.author("Средний"), f.author("Безвестный")
	f.exec(`UPDATE authors SET renown = 1000 WHERE id = $1`, famous)
	f.exec(`UPDATE authors SET renown = 500 WHERE id = $1`, mid)
	f.exec(`UPDATE authors SET renown = 10 WHERE id = $1`, obscure)
	mk := func(author int64, title string, year int, fl float64, marks int, editions int, librate int, genre int64, lang string) int64 {
		w := f.work(title, ip(year), nil)
		f.exec(`UPDATE works SET primary_author_id = $2, fantlab_midmark = NULLIF($3::real, 0), fantlab_marks = NULLIF($4, 0), edition_count = $5 WHERE id = $1`,
			w, author, fl, marks, editions)
		e := f.edition(w, sp(lang), nil)
		f.exec(`UPDATE books SET rating = NULLIF($2, 0), lang = $3 WHERE id = $1`, e, librate, lang)
		f.exec(`INSERT INTO book_authors (book_id, author_id) VALUES ($1,$2)`, e, author)
		f.exec(`INSERT INTO book_genres (book_id, genre_id) VALUES ($1,$2)`, e, genre)
		return w
	}
	flSF := mk(famous, "Фантлаб sf", 1965, 9.0, 500, 1, 0, gSF, "ru")
	flDet := mk(famous, "Фантлаб detective", 1950, 9.4, 500, 1, 0, gDet, "en")
	lib := mk(famous, "LIBRATE", 1930, 0, 0, 5, 5, gDet, "en")
	_ = mk(famous, "Мало голосов", 1960, 9.9, 20, 1, 0, gSF, "ru")
	_ = mk(famous, "Мало изданий", 1960, 0, 0, 2, 5, gSF, "ru")
	_ = mk(famous, "Слишком новая", 2010, 9.5, 500, 1, 0, gSF, "ru")
	_ = mk(obscure, "Безвестного", 1950, 9.5, 500, 5, 5, gSF, "ru")
	seen := mk(famous, "Кто-то открывал", 1940, 9.6, 500, 1, 0, gSF, "ru")
	var seenEd int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM books WHERE work_id = $1`, seen).Scan(&seenEd))
	f.exec(`INSERT INTO views (user_id, book_id) VALUES ($1,$2)`, other, seenEd)
	comp := mk(famous, "Сборник", 1950, 9.5, 500, 5, 5, gSF, "ru")
	f.exec(`UPDATE works SET kind = 'collection' WHERE id = $1`, comp)

	cp := &recsys.ClassicsPool{}
	n, err := cp.Refresh(ctx, pool, time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, 3, n)
	items, at := cp.Snapshot()
	require.False(t, at.IsZero())
	got := map[int64]recsys.ClassicCandidate{}
	for _, it := range items {
		got[it.WorkID] = it
	}
	require.Contains(t, got, flSF)
	require.Contains(t, got, flDet)
	require.Contains(t, got, lib)
	require.Equal(t, "fantlab", got[flSF].Source)
	require.InDelta(t, 9.0, got[flSF].Rating, 0.01)
	require.Equal(t, 1965, got[flSF].Year)
	require.Equal(t, []string{"sf"}, got[flSF].Genres)
	require.Equal(t, "ru", got[flSF].Lang)
	require.Equal(t, "librate", got[lib].Source)
	require.InDelta(t, 5, got[lib].Rating, 0.01)
	require.Equal(t, 5, got[lib].Editions)
	// Порядок пула — по базовому весу: LIBRATE 5 при 5 изданиях (4.625) ниже Фантлаба 9.4 (4.7), выше 9.0 (4.5).
	require.Equal(t, []int64{flDet, lib, flSF}, []int64{items[0].WorkID, items[1].WorkID, items[2].WorkID})

	// Пользователь любит sf и русский оригинал → «Фантлаб sf» выше.
	p := &recsys.Profile{Genres: map[string]float64{"sf": 3}, Langs: map[string]float64{"ru": 3}, Signals: map[int64]*recsys.WorkSignals{}}
	rule := recsys.ForgottenClassic{Pool: cp}
	cands, err := rule.Candidates(ctx, pool, p, 10)
	require.NoError(t, err)
	require.Len(t, cands, 3)
	require.Equal(t, flSF, cands[0].WorkID)
	r := cands[0].Reason
	require.Equal(t, recsys.ReasonForgottenClassic, r.Kind)
	require.Equal(t, 1965, r.Year)
	require.Equal(t, "fantlab", r.Source)
	require.InDelta(t, 9.0, r.Rating, 0.01)
	// Своё (скрытое из ленты) — нет.
	p.Signals[flSF] = &recsys.WorkSignals{WorkID: flSF, Dismissed: true}
	cands, err = rule.Candidates(ctx, pool, p, 10)
	require.NoError(t, err)
	require.Len(t, cands, 2)
	// Пустой пул — пусто; без пула — пусто.
	empty := recsys.ForgottenClassic{Pool: &recsys.ClassicsPool{}}
	cands, err = empty.Candidates(ctx, pool, p, 10)
	require.NoError(t, err)
	require.Empty(t, cands)
	cands, err = recsys.ForgottenClassic{}.Candidates(ctx, pool, p, 10)
	require.NoError(t, err)
	require.Empty(t, cands)

	// Лента через DefaultClassics: карточки с причиной и исключениями.
	recsys.DefaultClassics.Set(items, at)
	t.Cleanup(func() { recsys.DefaultClassics.Set(nil, time.Time{}) })
	cards, err := recsys.NewFeed(pool).Section(ctx, recsys.SectionForgottenClassic, me, recsys.Exclusions{Langs: []string{"en"}}, 10)
	require.NoError(t, err)
	require.Empty(t, cards, "у me нет сигналов — профиль пуст, секция пуста")
	f.exec(`INSERT INTO book_ratings (user_id, work_id, rating) VALUES ($1,$2,5)`, me, comp)
	cards, err = recsys.NewFeed(pool).Section(ctx, recsys.SectionForgottenClassic, me, recsys.Exclusions{Langs: []string{"en"}}, 10)
	require.NoError(t, err)
	require.Len(t, cards, 1, "английские издания скрыты")
	require.Equal(t, flSF, cards[0].WorkID)
	require.Equal(t, 1965, cards[0].Reasons[0].Year)
}

// TestFeedRelated — «раз понравилось — вот следующая»: карточки правил,
// основание которых — оценённая работа; чужое основание — нет.
func TestFeedRelated(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	f := newFixture(t, ctx, pool)
	me := f.user("me@example.com")
	a := f.author("Автор")
	cycle := f.series("Цикл", nil)
	mk := func(title string, no *int, year *int) int64 {
		var sid *int64
		if no != nil {
			sid = lp(cycle)
		}
		w := f.work(title, year, sid)
		if no != nil {
			f.exec(`UPDATE works SET ser_no = $2 WHERE id = $1`, w, *no)
		}
		e := f.edition(w, nil, nil)
		f.exec(`INSERT INTO book_authors (book_id, author_id) VALUES ($1,$2)`, e, a)
		return w
	}
	w1 := mk("Том 1", ip(1), ip(1990))
	w2 := mk("Том 2", ip(2), ip(1991))
	w3 := mk("Том 3", ip(3), ip(1992))
	other := mk("Другая", nil, ip(1980))

	// Оценил «Том 1» на 5 → дальше в цикле «Том 2»; автор понравился → по
	// хронологии после 1990 — тот же «Том 2» (одна карточка, без повтора).
	f.exec(`INSERT INTO book_ratings (user_id, work_id, rating) VALUES ($1,$2,5)`, me, w1)
	feed := recsys.NewFeed(pool)
	items, err := feed.Related(ctx, me, w1, recsys.Exclusions{}, 3)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, w2, items[0].WorkID)
	require.Equal(t, recsys.ReasonNextInSeries, items[0].Reasons[0].Kind)
	require.Equal(t, w1, items[0].Reasons[0].WorkID)

	// По другой работе (не основание) — пусто.
	items, err = feed.Related(ctx, me, other, recsys.Exclusions{}, 3)
	require.NoError(t, err)
	require.Empty(t, items)
	_, _ = w3, other
}
