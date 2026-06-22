package catalog_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	meili "github.com/meilisearch/meilisearch-go"
	"github.com/skriptes/skriptes/backend/internal/catalog"
	"github.com/skriptes/skriptes/backend/internal/db"
	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcmeili "github.com/testcontainers/testcontainers-go/modules/meilisearch"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const fixtureINPX = "../inpx/testdata/test.inpx"

// TestService_CollapsesEditionsByWork — два издания одной работы схлопываются в
// ОДНУ карточку на странице автора/серии (BookCount=1, EditionCount=2). Сидим
// напрямую (Meili не нужен — catalog работает по PG).
func TestService_CollapsesEditionsByWork(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPostgres(t, ctx)

	var collID, archID, authorID, seriesID, workID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO authors (last_name, normalized_name) VALUES ('Кинг','кинг стивен') RETURNING id`).Scan(&authorID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO series (title, normalized_title, author_id) VALUES ('Тёмная башня','тёмная башня',$1) RETURNING id`, authorID).Scan(&seriesID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO works (title, normalized_title, primary_author_id, series_id, ser_no) VALUES ('Оно','оно',$1,$2,1) RETURNING id`,
		authorID, seriesID).Scan(&workID))

	mkEdition := func(lib, lang string) {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, lang, series_id, ser_no, work_id)
			VALUES ($1,$2,$3,'f','fb2','Оно','оно',$4,$5,1,$6) RETURNING id`,
			collID, archID, lib, lang, seriesID, workID).Scan(&id))
		_, err := pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,0)`, id, authorID)
		require.NoError(t, err)
	}
	mkEdition("L-ru", "ru")
	mkEdition("L-en", "en")

	svc := catalog.New(pool)

	a, err := svc.GetAuthor(ctx, authorID, 0, nil, nil)
	require.NoError(t, err)
	require.Equal(t, 1, a.BookCount, "две издания одной работы = одна логическая книга")
	require.Len(t, a.Books, 1, "карточка автора схлопнута до одной работы")
	require.Equal(t, 2, a.Books[0].EditionCount, "edition_count учитывает оба издания")
	require.Len(t, a.Series, 1)
	require.Equal(t, 1, a.Series[0].Count, "счётчик серии — по работам, не изданиям")

	s, err := svc.GetSeries(ctx, seriesID, 0, nil, nil)
	require.NoError(t, err)
	require.Equal(t, 1, s.BookCount, "серия: одна логическая книга")
	require.Len(t, s.Books, 1)
	require.Equal(t, 2, s.Books[0].EditionCount)
}

// TestService_AuthorAndSeries_OnFixture — реальный PG + Meili (для импорта),
// после импорта ходим в Author/Series и проверяем агрегаты на конкретной книге
// LIBID=749080 (Алексеев / "Кадетский корпус. Книга 2" / серия "Петля [Алексеев]" #2 / 3 жанра).
func TestService_AuthorAndSeries_OnFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := startPostgres(t, ctx)
	mgr := startMeilisearch(t, ctx)

	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr})
	abs, _ := filepath.Abs(fixtureINPX)
	stats, err := imp.Run(ctx, abs)
	require.NoError(t, err)
	require.Equal(t, 20, stats.BooksInserted)

	// Год написания (written_year) в INPX/фикстуре отсутствует — его
	// наполняет fb2-обогащение (EnsureYearLocal), которого в этом тесте нет.
	// Проставляем явно, чтобы проверить гистограмму по ГОДУ НАПИСАНИЯ
	// (а не по date_added — см. граблю про дату добавления в коллекцию).
	_, err = pool.Exec(ctx, `UPDATE books SET written_year = 2015`)
	require.NoError(t, err)

	svc := catalog.New(pool)

	// Находим Алексеева по нормализованному имени — id плавающий между запусками.
	var (
		authorID int64
		seriesID int64
	)
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id FROM authors WHERE normalized_name = 'алексеев евгений артёмович'`,
	).Scan(&authorID))

	a, err := svc.GetAuthor(ctx, authorID, 0, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "Алексеев", a.LastName)
	require.Equal(t, "Алексеев Евгений Артёмович", a.FullName)
	require.Equal(t, 1, a.BookCount, "у Алексеева в фикстуре одна книга")
	require.Equal(t, 1, a.BooksTotal)
	require.Len(t, a.Books, 1)
	require.Equal(t, "Кадетский корпус. Книга 2", a.Books[0].Title)
	// Обогащённая плашка: books.HydrateListMeta проставил внешний рейтинг из
	// LIBRATE (=4 в фикстуре) + источник 'library'; экранизаций нет.
	require.NotNil(t, a.Books[0].ExternalRating, "плашка несёт внешний рейтинг (LIBRATE)")
	require.InDelta(t, 4.0, *a.Books[0].ExternalRating, 0.001)
	require.NotNil(t, a.Books[0].ExternalRatingSource)
	require.Equal(t, "library", *a.Books[0].ExternalRatingSource)
	require.False(t, a.Books[0].HasAdaptations)
	// Топ-жанры: ровно 3 (sf_action, popadanec, network_literature), каждый с count=1
	require.Len(t, a.TopGenres, 3)
	for _, g := range a.TopGenres {
		require.Equal(t, 1, g.Count)
	}
	// Серии: одна "Петля [Алексеев]" с count=1
	require.Len(t, a.Series, 1)
	require.Equal(t, "Петля [Алексеев]", a.Series[0].Title)
	require.Equal(t, 1, a.Series[0].Count)
	seriesID = a.Series[0].ID

	// Агрегаты-зеркало строки списка на карточке: годы активности (written_year
	// проставлен 2015 для всех) + языки изданий (ru). Подтверждает, что карточка
	// несёт ту же сводку, что компактный список авторов.
	require.NotNil(t, a.YearsActive, "годы активности на карточке")
	require.Equal(t, 2015, a.YearsActive.From)
	require.Equal(t, 2015, a.YearsActive.To)
	require.Contains(t, a.Languages, "ru", "языки изданий на карточке")

	// Series detail
	s, err := svc.GetSeries(ctx, seriesID, 0, nil, nil)
	require.NoError(t, err)
	require.Equal(t, "Петля [Алексеев]", s.Title)
	require.NotNil(t, s.AuthorID)
	require.Equal(t, authorID, *s.AuthorID)
	require.Equal(t, "Алексеев Евгений Артёмович", s.AuthorName)
	require.Equal(t, 1, s.BookCount)
	require.Len(t, s.Books, 1)
	require.Equal(t, "Кадетский корпус. Книга 2", s.Books[0].Title)
	require.Equal(t, []string{"Алексеев Евгений Артёмович"}, s.Books[0].Authors)

	// ── Видимость контента: скрытый язык убирает книги с карточек ──
	// Помечаем единственную книгу Алексеева языком-сентинелом и проверяем, что
	// GetAuthor/GetSeries с этим языком в исключениях её прячут (как в /books).
	_, err = pool.Exec(ctx, `UPDATE books SET lang = 'zz' WHERE id = $1`, a.Books[0].ID)
	require.NoError(t, err)

	aHidden, err := svc.GetAuthor(ctx, authorID, 0, nil, []string{"zz"})
	require.NoError(t, err)
	require.Equal(t, 0, aHidden.BookCount, "скрытый язык исключается из счётчика книг автора")
	require.Empty(t, aHidden.Books, "скрытый язык исключается из списка книг автора")
	// Серия без видимых книг не должна висеть на карточке (логически серия одна
	// на язык, у неё нет языка — прячем, если после фильтра книг не осталось).
	require.Empty(t, aHidden.Series, "серия без видимых книг скрыта на карточке автора")
	require.Empty(t, aHidden.TopGenres, "жанры скрытых книг не учитываются")
	require.Empty(t, aHidden.YearStats, "год скрытых книг не идёт в гистограмму")

	sHidden, err := svc.GetSeries(ctx, seriesID, 0, nil, []string{"zz"})
	require.NoError(t, err)
	require.Empty(t, sHidden.Books, "скрытый язык исключается из книг серии")
	require.Empty(t, sHidden.YearStats, "год скрытых книг серии не идёт в гистограмму")

	// Контроль: без исключения книга снова видна.
	aBack, err := svc.GetAuthor(ctx, authorID, 0, nil, nil)
	require.NoError(t, err)
	require.Len(t, aBack.Books, 1)

	// Negative: несуществующий id
	_, err = svc.GetAuthor(ctx, 99999999, 0, nil, nil)
	require.ErrorIs(t, err, catalog.ErrNotFound)
	_, err = svc.GetSeries(ctx, 99999999, 0, nil, nil)
	require.ErrorIs(t, err, catalog.ErrNotFound)

	// Suggest: префиксное совпадение по нормализованному имени.
	// "алек" → должны попасть Алексеев и Алексеева Адель Ивановна.
	authorSugg, err := svc.SuggestAuthors(ctx, "алек", 5)
	require.NoError(t, err)
	require.NotEmpty(t, authorSugg)
	var foundAlekseev bool
	for _, a := range authorSugg {
		if a.FullName == "Алексеев Евгений Артёмович" {
			require.Equal(t, 1, a.BookCount)
			foundAlekseev = true
		}
	}
	require.True(t, foundAlekseev, "ожидаем Алексеева в suggest по 'алек'")

	// Пустой запрос → пустой срез без ошибки.
	authorEmpty, err := svc.SuggestAuthors(ctx, "  ", 5)
	require.NoError(t, err)
	require.Empty(t, authorEmpty)

	// Suggest series: префикс "пет" по нормализованному заголовку.
	seriesSugg, err := svc.SuggestSeries(ctx, "пет", 5)
	require.NoError(t, err)
	require.NotEmpty(t, seriesSugg)
	require.Equal(t, "Петля [Алексеев]", seriesSugg[0].Title)
	require.Equal(t, "Алексеев Евгений Артёмович", seriesSugg[0].AuthorName)
	require.Equal(t, 1, seriesSugg[0].BookCount)

	// ── YearStats: у Алексеева ровно 1 книга с проставленным written_year →
	// одна точка в гистограмме по году написания.
	require.Len(t, a.YearStats, 1)
	require.Equal(t, 1, a.YearStats[0].Count)
	require.Equal(t, 2015, a.YearStats[0].Year)
	// К году приложен список книг (для тултипа гистограммы).
	require.Len(t, a.YearStats[0].Books, 1)
	require.Equal(t, "Кадетский корпус. Книга 2", a.YearStats[0].Books[0].Title)

	// Series тоже — единственная книга, одна точка.
	require.Len(t, s.YearStats, 1)
	require.Equal(t, 1, s.YearStats[0].Count)
	require.Equal(t, 2015, s.YearStats[0].Year)
	require.Len(t, s.YearStats[0].Books, 1)
	require.Equal(t, "Кадетский корпус. Книга 2", s.YearStats[0].Books[0].Title)

	// ── ReadCount: без сигналов = 0; запишем read и повторно прочитаем.
	require.Equal(t, 0, a.ReadCount)
	require.Equal(t, 0, s.ReadCount)

	// seed user + явная отметка «прочитано» (completed_at IS NOT NULL).
	// До v0.3 здесь был просто INSERT в reads с completed_at=NULL — этого
	// раньше хватало (read_count считал любые reads-rows). Теперь сигнал
	// строгий: только completed_at IS NOT NULL.
	var userID int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO users (email, display_name, password_hash, role)
		VALUES ('catalog-stats@example.com', 'Stats User', 'x', 'user')
		RETURNING id
	`).Scan(&userID))
	_, err = pool.Exec(ctx,
		`INSERT INTO reads (user_id, book_id, completed_at, updated_at) VALUES ($1, $2, now(), now())`,
		userID, a.Books[0].ID,
	)
	require.NoError(t, err)

	a2, err := svc.GetAuthor(ctx, authorID, userID, nil, nil)
	require.NoError(t, err)
	require.Equal(t, 1, a2.ReadCount)

	s2, err := svc.GetSeries(ctx, seriesID, userID, nil, nil)
	require.NoError(t, err)
	require.Equal(t, 1, s2.ReadCount)
}

// ── helpers (повтор из internal/books) ─────────────────────────

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
