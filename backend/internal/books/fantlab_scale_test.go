package books

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFantlabOnLibrateScale — перевод рейтинга Фантлаба в шкалу LIBRATE (#394):
// края, точки соответствия, монотонность.
func TestFantlabOnLibrateScale(t *testing.T) {
	require.InDelta(t, 1.0, FantlabOnLibrateScale(0.45), 1e-9, "ниже первой точки — 1")
	require.InDelta(t, 5.0, FantlabOnLibrateScale(8.97), 1e-9, "«Мастер и Маргарита» 8,97 — 5, а не 4,5")
	for _, p := range fantlabScale {
		require.InDelta(t, p.librate, FantlabOnLibrateScale(p.fantlab), 1e-9)
	}
	require.InDelta(t, 4.0, FantlabOnLibrateScale((5.61+7.16)/2), 1e-9, "между точками — линейно")
	prev := 0.0
	for r := 0.0; r <= 10; r += 0.05 {
		v := FantlabOnLibrateScale(r)
		require.GreaterOrEqual(t, v, prev, "монотонно (r=%.2f)", r)
		require.True(t, v >= 1 && v <= 5)
		prev = v
	}
}
