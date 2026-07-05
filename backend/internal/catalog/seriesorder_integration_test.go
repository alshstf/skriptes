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

// TestService_SeriesOrderCascade — каскад series_order на реальных данных:
// ser_no нет, порядок определяется written_year → edition_year → эвристика
// названия → date_added. Годы/даты ставим UPDATE'ом после импорта (билдер их
// не умеет — прецедент service_test.go).
func TestService_SeriesOrderCascade(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := startPostgres(t, ctx)
	mgr := startMeilisearch(t, ctx)

	// Во всех сериях ser_no = 0 (не задан) → каскад уходит на следующий уровень.
	books := []inpxtest.Book{
		// Серия «Год» — порядок по written_year.
		{Authors: []string{"Годов,Тест,"}, Genres: []string{"sf"}, Title: "ГодВ", Series: "Год", LibID: "920001", Lang: "ru"},
		{Authors: []string{"Годов,Тест,"}, Genres: []string{"sf"}, Title: "ГодА", Series: "Год", LibID: "920002", Lang: "ru"},
		{Authors: []string{"Годов,Тест,"}, Genres: []string{"sf"}, Title: "ГодБ", Series: "Год", LibID: "920003", Lang: "ru"},
		// Серия «Издание» — порядок по edition_year (written нет).
		{Authors: []string{"Издат,Тест,"}, Genres: []string{"sf"}, Title: "ИздВ", Series: "Издание", LibID: "920010", Lang: "ru"},
		{Authors: []string{"Издат,Тест,"}, Genres: []string{"sf"}, Title: "ИздА", Series: "Издание", LibID: "920011", Lang: "ru"},
		// Серия «Эвр» — порядок по эвристике названия.
		{Authors: []string{"Эврист,Тест,"}, Genres: []string{"sf"}, Title: "Хроники. Книга третья", Series: "Эвр", LibID: "920020", Lang: "ru"},
		{Authors: []string{"Эврист,Тест,"}, Genres: []string{"sf"}, Title: "Хроники. Книга первая", Series: "Эвр", LibID: "920021", Lang: "ru"},
		{Authors: []string{"Эврист,Тест,"}, Genres: []string{"sf"}, Title: "Хроники. Книга вторая", Series: "Эвр", LibID: "920022", Lang: "ru"},
		// Серия «Дата» — последний резерв date_added.
		{Authors: []string{"Датов,Тест,"}, Genres: []string{"sf"}, Title: "ДатаZ", Series: "Дата", LibID: "920030", Lang: "ru"},
		{Authors: []string{"Датов,Тест,"}, Genres: []string{"sf"}, Title: "ДатаA", Series: "Дата", LibID: "920031", Lang: "ru"},
	}
	path, err := inpxtest.WriteINPX(t.TempDir(), "seriesorder.inpx", books)
	require.NoError(t, err)
	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr})
	_, err = imp.Run(ctx, path)
	require.NoError(t, err)

	// written_year для серии «Год» (порядок А<Б<В по году, а не по алфавиту).
	for libID, wy := range map[string]int{"920002": 2005, "920003": 2008, "920001": 2010} {
		_, err = pool.Exec(ctx, `UPDATE books SET written_year=$1 WHERE lib_id=$2`, wy, libID)
		require.NoError(t, err)
	}
	// edition_year для «Издание».
	for libID, ey := range map[string]int{"920011": 1990, "920010": 1999} {
		_, err = pool.Exec(ctx, `UPDATE books SET edition_year=$1 WHERE lib_id=$2`, ey, libID)
		require.NoError(t, err)
	}
	// date_added для «Дата» (А раньше Z).
	_, err = pool.Exec(ctx, `UPDATE books SET date_added='2019-01-01' WHERE lib_id='920031'`)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE books SET date_added='2021-01-01' WHERE lib_id='920030'`)
	require.NoError(t, err)

	svc := catalog.New(pool)
	titles := func(seriesTitle string) []string {
		var sid int64
		require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM series WHERE title=$1`, seriesTitle).Scan(&sid))
		s, err := svc.GetSeries(ctx, sid, 0, nil, nil, false)
		require.NoError(t, err)
		out := make([]string, len(s.Books))
		for i, b := range s.Books {
			out[i] = b.Title
			require.NotNil(t, b.SeriesOrder, "series_order должен быть проставлен")
			require.Equal(t, i, *b.SeriesOrder, "series_order должен совпадать с позицией")
		}
		return out
	}

	require.Equal(t, []string{"ГодА", "ГодБ", "ГодВ"}, titles("Год"), "порядок по written_year")
	require.Equal(t, []string{"ИздА", "ИздВ"}, titles("Издание"), "порядок по edition_year")
	require.Equal(t, []string{"Хроники. Книга первая", "Хроники. Книга вторая", "Хроники. Книга третья"}, titles("Эвр"), "порядок по эвристике")
	require.Equal(t, []string{"ДатаA", "ДатаZ"}, titles("Дата"), "порядок по date_added")
}
