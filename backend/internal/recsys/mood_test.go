package recsys_test

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/genres"
	"github.com/skriptes/skriptes/backend/internal/recsys"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestMoodTable — mood-genres.json: все настроения на месте, коды — из словаря
// жанров, веса в (0, 1]; «короткое» — с потолком размера.
func TestMoodTable(t *testing.T) {
	dict, err := genres.Dictionary()
	require.NoError(t, err)
	known := map[string]bool{}
	for _, e := range dict {
		known[e.Code] = true
	}
	for _, m := range recsys.AllMoods {
		spec, ok := recsys.MoodSpecOf(m)
		require.True(t, ok, m)
		require.NotEmpty(t, spec.Genres, m)
		for code, w := range spec.Genres {
			require.Truef(t, known[code], "%s: жанра %q нет в genres/dictionary.json", m, code)
			require.Truef(t, w > 0 && w <= 1, "%s: вес %q = %g", m, code, w)
		}
	}
	short, _ := recsys.MoodSpecOf(recsys.MoodShort)
	require.Positive(t, short.MaxBytes)
	_, ok := recsys.MoodSpecOf(recsys.Mood("nope"))
	require.False(t, ok)
	require.True(t, recsys.IsMood("funny"))
	require.False(t, recsys.IsMood(""))
	require.False(t, recsys.IsMood("sad"))
}

// TestMoodMatch — совпадение: максимум по жанрам, «короткое» — по размеру,
// неизвестное настроение — 0.
func TestMoodMatch(t *testing.T) {
	require.Equal(t, 1.0, recsys.MoodMatch(recsys.MoodFunny, []string{"sf", "humor_prose"}, 0))
	require.Equal(t, 0.5, recsys.MoodMatch(recsys.MoodFunny, []string{"vaudeville"}, 0))
	require.Equal(t, 0.0, recsys.MoodMatch(recsys.MoodFunny, []string{"sf"}, 0))
	require.Equal(t, 0.0, recsys.MoodMatch(recsys.MoodFunny, nil, 0))
	// «Короткое»: по размеру — 1 при любом жанре; большое — только по жанру.
	require.Equal(t, 1.0, recsys.MoodMatch(recsys.MoodShort, []string{"sf"}, 100_000))
	require.Equal(t, 0.0, recsys.MoodMatch(recsys.MoodShort, []string{"sf"}, 900_000))
	require.Equal(t, 1.0, recsys.MoodMatch(recsys.MoodShort, []string{"short_story"}, 900_000))
	require.Equal(t, 0.0, recsys.MoodMatch(recsys.MoodShort, []string{"sf"}, 0), "размер неизвестен — не короткое")
	require.Equal(t, 0.0, recsys.MoodMatch("", []string{"humor_prose"}, 0))
	require.Equal(t, 0.0, recsys.MoodMatch("nope", []string{"humor_prose"}, 0))
}

