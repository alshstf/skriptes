package catalog_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/catalog"
	"github.com/stretchr/testify/require"
)

// authorsListFixture сидит небольшую синтетическую коллекцию напрямую в PG
// (Meili не нужен — ListAuthorsFiltered работает по PG) и возвращает id'шники
// сущностей, нужные тестам фильтров/агрегатов.
type authorsListFixture struct {
	pool     *pgxpool.Pool
	userID   int64
	kingID   int64 // Кинг — 2 книги (ru/en издания одной работы + ещё одна), рейтинг 5, экранизация, в избранном (автор+книга)
	asimovID int64 // Азимов — 1 книга sf, год 1951
	tolstoy  int64 // Толстой — 1 книга prose, без LIBRATE, но web-рейтинг 4.2
}

func seedAuthorsList(t *testing.T, ctx context.Context, pool *pgxpool.Pool) authorsListFixture {
	t.Helper()
	f := authorsListFixture{pool: pool}

	var collID, archID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ('u@e','U','x','user') RETURNING id`).Scan(&f.userID))

	mkAuthor := func(last, norm string) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO authors (last_name, normalized_name) VALUES ($1,$2) RETURNING id`, last, norm).Scan(&id))
		return id
	}
	f.kingID = mkAuthor("Кинг", "кинг стивен")
	f.asimovID = mkAuthor("Азимов", "азимов айзек")
	f.tolstoy = mkAuthor("Толстой", "толстой лев")

	mkGenre := func(code string) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO genres (fb2_code, name_ru) VALUES ($1,$1) RETURNING id`, code).Scan(&id))
		return id
	}
	gHorror := mkGenre("sf_horror")
	gSf := mkGenre("sf")
	gProse := mkGenre("prose_classic")

	// mkBook вставляет издание и привязывает автора+жанр. lang/srcLang/year/
	// rating/workID — управляемые поля для проверки агрегатов.
	type bookOpt struct {
		lib       string
		lang      string
		srcLang   string
		year      *int
		rating    *int     // LIBRATE (books.rating)
		extRating *float64 // web-рейтинг (books.external_rating)
		extSource string   // источник web-рейтинга (books.external_rating_source)
		workID    int64
		authorID  int64
		genreID   int64
		adaptYear *int
	}
	intp := func(v int) *int { return &v }
	fp := func(v float64) *float64 { return &v }
	nullFloat := func(p *float64) any {
		if p == nil {
			return nil
		}
		return *p
	}
	mkBook := func(o bookOpt) int64 {
		var workID int64
		if o.workID == 0 {
			require.NoError(t, pool.QueryRow(ctx,
				`INSERT INTO works (title, normalized_title, primary_author_id) VALUES ($1,$3,$2) RETURNING id`,
				o.lib, o.authorID, o.lib).Scan(&workID))
		} else {
			workID = o.workID
		}
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title,
			                   lang, src_lang, written_year, rating, work_id, external_rating, external_rating_source)
			VALUES ($1,$2,$3,'f','fb2',$3,$9,$4,$5,$6,$7,$8,$10,$11) RETURNING id`,
			collID, archID, o.lib, nullStr(o.lang), nullStr(o.srcLang),
			nullInt(o.year), nullInt(o.rating), workID, o.lib, nullFloat(o.extRating), nullStr(o.extSource)).Scan(&id))
		_, err := pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,0)`, id, o.authorID)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `INSERT INTO book_genres (book_id, genre_id) VALUES ($1,$2)`, id, o.genreID)
		require.NoError(t, err)
		if o.adaptYear != nil {
			_, err = pool.Exec(ctx,
				`INSERT INTO book_adaptations (book_id, provider, ext_id, title, year) VALUES ($1,'wikidata','Q1','Film',$2)`,
				id, *o.adaptYear)
			require.NoError(t, err)
		}
		return id
	}

	// Кинг: одна работа с двумя изданиями (ru + en), рейтинг 5, экранизация,
	// год 1986; плюс отдельная книга-работа на en (год 1977).
	var kingWork int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO works (title, normalized_title, primary_author_id) VALUES ('Оно','оно',$1) RETURNING id`,
		f.kingID).Scan(&kingWork))
	kingBookRu := mkBook(bookOpt{lib: "k-ru", lang: "ru", srcLang: "en", year: intp(1986), rating: intp(5), workID: kingWork, authorID: f.kingID, genreID: gHorror, adaptYear: intp(1990)})
	mkBook(bookOpt{lib: "k-en", lang: "en", year: intp(1986), workID: kingWork, authorID: f.kingID, genreID: gHorror})
	mkBook(bookOpt{lib: "k2", lang: "en", year: intp(1977), authorID: f.kingID, genreID: gSf})

	// Азимов: одна sf-книга, год 1951, без рейтинга/экранизации.
	mkBook(bookOpt{lib: "az", lang: "en", year: intp(1951), authorID: f.asimovID, genreID: gSf})

	// Толстой: одна prose-книга, без года/рейтинга/экранизации.
	// Толстой — без LIBRATE, но с web-рейтингом 4.2 от Google Books (проверка
	// фолбэка COALESCE(LIBRATE, web) в агрегате/фильтре + атрибуции источника).
	mkBook(bookOpt{lib: "tl", lang: "ru", authorID: f.tolstoy, genreID: gProse, extRating: fp(4.2), extSource: "googlebooks"})

	// Избранное юзера: подписка на Кинга + одна книга Кинга в избранном.
	// Книжное избранное — членство в служебной полке kind='favorites' (миграция 0023).
	_, err := pool.Exec(ctx, `INSERT INTO favorite_authors (user_id, author_id) VALUES ($1,$2)`, f.userID, f.kingID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		WITH fav AS (
			INSERT INTO user_collections (user_id, name, kind) VALUES ($1, 'Избранное', 'favorites')
			ON CONFLICT (user_id) WHERE kind = 'favorites' DO UPDATE SET name = user_collections.name
			RETURNING id
		)
		INSERT INTO user_collection_books (collection_id, book_id) SELECT id, $2 FROM fav
	`, f.userID, kingBookRu)
	require.NoError(t, err)

	// Оценки читателей (book_ratings, по инстансу): работу Кинга оценили ДВА
	// пользователя — 5 и 2 → средняя 3.5, count 2. У Азимова/Толстого оценок нет.
	var u2 int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ('u2@e','U2','x','user') RETURNING id`).Scan(&u2))
	_, err = pool.Exec(ctx,
		`INSERT INTO book_ratings (user_id, work_id, rating) VALUES ($1,$2,5),($3,$2,2)`, f.userID, kingWork, u2)
	require.NoError(t, err)

	return f
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
func nullInt(p *int) any {
	if p == nil {
		return nil
	}
	return *p
}

