package catalog_test

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/catalog"
	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/skriptes/skriptes/backend/internal/inpx/inpxtest"
	"github.com/stretchr/testify/require"
)

// TestService_DiverseFixture — синтетическая фикстура под кейсы, которых не было
// в test.inpx: язык в разном регистре (нормализация), одна серия-дубль на двух
// языках (скрытие пустых серий), мульти-жанровая книга со скрытым жанром,
// книга без языка, удалённая книга. Реальные данные вместо ручных UPDATE-ов.
func TestService_DiverseFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := startPostgres(t, ctx)
	mgr := startMeilisearch(t, ctx)

	books := []inpxtest.Book{
		// (1) Нормализация языка: DE + de + без языка у одного автора.
		{Authors: []string{"Норм,Язык,Тестович"}, Genres: []string{"prose_classic"}, Title: "Buch auf DE", LibID: "900001", Lang: "DE"},
		{Authors: []string{"Норм,Язык,Тестович"}, Genres: []string{"prose_classic"}, Title: "Buch auf de", LibID: "900002", Lang: "de"},
		{Authors: []string{"Норм,Язык,Тестович"}, Genres: []string{"prose_classic"}, Title: "Книга без языка", LibID: "900003", Lang: ""},

		// (2) Одна логическая серия — два технических ряда на разных языках.
		{Authors: []string{"Гэлбрейт,Роберт,"}, Genres: []string{"det_police"}, Title: "The Cuckoo's Calling", Series: "Cormoran Strike", SerNo: 1, LibID: "900010", Lang: "en"},
		{Authors: []string{"Гэлбрейт,Роберт,"}, Genres: []string{"det_police"}, Title: "The Silkworm", Series: "Cormoran Strike", SerNo: 2, LibID: "900011", Lang: "en"},
		{Authors: []string{"Гэлбрейт,Роберт,"}, Genres: []string{"det_police"}, Title: "Зов кукушки", Series: "Корморан Страйк", SerNo: 1, LibID: "900012", Lang: "ru"},
		{Authors: []string{"Гэлбрейт,Роберт,"}, Genres: []string{"det_police"}, Title: "Шелкопряд", Series: "Корморан Страйк", SerNo: 2, LibID: "900013", Lang: "ru"},

		// (3) Мульти-жанровая книга со «скрытым» жанром + чистая для контроля.
		{Authors: []string{"Жанров,Тест,"}, Genres: []string{"sf", "erotica"}, Title: "Фантастика с эротикой", LibID: "900020", Lang: "ru"},
		{Authors: []string{"Жанров,Тест,"}, Genres: []string{"sf"}, Title: "Чистая фантастика", LibID: "900021", Lang: "ru"},

		// (4) Удалённая книга (DEL=1) — не индексируется и не висит на карточке.
		{Authors: []string{"Удалён,Тест,"}, Genres: []string{"prose"}, Title: "Удалённая книга", LibID: "900030", Lang: "ru", Deleted: true},
	}

	path, err := inpxtest.WriteINPX(t.TempDir(), "diverse.inpx", books)
	require.NoError(t, err)

	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr})
	stats, err := imp.Run(ctx, path)
	require.NoError(t, err)
	require.Equal(t, 10, stats.BooksInserted, "удалённые тоже идут в PG")
	require.Equal(t, 1, stats.BooksDeleted)
	require.Equal(t, 9, stats.BooksIndexed, "удалённая в Meili не индексируется")

	svc := catalog.New(pool)

	// (1) Нормализация: DE+de схлопнулись в один 'de' с 2 книгами; 'DE' нет;
	//     книга без языка в список языков не попадает.
	langs, err := svc.ListLanguages(ctx)
	require.NoError(t, err)
	deCount, deBooks := 0, 0
	for _, l := range langs {
		require.NotEqual(t, "DE", l.Code, "язык должен быть нормализован к нижнему регистру")
		if l.Code == "de" {
			deCount++
			deBooks = l.BookCount
		}
	}
	require.Equal(t, 1, deCount, "немецкий — ровно одна строка")
	require.Equal(t, 2, deBooks, "DE+de = 2 книги после нормализации")

	var normID int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM authors WHERE last_name = 'Норм'`).Scan(&normID))
	norm, err := svc.GetAuthor(ctx, normID, 0, nil, nil, false)
	require.NoError(t, err)
	require.Equal(t, 3, norm.BookCount, "книга без языка существует и считается")

	// (2) Дубль серии + скрытие по языку: при скрытом en у Гэлбрейта остаётся
	//     только русская серия (английская пустеет после фильтра и исчезает).
	var galbraithID int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM authors WHERE last_name = 'Гэлбрейт'`).Scan(&galbraithID))
	full, err := svc.GetAuthor(ctx, galbraithID, 0, nil, nil, false)
	require.NoError(t, err)
	require.Len(t, full.Series, 2, "без фильтра — обе языковые серии")
	require.Equal(t, 4, full.BookCount)

	hidEn, err := svc.GetAuthor(ctx, galbraithID, 0, nil, []string{"en"}, false)
	require.NoError(t, err)
	require.Len(t, hidEn.Series, 1, "со скрытым en пустая англ. серия не показывается")
	require.Equal(t, "Корморан Страйк", hidEn.Series[0].Title)
	require.Equal(t, 2, hidEn.BookCount)
	for _, b := range hidEn.Books {
		require.NotEqual(t, "en", b.Lang)
	}

	// (3) Скрытие по жанру: книга с erotica исчезает, чистая фантастика остаётся.
	var genreID int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM authors WHERE last_name = 'Жанров'`).Scan(&genreID))
	hidEro, err := svc.GetAuthor(ctx, genreID, 0, []string{"erotica"}, nil, false)
	require.NoError(t, err)
	require.Equal(t, 1, hidEro.BookCount, "книга со скрытым жанром исключена")
	require.Len(t, hidEro.Books, 1)
	require.Equal(t, "Чистая фантастика", hidEro.Books[0].Title)

	// (4) Удалённая книга: автор есть, но книг 0 (deleted скрыт везде).
	var delID int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM authors WHERE last_name = 'Удалён'`).Scan(&delID))
	del, err := svc.GetAuthor(ctx, delID, 0, nil, nil, false)
	require.NoError(t, err)
	require.Equal(t, 0, del.BookCount)
	require.Empty(t, del.Books)

	// (5) hideCompilations (opt-in): помеченная сборником работа исчезает из
	// СПИСКА книг карточки; без флага — остаётся (её выносит секция сборников).
	var compAuthor int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM authors WHERE last_name = 'Жанров'`).Scan(&compAuthor))
	_, err = pool.Exec(ctx, `
		UPDATE works SET kind='collection', kind_source='heuristic'
		WHERE id = (SELECT b.work_id FROM books b WHERE b.title = 'Чистая фантастика' LIMIT 1)`)
	require.NoError(t, err)
	withComps, err := svc.GetAuthor(ctx, compAuthor, 0, nil, nil, false)
	require.NoError(t, err)
	noComps, err := svc.GetAuthor(ctx, compAuthor, 0, nil, nil, true)
	require.NoError(t, err)
	require.Greater(t, len(withComps.Books), len(noComps.Books), "hideCompilations убирает сборник из списка книг")
	for _, b := range noComps.Books {
		require.NotEqual(t, "Чистая фантастика", b.Title, "со скрытием сборник вне списка")
	}
	var inWith bool
	for _, b := range withComps.Books {
		if b.Title == "Чистая фантастика" {
			inWith = true
		}
	}
	require.True(t, inWith, "без скрытия сборник в списке (для секции)")

	// (6) LOOSE COUPLING: сборники ВСЕГДА вне счётчика/статистики автора,
	// независимо от hideCompilations. book_count одинаков в обоих режимах и
	// НЕ считает помеченную сборником работу.
	require.Equal(t, withComps.BookCount, noComps.BookCount,
		"book_count не зависит от hideCompilations — сборники всегда вне счётчика")
	require.Equal(t, 1, withComps.BookCount, "из 2 работ автора одна помечена сборником → счёт 1")

	// (6b) Языковые чипсы: метим EN-работы Гэлбрейта сборником → в языках
	// остаётся только ru (loose coupling статистики), но книги остаются видны.
	var galb int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM authors WHERE last_name = 'Гэлбрейт'`).Scan(&galb))
	before, err := svc.GetAuthor(ctx, galb, 0, nil, nil, false)
	require.NoError(t, err)
	require.Contains(t, before.Languages, "en")
	require.Contains(t, before.Languages, "ru")
	_, err = pool.Exec(ctx, `
		UPDATE works SET kind='anthology', kind_source='heuristic'
		WHERE id IN (SELECT b.work_id FROM books b WHERE b.title IN ('The Cuckoo''s Calling','The Silkworm'))`)
	require.NoError(t, err)
	after, err := svc.GetAuthor(ctx, galb, 0, nil, nil, false)
	require.NoError(t, err)
	require.NotContains(t, after.Languages, "en", "язык сборника не входит в чипсы автора")
	require.Contains(t, after.Languages, "ru")
	require.Less(t, after.BookCount, before.BookCount, "сборники не в счётчике")
	require.NotEmpty(t, after.Books, "но книги остаются видны (в секции)")
}
