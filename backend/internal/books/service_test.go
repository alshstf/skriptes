package books_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	meili "github.com/meilisearch/meilisearch-go"
	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/db"
	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcmeili "github.com/testcontainers/testcontainers-go/modules/meilisearch"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// fixtureINPX — наш постоянный 20-записный фикстура (включая Анну
// Каренину Толстого — public-domain книга для теста экранизаций).
// Лежит в backend/internal/inpx/testdata/test.inpx.
const fixtureINPX = "../inpx/testdata/test.inpx"

// TestService_ListAndGet — поднимает PG + Meili через testcontainers,
// импортирует фикстуру через тот же importer что и прод, проверяет:
//   - List() с пустым query вернёт ровно столько, сколько в Meili (18, без DEL=1)
//   - List(q="Кадетский") вернёт хотя бы один hit
//   - Get(id) вернёт книгу LIBID=749080 со всеми связями (1 автор, 3 жанра, серия)
func TestService_ListAndGet(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := startPostgres(t, ctx)
	mgr := startMeilisearch(t, ctx)

	// Импортируем фикстуру в БД и Meili — теми же путями что прод.
	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr})
	abs, err := filepath.Abs(fixtureINPX)
	require.NoError(t, err)
	stats, err := imp.Run(ctx, abs)
	require.NoError(t, err)
	require.Equal(t, 20, stats.BooksInserted)

	svc := books.New(pool, mgr, nil) // existing assertions не зависят от persona

	// ── List без query: должно вернуться 19 (минус 1 DEL=1)
	res, err := svc.List(ctx, books.ListParams{Limit: 50})
	require.NoError(t, err)
	require.Len(t, res.Items, stats.BooksIndexed)
	require.Equal(t, int64(stats.BooksIndexed), res.Total)
	require.Equal(t, 50, res.Limit)
	require.Equal(t, 0, res.Offset)
	for _, it := range res.Items {
		require.NotZero(t, it.ID)
		require.NotEmpty(t, it.Title)
	}

	// ── Search by title
	res, err = svc.List(ctx, books.ListParams{Query: "Кадетский", Limit: 5})
	require.NoError(t, err)
	require.NotEmpty(t, res.Items, "поиск по 'Кадетский' должен вернуть хотя бы одну книгу")
	require.Equal(t, "Кадетский", res.Query)
	hit := res.Items[0]
	require.Equal(t, "Кадетский корпус. Книга 2", hit.Title)

	// ── Get: детальная карточка LIBID=749080
	bookID := hit.ID
	book, err := svc.Get(ctx, bookID)
	require.NoError(t, err)
	require.Equal(t, "Кадетский корпус. Книга 2", book.Title)
	require.Equal(t, "749080", book.LibID)
	require.Equal(t, "fb2", book.Ext)
	require.Equal(t, "fb2-749080-749080.zip", book.Archive)
	require.Equal(t, int64(849047), book.SizeBytes)
	require.False(t, book.Deleted)

	require.NotNil(t, book.Series)
	require.Equal(t, "Петля [Алексеев]", book.Series.Title)
	require.NotNil(t, book.SerNo)
	require.Equal(t, 2, *book.SerNo)

	require.Len(t, book.Authors, 1)
	require.Equal(t, "Алексеев", book.Authors[0].LastName)
	require.Equal(t, "Алексеев Евгений Артёмович", book.Authors[0].FullName)

	require.Len(t, book.Genres, 3)
	codes := make([]string, 0, 3)
	for _, g := range book.Genres {
		codes = append(codes, g.Code)
	}
	require.ElementsMatch(t, []string{"network_literature", "popadanec", "sf_action"}, codes)

	// ── Get: несуществующий id → ErrNotFound
	_, err = svc.Get(ctx, 99999999)
	require.ErrorIs(t, err, books.ErrNotFound)

	// ── GenresAndLang: лёгкий lookup для hard-block gate. Те же жанры/язык,
	//    что и в полной карточке; несуществующий id → ErrNotFound.
	gCodes, gLang, err := svc.GenresAndLang(ctx, bookID)
	require.NoError(t, err)
	require.Equal(t, book.Lang, gLang)
	require.ElementsMatch(t, codes, gCodes)
	_, _, err = svc.GenresAndLang(ctx, 99999999)
	require.ErrorIs(t, err, books.ErrNotFound)

	// ── Suggest: typeahead с лимитом, по той же фикстуре.
	sugg, err := svc.Suggest(ctx, "Кадетский", 5, 0, nil, nil)
	require.NoError(t, err)
	require.NotEmpty(t, sugg)
	require.Equal(t, "Кадетский корпус. Книга 2", sugg[0].Title)
	require.LessOrEqual(t, len(sugg), 5)

	// Пустой query → пустой срез без ошибки.
	empty, err := svc.Suggest(ctx, "  ", 5, 0, nil, nil)
	require.NoError(t, err)
	require.Empty(t, empty)

	// ── Фильтр по жанру: только sf_action → должна вернуться Кадетский корпус
	//    (она единственная в фикстуре с этим жанром).
	res, err = svc.List(ctx, books.ListParams{Genres: []string{"sf_action"}, Limit: 50})
	require.NoError(t, err)
	require.NotEmpty(t, res.Items)
	for _, it := range res.Items {
		require.Contains(t, it.Genres, "sf_action", "при фильтре genres=sf_action все книги должны иметь этот жанр")
	}

	// ── Скрытый контент (NOT IN): exclude по жанру убирает книги этого
	//    жанра из выдачи; exclude по языку — книги этого языка. Проверяет
	//    реальный meili `NOT IN`-синтаксис против настоящего Meili.
	baseline, err := svc.List(ctx, books.ListParams{Limit: 50})
	require.NoError(t, err)
	excl, err := svc.List(ctx, books.ListParams{ExcludeGenres: []string{"sf_action"}, Limit: 50})
	require.NoError(t, err)
	require.Less(t, len(excl.Items), len(baseline.Items), "exclude жанра должен убрать хотя бы одну книгу")
	for _, it := range excl.Items {
		require.NotContains(t, it.Genres, "sf_action", "скрытый жанр не должен встречаться в выдаче")
	}
	exclLang, err := svc.List(ctx, books.ListParams{ExcludeLangs: []string{"ru"}, Limit: 50})
	require.NoError(t, err)
	for _, it := range exclLang.Items {
		require.NotEqual(t, "ru", it.Lang, "книги на скрытом языке не должны попадать в выдачу")
	}
	// Suggest с exclude: палитра не подсказывает книги скрытого жанра.
	sExcl, err := svc.Suggest(ctx, "Кадетский", 5, 0, []string{"sf_action"}, nil)
	require.NoError(t, err)
	for _, it := range sExcl {
		require.NotContains(t, it.Genres, "sf_action", "Suggest не должен подсказывать книги скрытого жанра")
	}

	// ── cover_path догидрачивается из Postgres в список.
	//    Meili обложек не хранит — проставляем cover_path напрямую в БД
	//    (как это делает enrichment) и проверяем, что List вернул его на
	//    нужной книге, а на остальных он пуст.
	_, err = pool.Exec(ctx, `UPDATE books SET cover_path = $1 WHERE id = $2`, "deadbeef.jpg", bookID)
	require.NoError(t, err)
	res, err = svc.List(ctx, books.ListParams{Limit: 50})
	require.NoError(t, err)
	var covered, withPath int
	for _, it := range res.Items {
		if it.ID == bookID {
			covered++
			require.Equal(t, "deadbeef.jpg", it.CoverPath, "cover_path должен догидрачиваться на список")
		}
		if it.CoverPath != "" {
			withPath++
		}
	}
	require.Equal(t, 1, covered, "книга с известным id должна быть в списке ровно один раз")
	require.Equal(t, 1, withPath, "cover_path должен стоять только у обогащённой книги")

	// ── Фильтр по языку: должны вернуться только русские.
	res, err = svc.List(ctx, books.ListParams{Lang: "ru", Limit: 50})
	require.NoError(t, err)
	for _, it := range res.Items {
		require.Equal(t, "ru", it.Lang)
	}

	// ── Сортировка по году убывающая: первая книга должна быть новейшей.
	res, err = svc.List(ctx, books.ListParams{Sort: "year_desc", Limit: 50})
	require.NoError(t, err)
	for i := 1; i < len(res.Items); i++ {
		// Пропускаем книги без year — Meili их кладёт в конец при desc.
		prev := res.Items[i-1].Year
		cur := res.Items[i].Year
		if prev != nil && cur != nil {
			require.GreaterOrEqual(t, *prev, *cur, "year_desc нарушен")
		}
	}

	// ── Facets: запросили genres и lang — должны получить распределения.
	res, err = svc.List(ctx, books.ListParams{Facets: []string{"genres", "lang"}, Limit: 50})
	require.NoError(t, err)
	require.NotNil(t, res.Facets)
	require.Contains(t, res.Facets, "genres")
	require.Contains(t, res.Facets, "lang")
	// Хотя бы один из жанров фикстуры должен присутствовать.
	require.NotEmpty(t, res.Facets["genres"])
	require.GreaterOrEqual(t, res.Facets["lang"]["ru"], int64(1))
}