// TestListAuthorsFiltered_Aggregates — базовые агрегаты на строке автора:
// book_count схлопывает издания по работе; languages = lang ∪ src_lang;
// years_active = min/max written_year; external_rating = max(COALESCE(rating, web));
// has_adaptations; is_favorite + favorited_books_count для текущего юзера.
func TestListAuthorsFiltered_Aggregates(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPostgres(t, ctx)
	f := seedAuthorsList(t, ctx, pool)
	svc := catalog.New(pool)

	res, err := svc.ListAuthorsFiltered(ctx, catalog.AuthorListParams{UserID: f.userID})
	require.NoError(t, err)
	require.Equal(t, 3, res.Total, "три автора в коллекции")
	require.Len(t, res.Items, 3)

	byID := map[int64]catalog.AuthorListItem{}
	for _, it := range res.Items {
		byID[it.ID] = it
	}

	king := byID[f.kingID]
	require.Equal(t, 2, king.BookCount, "две работы (издания ru/en схлопнуты)")
	require.True(t, king.IsFavorite)
	require.Equal(t, 1, king.FavoritedBooksCount, "одна книга Кинга в избранном")
	require.True(t, king.HasAdaptations)
	require.NotNil(t, king.ExternalRating)
	require.InDelta(t, 5.0, *king.ExternalRating, 0.001)
	require.NotNil(t, king.ExternalRatingSource)
	require.Equal(t, "library", *king.ExternalRatingSource, "у Кинга максимум — LIBRATE")
	require.NotNil(t, king.ReaderRating, "у Кинга есть оценки читателей")
	require.InDelta(t, 3.5, *king.ReaderRating, 0.001, "avg(5,2) по инстансу")
	require.Equal(t, 2, king.ReaderRatingCount)
	require.NotNil(t, king.YearsActive)
	require.Equal(t, 1977, king.YearsActive.From)
	require.Equal(t, 1986, king.YearsActive.To)
	require.Contains(t, king.Languages, "ru")
	require.Contains(t, king.Languages, "en")
	require.NotEmpty(t, king.TopGenres)

	tolstoy := byID[f.tolstoy]
	require.False(t, tolstoy.IsFavorite)
	require.Equal(t, 0, tolstoy.FavoritedBooksCount)
	require.False(t, tolstoy.HasAdaptations)
	require.NotNil(t, tolstoy.ExternalRating, "web-рейтинг подхватывается через COALESCE")
	require.InDelta(t, 4.2, *tolstoy.ExternalRating, 0.001, "fallback на web, когда нет LIBRATE")
	require.NotNil(t, tolstoy.ExternalRatingSource)
	require.Equal(t, "googlebooks", *tolstoy.ExternalRatingSource, "источник web-рейтинга в тултип")
	require.Nil(t, tolstoy.ReaderRating, "нет оценок читателей → nil")
	require.Equal(t, 0, tolstoy.ReaderRatingCount)
	require.Nil(t, tolstoy.YearsActive, "нет written_year → nil")
}

