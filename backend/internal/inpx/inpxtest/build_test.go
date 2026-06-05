package inpxtest_test

import (
	"testing"

	"github.com/skriptes/skriptes/backend/internal/inpx"
	"github.com/skriptes/skriptes/backend/internal/inpx/inpxtest"
	"github.com/stretchr/testify/require"
)

// TestWriteINPX_RoundTrip — сгенерированная фикстура парсится реальным
// inpx.Open/Each ровно как задумано (билдер не врёт про формат).
func TestWriteINPX_RoundTrip(t *testing.T) {
	books := []inpxtest.Book{
		{Authors: []string{"Толстой,Лев,Николаевич"}, Genres: []string{"prose_classic"}, Title: "Анна Каренина", LibID: "900001", Lang: "RU"},
		{
			Authors: []string{"Маккефри,Энн,", "Болл,Маргарет,"}, Genres: []string{"sf", "sf_heroic"},
			Title: "Наследница", Series: "Акорна", SerNo: 1, LibID: "900002", Lang: "ru", Rating: 3, Keywords: "единороги",
		},
		{Authors: []string{"Удалён,Тест,"}, Genres: []string{"prose"}, Title: "Удалёнка", LibID: "900003", Lang: "ru", Deleted: true},
	}
	path, err := inpxtest.WriteINPX(t.TempDir(), "rt.inpx", books)
	require.NoError(t, err)

	ix, err := inpx.Open(path)
	require.NoError(t, err)
	defer func() { _ = ix.Close() }()
	require.Equal(t, "Synthetic Test [FB2]", ix.Collection.Name)

	var recs []inpx.Record
	require.NoError(t, ix.Each(func(_ inpx.InpFile, r inpx.Record) error {
		recs = append(recs, r)
		return nil
	}))
	require.Len(t, recs, 3)

	// Запись 1: один автор (полное ФИО), один жанр, lang как в источнике (RU).
	require.Equal(t, "Анна Каренина", recs[0].Title)
	require.Equal(t, []inpx.Author{{LastName: "Толстой", FirstName: "Лев", MiddleName: "Николаевич"}}, recs[0].Authors)
	require.Equal(t, []string{"prose_classic"}, recs[0].Genres)
	require.Equal(t, "RU", recs[0].Lang, "билдер кладёт lang как есть — нормализация на стороне импортёра")

	// Запись 2: два автора, два жанра, серия + ser_no, рейтинг, keywords.
	require.Len(t, recs[1].Authors, 2)
	require.Equal(t, "Маккефри", recs[1].Authors[0].LastName)
	require.Equal(t, "Болл", recs[1].Authors[1].LastName)
	require.Equal(t, []string{"sf", "sf_heroic"}, recs[1].Genres)
	require.Equal(t, "Акорна", recs[1].Series)
	require.Equal(t, 1, recs[1].SerNo)
	require.Equal(t, 3, recs[1].Rating)
	require.Equal(t, "единороги", recs[1].Keywords)

	// Запись 3: удалённая (DEL=1).
	require.True(t, recs[2].Deleted)
	require.Equal(t, "Удалёнка", recs[2].Title)
}