// TestService_WorksIndex — веб-путь (ListWorks/SuggestWorks/GetWork) против
// индекса works, построенного импортёром. Фикстура без группировки → каждая
// работа singleton, поэтому число работ = числу проиндексированных изданий.
func TestService_WorksIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := startPostgres(t, ctx)
	mgr := startMeilisearch(t, ctx)

	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr})
	abs, err := filepath.Abs(fixtureINPX)
	require.NoError(t, err)
	stats, err := imp.Run(ctx, abs)
	require.NoError(t, err)

	svc := books.New(pool, mgr, nil)

	// ── ListWorks без query: по работе на каждое живое издание (singleton).
	wres, err := svc.ListWorks(ctx, books.ListParams{Limit: 50})
	require.NoError(t, err)
	require.Len(t, wres.Items, stats.BooksIndexed)
	require.Equal(t, int64(stats.BooksIndexed), wres.Total)
	for _, it := range wres.Items {
		require.NotZero(t, it.ID)
		require.NotEmpty(t, it.Title)
	}

	// ── Поиск по работам → ID работы; GetWork по нему отдаёт карточку.
	wsearch, err := svc.ListWorks(ctx, books.ListParams{Query: "Кадетский", Limit: 5})
	require.NoError(t, err)
	require.NotEmpty(t, wsearch.Items)
	require.Equal(t, "Кадетский корпус. Книга 2", wsearch.Items[0].Title)
	workID := wsearch.Items[0].ID

	// ── matchingStrategy=all: слово, которого нет ни в одном доке, обнуляет
	//    выдачу (Meili-дефолт «last» молча ронял хвостовые слова — «гарри
	//    <мусор>» матчил то же, что «гарри», прод-аудит P1 #5).
	wnone, err := svc.ListWorks(ctx, books.ListParams{Query: "Кадетский абракадабрище", Limit: 5})
	require.NoError(t, err)
	require.Empty(t, wnone.Items, "матчиться должны ВСЕ слова запроса")

	wb, err := svc.GetWork(ctx, workID, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "Кадетский корпус. Книга 2", wb.Title)
	require.Equal(t, workID, wb.WorkID, "GetWork(workID).WorkID == workID")
	require.NotEmpty(t, wb.Editions)

	// Несуществующая работа → ErrNotFound.
	_, err = svc.GetWork(ctx, 99999999, nil, nil)
	require.ErrorIs(t, err, books.ErrNotFound)

	// ── SuggestWorks → тот же work id, что ListWorks.
	wsugg, err := svc.SuggestWorks(ctx, "Кадетский", 5, 0, nil, nil, false)
	require.NoError(t, err)
	require.NotEmpty(t, wsugg)
	require.Equal(t, "Кадетский корпус. Книга 2", wsugg[0].Title)
	require.Equal(t, workID, wsugg[0].ID)

	// ── Исключение жанра убирает работу из works-выдачи.
	wbaseline, err := svc.ListWorks(ctx, books.ListParams{Limit: 50})
	require.NoError(t, err)
	wexcl, err := svc.ListWorks(ctx, books.ListParams{ExcludeGenres: []string{"sf_action"}, Limit: 50})
	require.NoError(t, err)
	require.Less(t, len(wexcl.Items), len(wbaseline.Items), "exclude жанра должен убрать хотя бы одну работу")
	for _, it := range wexcl.Items {
		require.NotContains(t, it.Genres, "sf_action")
	}

	// ── Исключение языка ru (works-семантика lang IN visible): русские работы
	//    уходят, нерусские остаются.
	wexLang, err := svc.ListWorks(ctx, books.ListParams{ExcludeLangs: []string{"ru"}, Limit: 50})
	require.NoError(t, err)
	for _, it := range wexLang.Items {
		require.NotEqual(t, "ru", it.Lang)
	}

	// ── Обложка/CoverEditionID/EditionCount догидрачиваются на works-выдачу.
	_, err = pool.Exec(ctx, `
		UPDATE books SET cover_path='wcover.jpg'
		WHERE id = (SELECT id FROM books WHERE work_id=$1 AND deleted=false ORDER BY id LIMIT 1)`, workID)
	require.NoError(t, err)
	wres2, err := svc.ListWorks(ctx, books.ListParams{Query: "Кадетский", Limit: 5})
	require.NoError(t, err)
	require.NotEmpty(t, wres2.Items)
	require.Equal(t, "wcover.jpg", wres2.Items[0].CoverPath)
	require.NotZero(t, wres2.Items[0].CoverEditionID)
	require.GreaterOrEqual(t, wres2.Items[0].EditionCount, 1)

	// ── Фасеты по works присутствуют (считают РАБОТЫ).
	wfac, err := svc.ListWorks(ctx, books.ListParams{Facets: []string{"genres", "lang"}, Limit: 50})
	require.NoError(t, err)
	require.NotNil(t, wfac.Facets)
	require.Contains(t, wfac.Facets, "genres")
	require.Contains(t, wfac.Facets, "lang")

	// ── Пагинация: offset кратен limit → page-режим Meili, Total точный
	//    (TotalHits) и на глубокой странице; страницы не пересекаются.
	page1, err := svc.ListWorks(ctx, books.ListParams{Limit: 7})
	require.NoError(t, err)
	page2, err := svc.ListWorks(ctx, books.ListParams{Limit: 7, Offset: 7})
	require.NoError(t, err)
	require.Equal(t, int64(stats.BooksIndexed), page2.Total, "точный total и на 2-й странице")
	require.NotEmpty(t, page2.Items)
	seen := map[int64]bool{}
	for _, it := range page1.Items {
		seen[it.ID] = true
	}
	for _, it := range page2.Items {
		require.False(t, seen[it.ID], "страницы page-режима не должны пересекаться")
	}

	// Некратный offset → fallback на Limit/Offset (оценка total; на малой
	// фикстуре она точна).
	odd, err := svc.ListWorks(ctx, books.ListParams{Limit: 7, Offset: 3})
	require.NoError(t, err)
	require.Equal(t, int64(stats.BooksIndexed), odd.Total)
	require.NotEmpty(t, odd.Items)

	// ── Симуляция прод-капа maxTotalHits: за потолком Meili возвращает ПУСТЫЕ
	//    hits, а не ошибку — фиксируем контракт «пусто, не 5xx» независимо от
	//    версии Meili (реальный кап на 20-книжной фикстуре не воспроизводится).
	task, err := mgr.Index("works").UpdatePaginationWithContext(ctx,
		&meili.Pagination{MaxTotalHits: 5})
	require.NoError(t, err)
	_, err = mgr.WaitForTaskWithContext(ctx, task.TaskUID, 0)
	require.NoError(t, err)
	capped, err := svc.ListWorks(ctx, books.ListParams{Limit: 5, Offset: 10})
	require.NoError(t, err, "запрос за потолком maxTotalHits не должен падать")
	require.Empty(t, capped.Items)
	// Возвращаем боевой потолок (тест — последний, но не оставляем сюрприз).
	task, err = mgr.Index("works").UpdatePaginationWithContext(ctx,
		&meili.Pagination{MaxTotalHits: importer.MeiliMaxTotalHits})
	require.NoError(t, err)
	_, err = mgr.WaitForTaskWithContext(ctx, task.TaskUID, 0)
	require.NoError(t, err)
}

