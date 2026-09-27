package books_test

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/catalog"
	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/skriptes/skriptes/backend/internal/inpx/inpxtest"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestSearch_YoFolding — #278: «е» и «ё» в поиске равны. «три мушкетера»
// находит «Три мушкетёра», короткое «еж» — «Ёжика», запрос с «ё» — тоже;
// подсказки и поиск авторов/серий в PG — так же. В выдаче — исходное
// написание. Свёрнутые поля включаются только после полного ресинка схемы v9.
func TestSearch_YoFolding(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	mgr := startMeilisearch(t, ctx)
	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr})
	path, err := inpxtest.WriteINPX(t.TempDir(), "lib.inpx", []inpxtest.Book{
		{LibID: "830001", Title: "Три мушкетёра", Authors: []string{"Дюма,Александр"}, Lang: "ru"},
		{LibID: "830002", Title: "Ёж", Authors: []string{"Козлов,Сергей"}, Series: "Сказки про Ёжика", Lang: "ru"},
		{LibID: "830003", Title: "Семнадцать мгновений весны", Authors: []string{"Семёнов,Юлиан"}, Lang: "ru"},
		{LibID: "830004", Title: "Мастер и Маргарита", Authors: []string{"Булгаков,Михаил"}, Lang: "ru"},
	})
	require.NoError(t, err)
	_, err = imp.Run(ctx, path)
	require.NoError(t, err)

	// Как на старте после разового ресинка: флаг схемы → поиск по свёрнутым полям.
	_, err = pool.Exec(ctx, `INSERT INTO app_settings (key, value) VALUES ($1, 'true'::jsonb)`, importer.WorksIndexSyncedFlagKey())
	require.NoError(t, err)
	require.NoError(t, imp.ConfigureWorksIndex(ctx))

	svc := books.New(pool, mgr, nil)
	titles := func(q string) []string {
		res, err := svc.ListWorks(ctx, books.ListParams{Query: q, Limit: 10})
		require.NoError(t, err)
		out := make([]string, 0, len(res.Items))
		for _, it := range res.Items {
			out = append(out, it.Title)
		}
		return out
	}
	// Смена searchableAttributes применяется задачей Meili — ждём по запросу,
	// который находится ТОЛЬКО по свёрнутым полям: у двухбуквенного слова нет
	// опечаточной толерантности (а «мушкетера» нашлось бы и опечаткой).
	require.Eventually(t, func() bool { return len(titles("еж")) == 1 }, 30*time.Second, 200*time.Millisecond)
	require.Equal(t, []string{"Три мушкетёра"}, titles("три мушкетера"), "исходное написание в выдаче")
	require.Equal(t, []string{"Три мушкетёра"}, titles("мушкетёра"))
	require.Equal(t, []string{"Ёж"}, titles("еж"), "короткое слово без опечатки")
	require.Equal(t, []string{"Семнадцать мгновений весны"}, titles("семенов"), "по автору")
	require.Equal(t, []string{"Ёж"}, titles("ежика"), "по серии")

	sugg, err := svc.SuggestWorks(ctx, "мушкетера", 5, 0, nil, nil, false)
	require.NoError(t, err)
	require.NotEmpty(t, sugg)
	require.Equal(t, "Три мушкетёра", sugg[0].Title)

	cat := catalog.New(pool)
	authors, err := cat.SuggestAuthors(ctx, "семенов", 5, nil, nil, false)
	require.NoError(t, err)
	require.Len(t, authors, 1)
	require.Equal(t, "Семёнов Юлиан", authors[0].FullName)
	authors, err = cat.SuggestAuthors(ctx, "козлов", 5, nil, nil, false)
	require.NoError(t, err)
	require.Len(t, authors, 1)
	series, err := cat.SuggestSeries(ctx, "про ежика", 5, nil, nil, false)
	require.NoError(t, err)
	require.Len(t, series, 1)
	list, err := cat.ListAuthorsFiltered(ctx, catalog.AuthorListParams{Query: "семенов", Limit: 10})
	require.NoError(t, err)
	require.Len(t, list.Items, 1)
	require.Equal(t, "Семёнов Юлиан", list.Items[0].FullName)
}
