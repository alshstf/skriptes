package importer

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestComputeWorkPopularity — фиксирует поведение формулы известности:
// нули у безвестного синглтона, вклад каждого сигнала, монотонность,
// «домашнее чтение видно, но классику не топит».
func TestComputeWorkPopularity(t *testing.T) {
	t.Run("безвестный синглтон = 0", func(t *testing.T) {
		require.EqualValues(t, 0, computeWorkPopularity(workPopSignals{EditionCount: 1}))
		require.EqualValues(t, 0, computeWorkPopularity(workPopSignals{}))
	})

	t.Run("вклад отдельных сигналов", func(t *testing.T) {
		// 2 издания → 100·log2(2) = 100.
		require.EqualValues(t, 100, computeWorkPopularity(workPopSignals{EditionCount: 2}))
		// LIBRATE 5 → 40 + 24·5 = 160.
		require.EqualValues(t, 160, computeWorkPopularity(workPopSignals{EditionCount: 1, LibrateMax: 5}))
		// Экранизация → 150.
		require.EqualValues(t, 150, computeWorkPopularity(workPopSignals{EditionCount: 1, HasAdaptation: true}))
		// Вовлечённость: просмотр 20, чтение 60, оценка 100.
		require.EqualValues(t, 20, computeWorkPopularity(workPopSignals{EditionCount: 1, Views: 1}))
		require.EqualValues(t, 60, computeWorkPopularity(workPopSignals{EditionCount: 1, Reads: 1}))
		require.EqualValues(t, 100, computeWorkPopularity(workPopSignals{EditionCount: 1, UserRatings: 1}))
	})

	t.Run("монотонность по каждому сигналу", func(t *testing.T) {
		base := workPopSignals{EditionCount: 2, LibrateMax: 3, ExtVotes: 10, Views: 1}
		p0 := computeWorkPopularity(base)

		more := base
		more.EditionCount = 4
		require.Greater(t, computeWorkPopularity(more), p0, "editions")

		more = base
		more.LibrateMax = 5
		require.Greater(t, computeWorkPopularity(more), p0, "librate")

		more = base
		more.ExtVotes = 1000
		require.Greater(t, computeWorkPopularity(more), p0, "ext votes")

		more = base
		more.HasAdaptation = true
		require.Greater(t, computeWorkPopularity(more), p0, "adaptation")

		more = base
		more.Views = 5
		require.Greater(t, computeWorkPopularity(more), p0, "views")

		more = base
		more.Reads = 1
		require.Greater(t, computeWorkPopularity(more), p0, "reads")

		more = base
		more.UserRatings = 1
		require.Greater(t, computeWorkPopularity(more), p0, "user ratings")

		more = base
		more.FantlabMarks = 100
		require.Greater(t, computeWorkPopularity(more), p0, "fantlab marks")

		more = base
		more.OLRatings = 50
		require.Greater(t, computeWorkPopularity(more), p0, "ol ratings")

		more = base
		more.OLWant = 50
		require.Greater(t, computeWorkPopularity(more), p0, "ol want-to-read")

		more = base
		more.WDSitelinks = 20
		require.Greater(t, computeWorkPopularity(more), p0, "wd sitelinks")
	})

	t.Run("внешние счётчики известности (renown)", func(t *testing.T) {
		// Метро 2033: 6724 оценки Фантлаба → 30·log2(6725) ≈ 381.
		fl := computeWorkPopularity(workPopSignals{EditionCount: 1, FantlabMarks: 6724})
		require.InDelta(t, 381, fl, 1)
		// 36 оценок + 302 want-to-read у OL → 25·log2(339) ≈ 210.
		ol := computeWorkPopularity(workPopSignals{EditionCount: 1, OLRatings: 36, OLWant: 302})
		require.InDelta(t, 210, ol, 1)
		// «Мастер и Маргарита»: 78 sitelinks → 40·log2(79) ≈ 252.
		wd := computeWorkPopularity(workPopSignals{EditionCount: 1, WDSitelinks: 78})
		require.InDelta(t, 252, wd, 1)
	})

	t.Run("потолок edition_count", func(t *testing.T) {
		atCap := computeWorkPopularity(workPopSignals{EditionCount: popEditionCap})
		require.EqualValues(t, atCap, computeWorkPopularity(workPopSignals{EditionCount: popEditionCap * 10}),
			"выше потолка вклад изданий не растёт")
	})

	t.Run("домашний просмотр поднимает над хвостом, но не над классикой", func(t *testing.T) {
		classic := computeWorkPopularity(workPopSignals{
			EditionCount: 10, LibrateMax: 5, ExtVotes: 500, HasAdaptation: true,
		})
		viewedObscure := computeWorkPopularity(workPopSignals{EditionCount: 1, Views: 1})
		readObscure := computeWorkPopularity(workPopSignals{
			EditionCount: 1, Views: 1, Reads: 1, UserRatings: 1,
		})
		require.Greater(t, viewedObscure, int64(0), "просмотр выносит из нулевого хвоста")
		require.Greater(t, classic, readObscure, "даже прочитанная безвестная книга ниже классики")
	})
}