// TestService_GetReturnsEditions — Get отдаёт work-level карточку с массивом
// editions[] (все издания работы), top-level поля = открытого издания.
func TestService_GetReturnsEditions(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPostgres(t, ctx)

	var collID, archID, authorID, workID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO authors (last_name, normalized_name) VALUES ('Кинг','кинг стивен') RETURNING id`).Scan(&authorID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO works (title, normalized_title, primary_author_id, written_year) VALUES ('Оно','оно',$1,1986) RETURNING id`,
		authorID).Scan(&workID))
	mk := func(lib, lang, translator string) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, lang, translator, work_id)
			VALUES ($1,$2,$3,$3,'fb2','Оно','оно',$4,NULLIF($5,''),$6) RETURNING id`,
			collID, archID, lib, lang, translator, workID).Scan(&id))
		_, err := pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,0)`, id, authorID)
		require.NoError(t, err)
		return id
	}
	ru := mk("L-ru", "ru", "Вебер Виктор")
	en := mk("L-en", "en", "")
	// У открытого издания (ru) обложки нет, у другого (en) — есть.
	_, err := pool.Exec(ctx, `UPDATE books SET cover_path='cover-en.jpg' WHERE id=$1`, en)
	require.NoError(t, err)

	svc := books.New(pool, nil, nil)
	b, err := svc.Get(ctx, ru)
	require.NoError(t, err)
	require.Equal(t, "Оно", b.Title)
	require.Equal(t, workID, b.WorkID)
	require.NotNil(t, b.WrittenYear)
	require.Equal(t, 1986, *b.WrittenYear, "год написания — уровня работы")
	require.Len(t, b.Editions, 2, "обе издания работы в editions[]")
	require.Equal(t, ru, b.Editions[0].ID, "открытое издание — первым")
	require.Equal(t, "Вебер Виктор", b.Editions[0].Translator)
	require.Equal(t, "ru", b.Lang, "top-level lang = открытое издание")
	require.Equal(t, "cover-en.jpg", b.CoverPath, "обложка берётся из другого издания, если у открытого её нет")
}

// ── helpers ────────────────────────────────────────────────────

func startPostgres(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	pgC, err := postgres.Run(ctx,
		"postgres:17-alpine",
		postgres.WithDatabase("skriptes_test"),
		postgres.WithUsername("skriptes"),
		postgres.WithPassword("skriptes"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pgC.Terminate(context.Background()) })

	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, db.Migrate(dsn))

	pool, err := db.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func startMeilisearch(t *testing.T, ctx context.Context) meili.ServiceManager {
	t.Helper()
	const masterKey = "test-master-key-1234567890"
	mC, err := tcmeili.Run(ctx, "getmeili/meilisearch:v1.13", tcmeili.WithMasterKey(masterKey))
	require.NoError(t, err)
	t.Cleanup(func() { _ = mC.Terminate(context.Background()) })
	addr, err := mC.Address(ctx)
	require.NoError(t, err)
	return meili.New(addr, meili.WithAPIKey(masterKey))
}
