package books

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPopularityBoost — форма буста известности: ноль без сигнала,
// монотонность, ожидаемый масштаб относительно Meili-_rankingScore [0,1].
func TestPopularityBoost(t *testing.T) {
	require.Zero(t, popularityBoost(0))
	require.Zero(t, popularityBoost(-5))
	require.Greater(t, popularityBoost(100), popularityBoost(10))
	require.Greater(t, popularityBoost(1000), popularityBoost(100))
	// «Классика» (p≈1000) получает ~+0.1 — хватает на близкий матч,
	// но не на явно лучший (разница base ≥0.2 непреодолима).
	require.InDelta(t, 0.0997, popularityBoost(1000), 0.001)
	require.Less(t, popularityBoost(100_000), 0.2)
}

// TestSortByFinalScore_PopularityBoost — известность перевешивает БЛИЗКИЙ
// матч, не перевешивает явно лучший, а при равных score порядок Meili
// сохраняется (стабильность).
func TestSortByFinalScore_PopularityBoost(t *testing.T) {
	t.Run("близкий матч: известная книга поднимается", func(t *testing.T) {
		scored := []scoredItem{
			{item: ListItem{ID: 1}, base: 0.95},                            // безвестная, чуть лучший матч
			{item: ListItem{ID: 2}, base: 0.94, pop: popularityBoost(800)}, // классика
		}
		sortByFinalScore(scored)
		require.EqualValues(t, 2, scored[0].item.ID)
	})

	t.Run("явно лучший матч не перебивается", func(t *testing.T) {
		scored := []scoredItem{
			{item: ListItem{ID: 1}, base: 1.0}, // точное совпадение редкой книги
			{item: ListItem{ID: 2}, base: 0.7, pop: popularityBoost(1000)},
		}
		sortByFinalScore(scored)
		require.EqualValues(t, 1, scored[0].item.ID)
	})

	t.Run("равные score сохраняют порядок Meili", func(t *testing.T) {
		scored := []scoredItem{
			{item: ListItem{ID: 1}, base: 0.9},
			{item: ListItem{ID: 2}, base: 0.9},
			{item: ListItem{ID: 3}, base: 0.9},
		}
		sortByFinalScore(scored)
		require.EqualValues(t, 1, scored[0].item.ID)
		require.EqualValues(t, 2, scored[1].item.ID)
		require.EqualValues(t, 3, scored[2].item.ID)
	})

	t.Run("persona и известность складываются", func(t *testing.T) {
		scored := []scoredItem{
			{item: ListItem{ID: 1}, base: 0.9, personal: 0.05},
			{item: ListItem{ID: 2}, base: 0.9, personal: 0.05, pop: popularityBoost(500)},
		}
		sortByFinalScore(scored)
		require.EqualValues(t, 2, scored[0].item.ID)
	})
}
