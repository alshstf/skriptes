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
	mgr, addr, key := startMeilisearchAddr(t, ctx)
	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr, MeiliURL: addr, MeiliAPIKey: key})
	path, err := inpxtest.WriteINPX(t.TempDir(), "lib.inpx", []inpxtest.Book{
		{LibID: "830001", Title: "Три мушкетёра", Authors: []string{"Дюма,Александр"}, Lang: "ru", Rating: 5},
		{LibID: "830002", Title: "Ёж", Authors: []string{"Козлов,Сергей"}, Series: "Сказки про Ёжика", Lang: "ru"},
		{LibID: "830003", Title: "Семнадцать мгновений весны", Authors: []string{"Семёнов,Юлиан"}, Lang: "ru"},
		{LibID: "830004", Title: "Мастер и Маргарита", Authors: []string{"Булгаков,Михаил"}, Lang: "ru"},
		// Тезка без «ё» и без рейтинга: при равном совпадении выше должна быть
		// известная книга, а не та, что написана через «е» (прод: Дюма был 40-м).
		{LibID: "830005", Title: "Три мушкетера", Authors: []string{"Филатов,Леонид"}, Lang: "ru"},
	})
	require.NoError(t, err)
	_, err = imp.Run(ctx, path)
	require.NoError(t, err)

	// Как на старте (runOnceWorksIndexSync): пересборка во временном индексе +
	// swap, потом флаг схемы и конфиг живого индекса (уже со свёрнутыми полями).
	n, err := imp.RebuildWorksIndex(ctx)
	require.NoError(t, err)
	require.Equal(t, 5, n)
	_, err = pool.Exec(ctx, `INSERT INTO app_settings (key, value) VALUES ($1, 'true'::jsonb)`, importer.WorksIndexSyncedFlagKey())
	require.NoError(t, err)
	require.NoError(t, imp.ConfigureWorksIndex(ctx))
	_, err = mgr.GetIndexWithContext(ctx, "works_rebuild")
	require.Error(t, err, "временный индекс удалён")

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
	require.Equal(t, []string{"Три мушкетёра", "Три мушкетера"}, titles("три мушкетера"),
		"исходное написание в выдаче; «ё» не проигрывает ранжирование")
	require.Equal(t, []string{"Три мушкетёра", "Три мушкетера"}, titles("мушкетёра"))
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

// TestSearch_AltTitlesAndLatinAuthors — #291: работа находится по названию
// другого издания и названию оригинала, автор — по латинскому имени из fb2
// перевода; совпадение в названии работы выше совпадения в альтернативном.
func TestSearch_AltTitlesAndLatinAuthors(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	mgr, addr, key := startMeilisearchAddr(t, ctx)
	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr, MeiliURL: addr, MeiliAPIKey: key})
	path, err := inpxtest.WriteINPX(t.TempDir(), "lib.inpx", []inpxtest.Book{
		{LibID: "840001", Title: "Хоббит, или Туда и обратно", Authors: []string{"Толкин,Джон"}, Lang: "ru"},
		{LibID: "840002", Title: "Дюна", Authors: []string{"Герберт,Фрэнк"}, Lang: "ru"},
		{LibID: "840003", Title: "Собака Баскервилей", Authors: []string{"Дойл,Артур"}, Lang: "ru"},
		{LibID: "840004", Title: "Hobbit Hole", Authors: []string{"Иванов,Иван"}, Lang: "ru"},
	})
	require.NoError(t, err)
	_, err = imp.Run(ctx, path)
	require.NoError(t, err)
	// Название оригинала и латинский автор приходят из fb2 (<src-title-info>).
	for lib, src := range map[string][2]string{
		"840001": {"The Hobbit", "tolkien john ronald reuel"},
		"840002": {"Dune", "herbert frank"},
		"840003": {"The Hound of the Baskervilles", "doyle arthur conan"},
	} {
		_, err := pool.Exec(ctx, `UPDATE books SET src_title = $2, src_author_normalized = $3 WHERE lib_id = $1`, lib, src[0], src[1])
		require.NoError(t, err)
	}

	_, err = imp.RebuildWorksIndex(ctx)
	require.NoError(t, err)
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
	require.Eventually(t, func() bool { return len(titles("dune herbert")) == 1 }, 30*time.Second, 200*time.Millisecond)
	require.Equal(t, []string{"Дюна"}, titles("dune herbert"), "оригинал + латинский автор")
	require.Equal(t, []string{"Собака Баскервилей"}, titles("doyle"), "латинское имя автора")
	require.Equal(t, []string{"Hobbit Hole", "Хоббит, или Туда и обратно"}, titles("hobbit"),
		"совпадение в названии работы выше совпадения в названии оригинала")
	require.Contains(t, titles("the hobbit"), "Хоббит, или Туда и обратно")
}

