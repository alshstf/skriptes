package books_test

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/skriptes/skriptes/backend/internal/inpx/inpxtest"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestPresets — готовые подборки (#389): следующая книга начатой серии,
// непрочитанное у подписанных и прочитанных трижды авторов (по известности,
// без скрытого), прочитанное в этом году, экранизации этого и следующего года.
func TestPresets(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	mgr, addr, key := startMeilisearchAddr(t, ctx)
	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr, MeiliURL: addr, MeiliAPIKey: key})
	list := []inpxtest.Book{
		{LibID: "1", Title: "Дюна", Series: "Хроники Дюны", SerNo: 1, Authors: []string{"Герберт,Фрэнк"}, Lang: "ru"},
		{LibID: "2", Title: "Мессия Дюны", Series: "Хроники Дюны", SerNo: 2, Authors: []string{"Герберт,Фрэнк"}, Lang: "ru"},
		{LibID: "3", Title: "Дети Дюны", Series: "Хроники Дюны", SerNo: 3, Authors: []string{"Герберт,Фрэнк"}, Lang: "ru"},
		{LibID: "4", Title: "Бог-император Дюны", Series: "Хроники Дюны", SerNo: 4, Authors: []string{"Герберт,Фрэнк"}, Lang: "ru"},
		{LibID: "5", Title: "Хоббит", Authors: []string{"Толкин,Джон"}, Lang: "ru"},
		{LibID: "6", Title: "Сильмариллион", Authors: []string{"Толкин,Джон"}, Lang: "ru"},
		{LibID: "7", Title: "Братство Кольца", Authors: []string{"Толкин,Джон"}, Lang: "ru"},
		{LibID: "8", Title: "Дети Хурина", Authors: []string{"Толкин,Джон"}, Lang: "ru"},
		{LibID: "9", Title: "Основание", Authors: []string{"Азимов,Айзек"}, Lang: "ru"},
		{LibID: "10", Title: "Я, робот", Authors: []string{"Азимов,Айзек"}, Lang: "ru"},
		{LibID: "11", Title: "The Gods Themselves", Authors: []string{"Азимов,Айзек"}, Lang: "en"},
		{LibID: "12", Title: "Чужая книга", Authors: []string{"Чужой,Автор"}, Lang: "ru"},
	}
	path, err := inpxtest.WriteINPX(t.TempDir(), "lib.inpx", list)
	require.NoError(t, err)
	_, err = imp.Run(ctx, path)
	require.NoError(t, err)

	work := func(title string) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `SELECT work_id FROM books WHERE title = $1`, title).Scan(&id))
		return id
	}
	book := func(title string) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM books WHERE title = $1`, title).Scan(&id))
		return id
	}
	_, err = pool.Exec(ctx, `UPDATE works w SET series_id = b.series_id, ser_no = b.ser_no FROM books b WHERE b.work_id = w.id`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE works SET fantlab_marks = 5000, wd_sitelinks = 60 WHERE title = 'Основание'`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE works SET fantlab_marks = 300 WHERE title = 'Я, робот'`)
	require.NoError(t, err)
	_, err = imp.RebuildWorksIndex(ctx)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO app_settings (key, value) VALUES ($1, 'true'::jsonb)`, importer.WorksIndexSyncedFlagKey())
	require.NoError(t, err)
	require.NoError(t, imp.ConfigureWorksIndex(ctx))

	var userID int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (email, display_name, password_hash, role)
		VALUES ('p@e.com','P','x','user') RETURNING id`).Scan(&userID))
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	read := func(title string, at time.Time) {
		_, err := pool.Exec(ctx, `INSERT INTO reads (user_id, book_id, completed_at) VALUES ($1, $2, $3)`, userID, book(title), at)
		require.NoError(t, err)
	}
	read("Дюна", time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC))
	read("Мессия Дюны", time.Date(2026, 4, 2, 10, 0, 0, 0, time.UTC))
	read("Хоббит", time.Date(2025, 12, 30, 10, 0, 0, 0, time.UTC))
	read("Сильмариллион", time.Date(2025, 11, 1, 10, 0, 0, 0, time.UTC))
	read("Братство Кольца", time.Date(2026, 1, 5, 10, 0, 0, 0, time.UTC))
	var asimov int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM authors WHERE last_name = 'Азимов'`).Scan(&asimov))
	_, err = pool.Exec(ctx, `INSERT INTO favorite_authors (user_id, author_id) VALUES ($1, $2)`, userID, asimov)
	require.NoError(t, err)
	for _, a := range []struct {
		title string
		year  int
	}{{"Дети Дюны", 2027}, {"Чужая книга", 2026}, {"Хоббит", 2030}} {
		_, err = pool.Exec(ctx, `INSERT INTO book_adaptations (book_id, provider, ext_id, title, year)
			VALUES ($1, 'wikidata', $2, $3, $4)`, book(a.title), "Q"+a.title, "Фильм по «"+a.title+"»", a.year)
		require.NoError(t, err)
	}

	svc := books.New(pool, mgr, nil)
	p := books.PresetParams{UserID: userID, Now: now}
	titlesOf := func(items []books.ListItem) []string {
		out := make([]string, 0, len(items))
		for _, it := range items {
			out = append(out, it.Title)
		}
		return out
	}
	require.Eventually(t, func() bool {
		res, err := svc.PresetWorks(ctx, books.PresetUnfinishedSeries, p)
		return err == nil && len(res.Items) == 1
	}, 30*time.Second, 200*time.Millisecond, "фильтр по id применился")

	res, err := svc.PresetWorks(ctx, books.PresetUnfinishedSeries, p)
	require.NoError(t, err)
	require.Equal(t, []string{"Дети Дюны"}, titlesOf(res.Items), "следующая после прочитанной № 2")
	require.Equal(t, "в серии прочитано 2 из 4", res.Notes[work("Дети Дюны")])

	res, err = svc.PresetWorks(ctx, books.PresetAuthorsUnread, p)
	require.NoError(t, err)
	got := titlesOf(res.Items)
	require.ElementsMatch(t, []string{"Основание", "Я, робот", "The Gods Themselves", "Дети Хурина"}, got,
		"подписка на Азимова и трижды прочитанный Толкин; чужой автор и прочитанное — нет")
	require.Equal(t, []string{"Основание", "Я, робот"}, got[:2], "по известности")
	hidden := p
	hidden.ExcludeLangs = []string{"en"}
	res, err = svc.PresetWorks(ctx, books.PresetAuthorsUnread, hidden)
	require.NoError(t, err)
	require.NotContains(t, titlesOf(res.Items), "The Gods Themselves", "скрытый язык не предлагаем")

	res, err = svc.PresetWorks(ctx, books.PresetReadThisYear, hidden)
	require.NoError(t, err)
	require.Equal(t, []string{"Мессия Дюны", "Дюна", "Братство Кольца"}, titlesOf(res.Items), "свежие сверху, прошлый год — нет")
	require.Equal(t, "прочитано 12 марта", res.Notes[work("Дюна")])

	res, err = svc.PresetWorks(ctx, books.PresetAdaptations, p)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"Дети Дюны", "Чужая книга"}, titlesOf(res.Items), "2030 — не этот и не следующий год")
	require.Equal(t, "2027 · «Фильм по «Дети Дюны»»", res.Notes[work("Дети Дюны")])

	presets, err := svc.Presets(ctx, p)
	require.NoError(t, err)
	counts := map[string]int{}
	for _, pr := range presets {
		counts[pr.Key] = pr.Count
		require.NotEmpty(t, pr.Title)
	}
	require.Equal(t, map[string]int{books.PresetUnfinishedSeries: 1, books.PresetAuthorsUnread: 4,
		books.PresetReadThisYear: 3, books.PresetAdaptations: 2}, counts)
	require.Equal(t, "Прочитано в 2026 году", presets[2].Title)

	_, err = svc.PresetWorks(ctx, "nope", p)
	require.ErrorIs(t, err, books.ErrUnknownPreset)

	// Карточки лауреатов премий: порядок запроса, повторы схлопнуты, скрытое — нет.
	ids := []int64{work("The Gods Themselves"), work("Дюна"), work("The Gods Themselves")}
	items, err := svc.VisibleWorks(ctx, ids, p)
	require.NoError(t, err)
	require.Equal(t, []string{"The Gods Themselves", "Дюна"}, titlesOf(items))
	items, err = svc.VisibleWorks(ctx, ids, hidden)
	require.NoError(t, err)
	require.Equal(t, []string{"Дюна"}, titlesOf(items))
}