// TestListAuthorsFiltered_Filters — фильтры режут множество авторов.
func TestListAuthorsFiltered_Filters(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPostgres(t, ctx)
	f := seedAuthorsList(t, ctx, pool)
	svc := catalog.New(pool)

	ids := func(r catalog.AuthorListResult) map[int64]bool {
		m := map[int64]bool{}
		for _, it := range r.Items {
			m[it.ID] = true
		}
		return m
	}

	// q — префикс по normalized_name.
	res, err := svc.ListAuthorsFiltered(ctx, catalog.AuthorListParams{Query: "кинг"})
	require.NoError(t, err)
	require.Equal(t, 1, res.Total)
	require.True(t, ids(res)[f.kingID])

	// genres — sf пишут Кинг и Азимов, prose — Толстой.
	res, err = svc.ListAuthorsFiltered(ctx, catalog.AuthorListParams{Genres: []string{"sf"}})
	require.NoError(t, err)
	got := ids(res)
	require.True(t, got[f.kingID])
	require.True(t, got[f.asimovID])
	require.False(t, got[f.tolstoy])

	// langs — ru: Кинг (издание ru) и Толстой.
	res, err = svc.ListAuthorsFiltered(ctx, catalog.AuthorListParams{Langs: []string{"ru"}})
	require.NoError(t, err)
	got = ids(res)
	require.True(t, got[f.kingID])
	require.True(t, got[f.tolstoy])
	require.False(t, got[f.asimovID], "Азимов только на en")

	// year_from/year_to — 1950..1960 ловит только Азимова (1951).
	res, err = svc.ListAuthorsFiltered(ctx, catalog.AuthorListParams{YearFrom: 1950, YearTo: 1960})
	require.NoError(t, err)
	require.Equal(t, 1, res.Total)
	require.True(t, ids(res)[f.asimovID])

	// has_adaptations — только Кинг.
	res, err = svc.ListAuthorsFiltered(ctx, catalog.AuthorListParams{HasAdaptations: true})
	require.NoError(t, err)
	require.Equal(t, 1, res.Total)
	require.True(t, ids(res)[f.kingID])

	// min_rating — единый внешний рейтинг = COALESCE(LIBRATE, web): Кинг=5 (LIBRATE),
	// Толстой=4.2 (web). Порог 5 → только Кинг; порог 3 → оба (web Толстого считается).
	res, err = svc.ListAuthorsFiltered(ctx, catalog.AuthorListParams{MinRating: 5})
	require.NoError(t, err)
	require.Equal(t, 1, res.Total)
	require.True(t, ids(res)[f.kingID])

	res, err = svc.ListAuthorsFiltered(ctx, catalog.AuthorListParams{MinRating: 3})
	require.NoError(t, err)
	require.Equal(t, 2, res.Total, "web-рейтинг Толстого (4.2) проходит порог 3")
	require.True(t, ids(res)[f.kingID])
	require.True(t, ids(res)[f.tolstoy])

	// min_reader_rating — у Кинга средняя 3.5: проходит ≥3, не проходит ≥4.
	res, err = svc.ListAuthorsFiltered(ctx, catalog.AuthorListParams{MinReaderRating: 3})
	require.NoError(t, err)
	require.Equal(t, 1, res.Total)
	require.True(t, ids(res)[f.kingID])

	res, err = svc.ListAuthorsFiltered(ctx, catalog.AuthorListParams{MinReaderRating: 4})
	require.NoError(t, err)
	require.Equal(t, 0, res.Total, "3.5 < 4 → Кинг отфильтрован")

	// favorites_only — подписка только на Кинга.
	res, err = svc.ListAuthorsFiltered(ctx, catalog.AuthorListParams{UserID: f.userID, FavoritesOnly: true})
	require.NoError(t, err)
	require.Equal(t, 1, res.Total)
	require.True(t, ids(res)[f.kingID])
}

