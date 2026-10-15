package catalog_test

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/catalog"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestSuggest_FoldingAndFuzzy — #516: подсказки авторов/серий и поиск /authors
// терпят разночтения транслитерации (свёртка «ё/э/й») и опечатки (добор похожих
// после строгих: меньше правок, потом известнее). Видимость — как у строгих.
func TestSuggest_FoldingAndFuzzy(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)

	var collID, archID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	author := func(last, first string, renown int64) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO authors (last_name, first_name, normalized_name, renown) VALUES ($1, $2, lower($1 || ' ' || $2), $3) RETURNING id`,
			last, first, renown).Scan(&id))
		return id
	}
	series := func(title string) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO series (title, normalized_title) VALUES ($1, lower($1)) RETURNING id`, title).Scan(&id))
		return id
	}
	n := 0
	book := func(authorID int64, lang string, seriesID *int64) {
		n++
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, lang, series_id)
			VALUES ($1, $2, $3, 'f', 'fb2', 'Книга', 'книга', $4, $5) RETURNING id`,
			collID, archID, "L"+string(rune('a'+n)), lang, seriesID).Scan(&id))
		_, err := pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id, position) VALUES ($1, $2, 0)`, id, authorID)
		require.NoError(t, err)
	}

	ray := author("Брэдбери", "Рэй", 2368)
	malcolm := author("Брэдбери", "Малькольм", 256)
	asimov := author("Азимов", "Айзек", 2336)
	olga := author("Азимова", "Ольга", 0)
	hidden := author("Азимовер", "Джон", 900) // только англоязычные книги
	heinlein := author("Хайнлайн", "Роберт", 2121)
	potter := series("Гарри Поттер")
	book(ray, "ru", nil)
	book(ray, "ru", nil)
	book(malcolm, "ru", nil)
	book(asimov, "ru", nil)
	book(olga, "ru", nil)
	book(hidden, "en", nil)
	book(heinlein, "ru", &potter)
	book(heinlein, "ru", &potter)

	svc := catalog.New(pool)
	ids := func(q string, excludeLangs ...string) []int64 {
		res, err := svc.SuggestAuthors(ctx, q, 5, nil, excludeLangs, false)
		require.NoError(t, err)
		out := make([]int64, 0, len(res))
		for _, a := range res {
			out = append(out, a.ID)
		}
		return out
	}

	// Свёртка «э/й»: строгие совпадения, порядок — как раньше (больше книг выше).
	require.Equal(t, []int64{ray, malcolm}, ids("бредбери"))
	require.Equal(t, []int64{ray}, ids("рей бредбери"))
	require.Equal(t, []int64{heinlein}, ids("хаинлаин"))
	// Опечатка: похожие, при равном числе правок — известнее выше.
	require.Equal(t, []int64{ray, malcolm}, ids("брадбери"))
	require.Equal(t, []int64{ray}, ids("рэй брадбери"), "второе слово отсекает тёзку")
	require.Equal(t, []int64{asimov, hidden, olga}, ids("азимав"))
	require.Equal(t, []int64{asimov, olga}, ids("азимав", "en"), "автор без видимых книг не подсказывается")
	// Строгие — выше похожих: «азимов» — вхождение у троих, похожих нет.
	require.Equal(t, []int64{asimov, olga, hidden}, ids("азимов"))
	// Короткие слова опечаток не терпят.
	require.Empty(t, ids("кот"))

	sers, err := svc.SuggestSeries(ctx, "гари потер", 5, nil, nil, false)
	require.NoError(t, err)
	require.Len(t, sers, 1)
	require.Equal(t, potter, sers[0].ID)
	require.Equal(t, 2, sers[0].BookCount)

	// /authors: точных совпадений по имени нет — похожие с пометкой; есть — как раньше.
	list, err := svc.ListAuthorsFiltered(ctx, catalog.AuthorListParams{Query: "азимав"})
	require.NoError(t, err)
	require.True(t, list.Fuzzy)
	require.Equal(t, 3, list.Total)
	require.Equal(t, asimov, list.Items[0].ID, "сортировка списка — известность")
	list, err = svc.ListAuthorsFiltered(ctx, catalog.AuthorListParams{Query: "азимав", ExcludeLangs: []string{"en"}})
	require.NoError(t, err)
	require.Equal(t, 2, list.Total)
	list, err = svc.ListAuthorsFiltered(ctx, catalog.AuthorListParams{Query: "азим"})
	require.NoError(t, err)
	require.False(t, list.Fuzzy)
	require.Equal(t, 3, list.Total)
	list, err = svc.ListAuthorsFiltered(ctx, catalog.AuthorListParams{Query: "брэдбе"})
	require.NoError(t, err)
	require.False(t, list.Fuzzy)
	require.Equal(t, 2, list.Total)
	list, err = svc.ListAuthorsFiltered(ctx, catalog.AuthorListParams{Query: "щщщщщщ"})
	require.NoError(t, err)
	require.True(t, list.Fuzzy)
	require.Zero(t, list.Total)
	_, err = svc.AuthorFacets(ctx, catalog.AuthorListParams{Query: "азимав"})
	require.NoError(t, err)
}
