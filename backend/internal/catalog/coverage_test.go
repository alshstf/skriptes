package catalog_test

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/catalog"
	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/skriptes/skriptes/backend/internal/inpx/inpxtest"
	"github.com/stretchr/testify/require"
)

// TestService_CoverageFixture — добивает «хвостовые» кейсы тем же билдером:
// региональные субтеги языка, книга с несколькими авторами (видна на карточке
// каждого), серия без номеров томов, очень плодовитый автор (потолок списка книг).
func TestService_CoverageFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	pool := startPostgres(t, ctx)
	mgr := startMeilisearch(t, ctx)

	books := []inpxtest.Book{
		// (1) Региональные/скриптовые субтеги: pt-BR / pt_BR / pt → все 'pt'.
		{Authors: []string{"Регион,Тест,"}, Genres: []string{"prose"}, Title: "Livro BR", LibID: "910001", Lang: "pt-BR"},
		{Authors: []string{"Регион,Тест,"}, Genres: []string{"prose"}, Title: "Livro BR 2", LibID: "910002", Lang: "pt_BR"},
		{Authors: []string{"Регион,Тест,"}, Genres: []string{"prose"}, Title: "Livro PT", LibID: "910003", Lang: "pt"},

		// (2) Книга с двумя авторами — должна быть на карточке каждого.
		{Authors: []string{"Альфа,Аркадий,", "Бета,Борис,"}, Genres: []string{"sf"}, Title: "Совместная книга", LibID: "910010", Lang: "ru"},

		// (3) Серия без номеров томов (SerNo = 0).
		{Authors: []string{"Серий,Тест,"}, Genres: []string{"sf"}, Title: "Том А", Series: "Безномерная серия", LibID: "910020", Lang: "ru"},
		{Authors: []string{"Серий,Тест,"}, Genres: []string{"sf"}, Title: "Том Б", Series: "Безномерная серия", LibID: "910021", Lang: "ru"},
	}
	// (4) Очень плодовитый автор: 505 книг (> потолка 500 в queryAuthorBooks).
	const prolific = 505
	for i := 0; i < prolific; i++ {
		books = append(books, inpxtest.Book{
			Authors: []string{"Плодовит,Тест,"},
			Genres:  []string{"sf"},
			Title:   fmt.Sprintf("Книга %03d", i),
			LibID:   strconv.Itoa(911000 + i),
			Lang:    "ru",
		})
	}

	path, err := inpxtest.WriteINPX(t.TempDir(), "coverage.inpx", books)
	require.NoError(t, err)

	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr})
	stats, err := imp.Run(ctx, path)
	require.NoError(t, err)
	require.Equal(t, 6+prolific, stats.BooksInserted)

	svc := catalog.New(pool)

	// (1) Региональные субтеги схлопнулись в один 'pt' (3 книги); ни одного кода
	//     с локалью ('-'/'_') в списке языков.
	langs, err := svc.ListLanguages(ctx)
	require.NoError(t, err)
	var ptCount int
	for _, l := range langs {
		require.NotContains(t, l.Code, "-", "локаль не срезана: %s", l.Code)
		require.NotContains(t, l.Code, "_", "локаль не срезана: %s", l.Code)
		if l.Code == "pt" {
			ptCount = l.BookCount
		}
	}
	require.Equal(t, 3, ptCount, "pt-BR/pt_BR/pt → один 'pt' с 3 книгами")

	// (2) Книга с двумя авторами видна на карточке каждого, с обоими именами.
	for _, last := range []string{"Альфа", "Бета"} {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM authors WHERE last_name = $1`, last).Scan(&id))
		a, err := svc.GetAuthor(ctx, id, 0, nil, nil, false)
		require.NoError(t, err)
		require.Equal(t, 1, a.BookCount, "%s: книга соавторства должна быть на карточке", last)
		require.Len(t, a.Books, 1)
		require.Equal(t, "Совместная книга", a.Books[0].Title)
		require.Len(t, a.Books[0].Authors, 2, "%s: у книги должны быть оба автора", last)
	}

	// (3) Серия без номеров: группируется, книги без ser_no, упорядочены по title.
	var seriesAuthorID int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM authors WHERE last_name = 'Серий'`).Scan(&seriesAuthorID))
	sa, err := svc.GetAuthor(ctx, seriesAuthorID, 0, nil, nil, false)
	require.NoError(t, err)
	require.Len(t, sa.Series, 1)
	require.Equal(t, "Безномерная серия", sa.Series[0].Title)
	require.Equal(t, 2, sa.Series[0].Count)
	require.Len(t, sa.Books, 2)
	for _, b := range sa.Books {
		require.Nil(t, b.SerNo, "у книг безномерной серии ser_no пуст")
	}

	// (4) Плодовитый автор: счётчик = реальное число, список книг урезан до 500.
	var prolificID int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM authors WHERE last_name = 'Плодовит'`).Scan(&prolificID))
	pa, err := svc.GetAuthor(ctx, prolificID, 0, nil, nil, false)
	require.NoError(t, err)
	require.Equal(t, prolific, pa.BookCount, "счётчик книг — полный")
	require.Len(t, pa.Books, 500, "список книг автора упирается в потолок 500")
}
