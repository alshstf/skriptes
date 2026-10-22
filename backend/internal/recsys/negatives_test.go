package recsys_test

import (
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/recsys"
	"github.com/stretchr/testify/require"
)

// Предикаты «читает»/«бросил» и «негативное скрытие» — общие для модели
// близости и персоны поиска (recsys/03, 3.5): Score и
// history.PersonaProfile должны сходиться на одном определении.
func TestWeights_ReadingDroppedPredicates(t *testing.T) {
	w := recsys.DefaultWeights
	old := now.Add(-w.DroppedMinAge - time.Hour)
	fresh := now.Add(-24 * time.Hour)
	cases := []struct {
		name             string
		s                recsys.WorkSignals
		reading, dropped bool
	}{
		{"доля 0.1, давно", recsys.WorkSignals{Fraction: fp(0.1), ReadAt: old}, false, true},
		{"доля 0.1, недавно", recsys.WorkSignals{Fraction: fp(0.1), ReadAt: fresh}, false, false},
		{"доля 0.24 на границе, давно", recsys.WorkSignals{Fraction: fp(0.249), ReadAt: old}, false, true},
		{"доля 0.25 — не брошено", recsys.WorkSignals{Fraction: fp(0.25), ReadAt: old}, false, false},
		{"доля 0.7, недавно — читает", recsys.WorkSignals{Fraction: fp(0.7), ReadAt: fresh}, true, false},
		{"доля 0.7, давно — ни то ни другое", recsys.WorkSignals{Fraction: fp(0.7), ReadAt: old}, false, false},
		{"дочитано — не брошено", recsys.WorkSignals{Completed: true, Fraction: fp(0.1), ReadAt: old}, false, false},
		{"без доли (Kindle) — нет данных", recsys.WorkSignals{ReadAt: old}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.reading, w.IsReading(tc.s, now), "reading")
			require.Equal(t, tc.dropped, w.IsDropped(tc.s, now), "dropped")
			a := recsys.Score(tc.s, now, w)
			_, hasReading := a.Parts[recsys.KindReading]
			_, hasDropped := a.Parts[recsys.KindDropped]
			require.Equal(t, tc.reading, hasReading, "Score: reading")
			require.Equal(t, tc.dropped, hasDropped, "Score: dropped")
		})
	}
}

func TestDismissReason_Negative(t *testing.T) {
	require.True(t, recsys.DismissReason("").Negative(), "скрытия до 1.57 без причины")
	require.True(t, recsys.DismissNotInterested.Negative())
	require.False(t, recsys.DismissAlreadyRead.Negative())
	require.False(t, recsys.DismissLater.Negative())
	for _, r := range []recsys.DismissReason{"", recsys.DismissNotInterested, recsys.DismissAlreadyRead, recsys.DismissLater} {
		a := recsys.Score(recsys.WorkSignals{Dismissed: true, DismissReason: r, DismissedAt: now}, now, recsys.DefaultWeights)
		_, has := a.Parts[recsys.KindDismissed]
		require.Equal(t, r.Negative(), has, "Score: %q", r)
	}
}
