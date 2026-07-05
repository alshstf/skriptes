package books

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestVisibleEditions — скрытые языки убираются из списка изданий работы
// (нормализация lower+trim с обеих сторон), неизвестный язык остаётся,
// пустой excludeLangs не мутирует вход.
func TestVisibleEditions(t *testing.T) {
	eds := []EditionRef{
		{ID: 1, Lang: "ru"},
		{ID: 2, Lang: "EN"},   // регистр
		{ID: 3, Lang: " en "}, // пробелы
		{ID: 4, Lang: "de"},
		{ID: 5, Lang: ""}, // неизвестный язык — оставляем
	}

	got := visibleEditions(eds, []string{"EN"}) // exclude тоже канонизируем
	ids := make([]int64, 0, len(got))
	for _, e := range got {
		ids = append(ids, e.ID)
	}
	require.Equal(t, []int64{1, 4, 5}, ids, "en-издания скрыты, ru/de/пустой — видны")

	require.Len(t, visibleEditions(eds, nil), 5, "пустой excludeLangs → все издания")
	require.Len(t, visibleEditions(nil, []string{"en"}), 0, "пустой список изданий")

	// вход не мутируется (получаем новый слайс).
	_ = visibleEditions(eds, []string{"ru"})
	require.Equal(t, int64(1), eds[0].ID, "исходный слайс не тронут")
}
