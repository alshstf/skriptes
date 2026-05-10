package inpx_test

import (
	"sort"
	"testing"

	"github.com/skriptes/skriptes/backend/internal/inpx"
	"github.com/stretchr/testify/require"
)

// TestOpenAndIterate — golden-тест на реальном фикстуре.
// Фикстура: infra/testdata/test.inpx (3 inp / 19 записей; 2 архива _lost).
func TestOpenAndIterate(t *testing.T) {
	ix, err := inpx.Open("testdata/test.inpx")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ix.Close() })

	// collection.info
	require.Equal(t, "Lib.rus.ec Local [FB2]", ix.Collection.Name)
	require.Equal(t, "librusec_local_fb2", ix.Collection.Prefix)
	require.Equal(t, 65536, ix.Collection.Version)

	// version.info
	require.Equal(t, "20260501", ix.Version)

	// structure.info отсутствует → DefaultSchema
	require.Equal(t, inpx.DefaultSchema, ix.Schema)

	// 3 .inp файла; у двух в имени суффикс "_lost" — он должен быть
	// отброшен при деривации Archive (физические архивы такой суффикс
	// не несут).
	require.Len(t, ix.Files, 3)
	names := map[string]inpx.InpFile{}
	for _, f := range ix.Files {
		names[f.Name] = f
	}
	require.Contains(t, names, "fb2-749080-749080.inp")
	require.Contains(t, names, "fb2-625127-625160_lost.inp")
	require.Contains(t, names, "fb2-025838-696919_lost.inp")
	require.Equal(t, "fb2-749080-749080.zip", names["fb2-749080-749080.inp"].Archive)
	require.Equal(t, "fb2-625127-625160.zip", names["fb2-625127-625160_lost.inp"].Archive,
		"_lost суффикс должен быть отброшен при выводе имени архива")
	require.Equal(t, "fb2-025838-696919.zip", names["fb2-025838-696919_lost.inp"].Archive)

	// Итерация: 19 записей всего; собираем мапу archive → []record для проверок.
	type fr struct {
		archive string
		rec     inpx.Record
	}
	var all []fr
	require.NoError(t, ix.Each(func(file inpx.InpFile, rec inpx.Record) error {
		all = append(all, fr{archive: file.Archive, rec: rec})
		return nil
	}))
	require.Len(t, all, 19)

	// Конкретная запись из непотерянного архива.
	var alekseev *inpx.Record
	for i := range all {
		if all[i].rec.LibID == "749080" {
			alekseev = &all[i].rec
		}
	}
	require.NotNil(t, alekseev, "запись LIBID=749080 должна быть в фикстуре")
	require.Equal(t, "Кадетский корпус. Книга 2", alekseev.Title)
	require.Equal(t, "Петля [Алексеев]", alekseev.Series)
	require.Equal(t, 2, alekseev.SerNo)
	require.Equal(t, int64(849047), alekseev.Size)
	require.Equal(t, "fb2", alekseev.Ext)
	require.Equal(t, "ru", alekseev.Lang)
	require.Equal(t, 4, alekseev.Rating, "LIBRATE=4 в фикстуре")
	require.False(t, alekseev.Deleted)
	require.NotNil(t, alekseev.Date)
	require.Equal(t, "2023-02-07", alekseev.Date.Format("2006-01-02"))
	require.Len(t, alekseev.Authors, 1)
	require.Equal(t, "Алексеев", alekseev.Authors[0].LastName)
	require.Equal(t, "Евгений", alekseev.Authors[0].FirstName)
	require.Equal(t, "Артёмович", alekseev.Authors[0].MiddleName)

	// Жанры разрезаны корректно.
	sort.Strings(alekseev.Genres)
	require.Equal(t, []string{"network_literature", "popadanec", "sf_action"}, alekseev.Genres)

	// Записи из .inp с суффиксом _lost разбираются нормально — и в
	// derived Archive они попадают под именем без суффикса.
	var fromStrippedArchives int
	for _, x := range all {
		if x.archive == "fb2-625127-625160.zip" || x.archive == "fb2-025838-696919.zip" {
			fromStrippedArchives++
		}
	}
	require.Equal(t, 18, fromStrippedArchives,
		"из 19 записей 18 пришли из .inp со стриппнутым _lost-суффиксом")
}