// TestListAuthorsFiltered_SortAndExclusions — сортировка по числу книг и
// исключение скрытого контента из агрегатов.
func TestListAuthorsFiltered_SortAndExclusions(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPostgres(t, ctx)
	f := seedAuthorsList(t, ctx, pool)
	svc := catalog.New(pool)

	// sort=book_count — Кинг (2 работы) впереди одиночек.
	res, err := svc.ListAuthorsFiltered(ctx, catalog.AuthorListParams{Sort: "book_count"})
	require.NoError(t, err)
	require.Len(t, res.Items, 3)
	require.Equal(t, f.kingID, res.Items[0].ID, "автор с большим числом книг — первым")

	// sort=reader_rating — Кинг (единственный с оценками) впереди.
	res, err = svc.ListAuthorsFiltered(ctx, catalog.AuthorListParams{Sort: "reader_rating"})
	require.NoError(t, err)
	require.Len(t, res.Items, 3)
	require.Equal(t, f.kingID, res.Items[0].ID, "автор с оценками читателей — первым")

	// sort=rating — единый внешний рейтинг: Кинг (5, LIBRATE) > Толстой (4.2, web) >
	// Азимов (нет внешнего, NULLS LAST).
	res, err = svc.ListAuthorsFiltered(ctx, catalog.AuthorListParams{Sort: "rating"})
	require.NoError(t, err)
	require.Len(t, res.Items, 3)
	require.Equal(t, f.kingID, res.Items[0].ID, "LIBRATE 5 — первым")
	require.Equal(t, f.tolstoy, res.Items[1].ID, "web 4.2 — вторым")

	// Исключение жанра sf_horror: у Кинга остаётся только sf-работа (k2),
	// book_count падает до 1 и hsorror-экранизация/рейтинг с horror-издания уходят.
	res, err = svc.ListAuthorsFiltered(ctx, catalog.AuthorListParams{
		UserID:        f.userID,
		ExcludeGenres: []string{"sf_horror"},
	})
	require.NoError(t, err)
	var king catalog.AuthorListItem
	for _, it := range res.Items {
		if it.ID == f.kingID {
			king = it
		}
	}
	require.Equal(t, 1, king.BookCount, "horror-работа исключена из счётчика")
	require.False(t, king.HasAdaptations, "экранизация была на horror-издании")
	require.Nil(t, king.ExternalRating, "рейтинг 5 был на horror-издании")
	for _, g := range king.TopGenres {
		require.NotEqual(t, "sf_horror", g.Code, "скрытый жанр не светится в топе")
	}

	// Регресс-кейс плейсхолдеров: исключения активны, но контент-фильтра нет —
	// активен лишь NAME-фильтр. COUNT-запрос не должен ссылаться на висячий
	// плейсхолдер исключений (раньше падал «could not determine data type»).
	res, err = svc.ListAuthorsFiltered(ctx, catalog.AuthorListParams{
		Query:         "кинг",
		ExcludeGenres: []string{"sf_horror"},
		ExcludeLangs:  []string{"de"},
	})
	require.NoError(t, err)
	require.Equal(t, 1, res.Total)
	require.Len(t, res.Items, 1)
	require.Equal(t, f.kingID, res.Items[0].ID)
	require.Equal(t, 1, res.Items[0].BookCount, "horror исключён и в агрегатах под name-фильтром")
}
