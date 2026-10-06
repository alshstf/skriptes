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

// TestSearch_PopularMatches — известные совпадения (#401): на «мастер» первая
// страница /books — «Мастер и Маргарита» сверху (десятки книг «Мастер» иначе
// занимали её целиком), дальше обычная выдача без неё; прокрутка без потерь и
// повторов; «Мастерская» (совпадение по началу слова) не закрепляется;
// подсказки тоже показывают «Мастера и Маргариту».
func TestSearch_PopularMatches(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	mgr, addr, key := startMeilisearchAddr(t, ctx)
	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr, MeiliURL: addr, MeiliAPIKey: key})
	list := []inpxtest.Book{
		{LibID: "860001", Title: "Мастер и Маргарита", Authors: []string{"Булгаков,Михаил"}, Lang: "ru"},
		{LibID: "860002", Title: "Мастерская", Authors: []string{"Мастеров,Иван"}, Lang: "ru"},
		{LibID: "860003", Title: "Сад", Authors: []string{"Садов,Пётр"}, Lang: "ru"},
	}
	for i, a := range []string{"Первый", "Второй", "Третий", "Четвёртый", "Пятый", "Шестой"} {
		list = append(list, inpxtest.Book{LibID: "86010" + string(rune('0'+i)), Title: "Мастер",
			Authors: []string{a + ",Автор"}, Lang: "ru"})
	}
	path, err := inpxtest.WriteINPX(t.TempDir(), "lib.inpx", list)
	require.NoError(t, err)
	_, err = imp.Run(ctx, path)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE works SET fantlab_marks = 20000, wd_sitelinks = 78
		WHERE title IN ('Мастер и Маргарита', 'Мастерская')`)
	require.NoError(t, err)
	_, err = imp.RebuildWorksIndex(ctx)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO app_settings (key, value) VALUES ($1, 'true'::jsonb)`, importer.WorksIndexSyncedFlagKey())
	require.NoError(t, err)
	require.NoError(t, imp.ConfigureWorksIndex(ctx))

	svc := books.New(pool, mgr, nil)
	const total = 8 // шесть «Мастер», «Мастер и Маргарита», «Мастерская»
	require.Eventually(t, func() bool {
		res, err := svc.ListWorks(ctx, books.ListParams{Query: "мастер", Limit: 3})
		return err == nil && len(res.Items) > 0 && res.Items[0].Title == "Мастер и Маргарита"
	}, 30*time.Second, 200*time.Millisecond, "фильтр по id применился, закрепление работает")

	res, err := svc.ListWorks(ctx, books.ListParams{Query: "мастер", Limit: 3})
	require.NoError(t, err)
	require.EqualValues(t, total, res.Total)
	require.Equal(t, "Мастер и Маргарита", res.Items[0].Title)
	require.Equal(t, "Мастер", res.Items[1].Title, "дальше — обычная выдача, точные совпадения первыми")
	require.Equal(t, "Мастер", res.Items[2].Title)

	// Прокрутка по одной работе — все восемь ровно по разу, закреплённая первой.
	seen := map[int64]int{}
	var first string
	for offset := 0; offset < total+2; offset++ {
		page, err := svc.ListWorks(ctx, books.ListParams{Query: "мастер", Limit: 1, Offset: offset})
		require.NoError(t, err)
		require.EqualValues(t, total, page.Total)
		for _, it := range page.Items {
			if offset == 0 {
				first = it.Title
			}
			seen[it.ID]++
		}
	}
	require.Equal(t, "Мастер и Маргарита", first)
	require.Len(t, seen, total, "без потерь")
	for id, n := range seen {
		require.Equal(t, 1, n, "без повторов (работа %d)", id)
	}

	// Сортировка или фильтр автора — без закрепления (как у запроса-автора).
	byYear, err := svc.ListWorks(ctx, books.ListParams{Query: "мастер", Limit: 3, Sort: "year"})
	require.NoError(t, err)
	require.EqualValues(t, total, byYear.Total)

	// Подсказки: «Мастер и Маргарита» в окне и наверху.
	sugg, err := svc.SuggestWorks(ctx, "мастер", 5, 0, nil, nil, false)
	require.NoError(t, err)
	require.NotEmpty(t, sugg)
	require.Equal(t, "Мастер и Маргарита", sugg[0].Title)
}