// TestFeedMood — настроение в ленте (recsys/03, 3.4): секции правил
// пересортировываются (подходящее вперёд, прежний порядок внутри), поднятая
// карточка несёт Reason.Mood; «Забытая классика» фильтруется; «На вечер» —
// тоже пересортировка; без настроения — как раньше.
func TestFeedMood(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	f := newFixture(t, ctx, pool)
	me := f.user("me@example.com")
	gSF, gHumor, gHorror, gShort := f.genre("sf"), f.genre("humor_prose"), f.genre("sf_horror"), f.genre("short_story")

	mk := func(author int64, title string, year int, size int64, editions int, gs ...int64) int64 {
		w := f.work(title, ip(year), nil)
		f.exec(`UPDATE works SET edition_count = $2, primary_author_id = $3 WHERE id = $1`, w, editions, author)
		e := f.edition(w, sp("ru"), nil)
		f.exec(`UPDATE books SET size_bytes = $2, lang = 'ru' WHERE id = $1`, e, size)
		f.exec(`INSERT INTO book_authors (book_id, author_id) VALUES ($1,$2)`, e, author)
		for _, g := range gs {
			f.exec(`INSERT INTO book_genres (book_id, genre_id) VALUES ($1,$2)`, e, g)
		}
		return w
	}
	// Три автора, у каждого прочитанная (оценка) и непрочитанная работа.
	// Близость: A (5) > B (4) > C (4, старее) — таков порядок «Автор, которого
	// вы дочитали» без настроения.
	a, b, c := f.author("Серьёзный"), f.author("Весёлый"), f.author("Краткий")
	readA, readB, readC := mk(a, "A1", 1990, 500_000, 1, gSF), mk(b, "B1", 1990, 500_000, 1, gHumor), mk(c, "C1", 1990, 500_000, 1, gSF)
	nextA := mk(a, "A2 sf", 2000, 500_000, 1, gSF)
	nextB := mk(b, "B2 юмор", 2000, 500_000, 1, gHumor)
	nextC := mk(c, "C2 короткая", 2000, 50_000, 1, gSF)
	f.exec(`INSERT INTO book_ratings (user_id, work_id, rating, rated_at) VALUES ($1,$2,5,now()), ($1,$3,4,now()), ($1,$4,4,now() - interval '1 day')`,
		me, readA, readB, readC)

	feed := recsys.NewFeed(pool)
	ids := func(items []recsys.Item) []int64 {
		out := make([]int64, 0, len(items))
		for _, it := range items {
			out = append(out, it.WorkID)
		}
		return out
	}
	items, err := feed.Section(ctx, recsys.SectionAuthorFinished, me, recsys.Exclusions{}, 10, "")
	require.NoError(t, err)
	require.Equal(t, []int64{nextA, nextB, nextC}, ids(items), "без настроения — по близости")
	for _, it := range items {
		require.Empty(t, it.Reasons[0].Mood)
	}

	// «Смешное»: юмор вперёд, остальные в прежнем порядке; поднятая — с Mood.
	items, err = feed.Section(ctx, recsys.SectionAuthorFinished, me, recsys.Exclusions{}, 10, recsys.MoodFunny)
	require.NoError(t, err)
	require.Equal(t, []int64{nextB, nextA, nextC}, ids(items))
	require.Equal(t, recsys.MoodFunny, items[0].Reasons[0].Mood)
	require.Empty(t, items[1].Reasons[0].Mood)
	require.Equal(t, recsys.ReasonAuthorFinished, items[0].Reasons[0].Kind, "причина правила остаётся")

	// «Короткое»: по размеру издания.
	items, err = feed.Section(ctx, recsys.SectionAuthorFinished, me, recsys.Exclusions{}, 10, recsys.MoodShort)
	require.NoError(t, err)
	require.Equal(t, []int64{nextC, nextA, nextB}, ids(items))
	require.Equal(t, recsys.MoodShort, items[0].Reasons[0].Mood)

	// Ничего не подходит — порядок прежний, без Mood.
	items, err = feed.Section(ctx, recsys.SectionAuthorFinished, me, recsys.Exclusions{}, 10, recsys.MoodDark)
	require.NoError(t, err)
	require.Equal(t, []int64{nextA, nextB, nextC}, ids(items))
	require.Empty(t, items[0].Reasons[0].Mood)

	// «Забытая классика» — фильтр пула (жанры и размер уже в пуле).
	horror := mk(a, "Ужас 1950", 1950, 500_000, 5, gHorror)
	classic := mk(a, "Классика 1950", 1950, 500_000, 5, gSF)
	tiny := mk(a, "Рассказ 1950", 1950, 20_000, 5, gShort)
	pool0 := []recsys.ClassicCandidate{
		{WorkID: classic, Year: 1950, Rating: 9.4, Source: "fantlab", Genres: []string{"sf"}, Bytes: 500_000},
		{WorkID: horror, Year: 1950, Rating: 9.0, Source: "fantlab", Genres: []string{"sf_horror"}, Bytes: 500_000},
		{WorkID: tiny, Year: 1950, Rating: 8.5, Source: "fantlab", Genres: []string{"short_story"}, Bytes: 20_000},
	}
	recsys.DefaultClassics.Set(pool0, time.Now())
	t.Cleanup(func() { recsys.DefaultClassics.Set(nil, time.Time{}) })
	items, err = feed.Section(ctx, recsys.SectionForgottenClassic, me, recsys.Exclusions{}, 10, "")
	require.NoError(t, err)
	require.Equal(t, []int64{classic, horror, tiny}, ids(items))
	items, err = feed.Section(ctx, recsys.SectionForgottenClassic, me, recsys.Exclusions{}, 10, recsys.MoodDark)
	require.NoError(t, err)
	require.Equal(t, []int64{horror}, ids(items), "мрачное — только ужасы")
	require.Equal(t, recsys.MoodDark, items[0].Reasons[0].Mood)
	items, err = feed.Section(ctx, recsys.SectionForgottenClassic, me, recsys.Exclusions{}, 10, recsys.MoodShort)
	require.NoError(t, err)
	require.Equal(t, []int64{tiny}, ids(items), "короткое — по размеру и жанру")
	items, err = feed.Section(ctx, recsys.SectionForgottenClassic, me, recsys.Exclusions{}, 10, recsys.MoodFunny)
	require.NoError(t, err)
	require.Empty(t, items, "смешного в пуле нет — секция пуста")

	// Пул Refresh кладёт размер самого маленького живого издания.
	f.exec(`UPDATE authors SET renown = 100 WHERE id = $1`, a)
	f.exec(`UPDATE works SET fantlab_midmark = 9.0, fantlab_marks = 500 WHERE id = $1`, tiny)
	cp := &recsys.ClassicsPool{}
	_, err = cp.Refresh(ctx, pool, time.Now())
	require.NoError(t, err)
	got, _ := cp.Snapshot()
	var found bool
	for _, it := range got {
		if it.WorkID == tiny {
			found = true
			require.Equal(t, int64(20_000), it.Bytes)
		}
	}
	require.True(t, found, "рассказ в пуле")

	// «На вечер»: тоже пересортировка под настроение.
	tonight, err := feed.Tonight(ctx, me, 600, recsys.Speed{}, recsys.Exclusions{}, 10, "")
	require.NoError(t, err)
	require.NotEmpty(t, tonight)
	tonightFunny, err := feed.Tonight(ctx, me, 600, recsys.Speed{}, recsys.Exclusions{}, 10, recsys.MoodFunny)
	require.NoError(t, err)
	require.Equal(t, nextB, tonightFunny[0].WorkID)
	require.Equal(t, recsys.MoodFunny, tonightFunny[0].Reasons[0].Mood)

	// ApplyMood напрямую: пустое настроение — как есть.
	cands := []recsys.Candidate{{WorkID: nextA}, {WorkID: nextB}}
	same, err := recsys.ApplyMood(ctx, pool, cands, "")
	require.NoError(t, err)
	require.Equal(t, cands, same)
}