// TestSearch_AuthorQuery — #290: запрос — имя известного автора → сначала его
// работы, потом книги о нём; страницы стыкуются без потерь и повторов, плашка —
// только на первой. Книга с таким названием, известнее автора, остаётся книгой.
func TestSearch_AuthorQuery(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	mgr, addr, key := startMeilisearchAddr(t, ctx)
	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr, MeiliURL: addr, MeiliAPIKey: key})
	path, err := inpxtest.WriteINPX(t.TempDir(), "lib.inpx", []inpxtest.Book{
		{LibID: "850001", Title: "Лев Толстой: Бегство из рая", Authors: []string{"Басинский,Павел"}, Lang: "ru"},
		{LibID: "850002", Title: "Война и мир", Authors: []string{"Толстой,Лев,Николаевич"}, Lang: "ru", Rating: 5},
		{LibID: "850003", Title: "Анна Каренина", Authors: []string{"Толстой,Лев,Николаевич"}, Lang: "ru", Rating: 4},
		// Его же тексты со словом-фамилией в названии: по релевантности шли бы
		// первыми, по известности — после «Войны и мира» (скриншот прода 2026-10-05).
		{LibID: "850009", Title: "Лев Толстой: Исповедь", Authors: []string{"Толстой,Лев,Николаевич"}, Lang: "ru"},
		{LibID: "850010", Title: "Толстой и Достоевский", Authors: []string{"Достоевский,Федор", "Толстой,Лев,Николаевич"}, Lang: "ru"},
		{LibID: "850004", Title: "Кармен", Authors: []string{"Мериме,Проспер"}, Lang: "ru", Rating: 5},
		{LibID: "850005", Title: "Песни", Authors: []string{"Кармен,Анна"}, Lang: "ru"},
		// Малоизвестный тёзка: в первую часть не попадает (меньше трети известности Льва).
		{LibID: "850006", Title: "Записки", Authors: []string{"Толстой,Никита"}, Lang: "ru"},
		// «doyle»: Конан Дойль узнаётся по латинскому имени из fb2 переводов, хотя
		// у Роба Дойла латиница прямо в имени автора.
		{LibID: "850007", Title: "Этюд в багровых тонах", Authors: []string{"Дойль,Артур,Конан"}, Lang: "ru"},
		{LibID: "850008", Title: "Here Are the Young Men", Authors: []string{"Doyle,Rob"}, Lang: "en"},
	})
	require.NoError(t, err)
	_, err = imp.Run(ctx, path)
	require.NoError(t, err)
	// «Кармен» Мериме — известная книга; автор Кармен известен меньше её.
	_, err = pool.Exec(ctx, `UPDATE works SET fantlab_marks = 20000 WHERE title = 'Кармен'`)
	require.NoError(t, err)
	// Латинский автор оригинала приходит из fb2 перевода — до сборки индекса.
	_, err = pool.Exec(ctx, `UPDATE books SET src_author_normalized = 'doyle arthur conan' WHERE lib_id = '850007'`)
	require.NoError(t, err)
	_, err = imp.RebuildWorksIndex(ctx)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO app_settings (key, value) VALUES ($1, 'true'::jsonb)`, importer.WorksIndexSyncedFlagKey())
	require.NoError(t, err)
	require.NoError(t, imp.ConfigureWorksIndex(ctx))
	_, err = catalog.RecomputeAuthorStats(ctx, pool)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE authors SET renown = CASE
		WHEN last_name = 'Толстой' AND first_name = 'Лев' THEN 2000
		WHEN last_name = 'Толстой' THEN 300
		WHEN last_name = 'Дойль' THEN 1300
		WHEN last_name = 'Кармен' THEN 200 ELSE 0 END`)
	require.NoError(t, err)

	svc := books.New(pool, mgr, nil)
	require.Eventually(t, func() bool {
		res, err := svc.ListWorks(ctx, books.ListParams{Query: "толстой", Limit: 10})
		return err == nil && res.Total == 6
	}, 30*time.Second, 200*time.Millisecond)

	res, err := svc.ListWorks(ctx, books.ListParams{Query: "толстой", Limit: 10})
	require.NoError(t, err)
	got := make([]string, 0, len(res.Items))
	for _, it := range res.Items {
		got = append(got, it.Title)
	}
	require.Equal(t, "Война и мир", got[0], "сначала работы автора по известности")
	require.Equal(t, "Анна Каренина", got[1])
	require.ElementsMatch(t, []string{"Лев Толстой: Исповедь", "Толстой и Достоевский"}, got[2:4],
		"его тексты со словом-фамилией в названии — после известных")
	require.ElementsMatch(t, []string{"Лев Толстой: Бегство из рая", "Записки"}, got[4:],
		"потом остальное: книга о нём и малоизвестный тёзка")
	require.Len(t, res.MatchedAuthors, 1)
	require.Equal(t, "Толстой Лев Николаевич", res.MatchedAuthors[0].FullName)
	require.Equal(t, 4, res.MatchedAuthors[0].BookCount)

	// Подсказки (hero, Cmd+K) — так же: известные работы автора первыми.
	sugg, err := svc.SuggestWorks(ctx, "толстой", 5, 0, nil, nil, false)
	require.NoError(t, err)
	require.Len(t, sugg, 5)
	require.Equal(t, "Война и мир", sugg[0].Title)
	require.Equal(t, "Анна Каренина", sugg[1].Title)
	for _, it := range sugg[:4] {
		require.NotEqual(t, "Лев Толстой: Бегство из рая", it.Title, "книга о нём — после его работ")
	}

	// По одной работе на страницу — тот же порядок, без потерь и повторов.
	var paged []string
	for offset := 0; offset < 7; offset++ {
		page, err := svc.ListWorks(ctx, books.ListParams{Query: "толстой", Limit: 1, Offset: offset})
		require.NoError(t, err)
		require.EqualValues(t, 6, page.Total)
		if offset > 0 {
			require.Empty(t, page.MatchedAuthors, "плашка — только на первой странице")
		}
		for _, it := range page.Items {
			paged = append(paged, it.Title)
		}
	}
	require.Equal(t, got, paged)

	// «Лев Толстой» — тот же автор; «толстой война» — не имя, обычный поиск.
	res, err = svc.ListWorks(ctx, books.ListParams{Query: "лев толстой", Limit: 10})
	require.NoError(t, err)
	require.Len(t, res.MatchedAuthors, 1)
	res, err = svc.ListWorks(ctx, books.ListParams{Query: "толстой война", Limit: 10})
	require.NoError(t, err)
	require.Empty(t, res.MatchedAuthors)

	// «doyle» — Конан Дойль по латинскому имени, его книга первой.
	res, err = svc.ListWorks(ctx, books.ListParams{Query: "doyle", Limit: 10})
	require.NoError(t, err)
	require.Len(t, res.MatchedAuthors, 1)
	require.Equal(t, "Дойль Артур Конан", res.MatchedAuthors[0].FullName)
	require.Equal(t, "Этюд в багровых тонах", res.Items[0].Title)

	// «кармен»: книга Мериме известнее автора Кармен — обычный поиск, книга первой.
	res, err = svc.ListWorks(ctx, books.ListParams{Query: "кармен", Limit: 10})
	require.NoError(t, err)
	require.Empty(t, res.MatchedAuthors)
	require.NotEmpty(t, res.Items)
	require.Equal(t, "Кармен", res.Items[0].Title)
	sugg, err = svc.SuggestWorks(ctx, "кармен", 5, 0, nil, nil, false)
	require.NoError(t, err)
	require.NotEmpty(t, sugg)
	require.Equal(t, "Кармен", sugg[0].Title, "подсказка: книга известнее автора — обычный поиск")
}
