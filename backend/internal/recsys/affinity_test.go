package recsys_test

import (
	"math"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/history"
	"github.com/skriptes/skriptes/backend/internal/recsys"
	"github.com/stretchr/testify/require"
)

var now = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

func fp(f float64) *float64 { return &f }

// TestMaxAuthorsMirrorsHistory — recsys не импортирует history, константы
// должны совпадать руками.
func TestMaxAuthorsMirrorsHistory(t *testing.T) {
	require.Equal(t, history.MaxPersonaAuthors, recsys.MaxAuthors)
}

// TestScore_Table — каждая строка таблицы весов плана 1.1 по отдельности.
func TestScore_Table(t *testing.T) {
	w := recsys.DefaultWeights
	recent := now // возраст 0 — без затухания; затухание проверяет TestScore_Decay
	cases := []struct {
		name  string
		s     recsys.WorkSignals
		kind  recsys.Kind
		part  float64
		score float64
	}{
		{"оценка 5", recsys.WorkSignals{Rating: 5, RatedAt: recent}, recsys.KindRating, 5, 5},
		{"оценка 4", recsys.WorkSignals{Rating: 4, RatedAt: recent}, recsys.KindRating, 3, 3},
		{"оценка 3", recsys.WorkSignals{Rating: 3, RatedAt: recent}, recsys.KindRating, 0, 0},
		{"оценка 2", recsys.WorkSignals{Rating: 2, RatedAt: recent}, recsys.KindRating, -3, -3},
		{"оценка 1", recsys.WorkSignals{Rating: 1, RatedAt: recent}, recsys.KindRating, -5, -5},
		{"дочитано", recsys.WorkSignals{Completed: true, CompletedAt: recent, Opened: true}, recsys.KindCompleted, 3, 3},
		{"читает: доля 0.6, вчера", recsys.WorkSignals{Fraction: fp(0.6), ReadAt: recent, Opened: true}, recsys.KindReading, 2, 2},
		{"бросил: доля 0.1, 200 дней", recsys.WorkSignals{Fraction: fp(0.1), ReadAt: now.Add(-200 * 24 * time.Hour), Opened: true}, recsys.KindDropped, -2, -2 * math.Pow(0.5, 200.0/730)},
		{"★ избранное", recsys.WorkSignals{Favorite: true, ShelfAt: recent}, recsys.KindFavorite, 2, 2},
		{"своя полка", recsys.WorkSignals{OnShelf: true, ShelfAt: recent}, recsys.KindShelf, 1, 1},
		{"одно подчёркивание", recsys.WorkSignals{Annotations: 1, AnnotatedAt: recent}, recsys.KindAnnotations, 0.5, 0.5},
		{"пять подчёркиваний — предел 2", recsys.WorkSignals{Annotations: 5, AnnotatedAt: recent}, recsys.KindAnnotations, 2, 2},
		{"приобретено, не открыто", recsys.WorkSignals{Acquired: true, ReadAt: recent}, recsys.KindAcquired, 1, 1},
		{"один просмотр", recsys.WorkSignals{Views: 1, ViewedAt: recent}, recsys.KindViews, 0.5, 0.5},
		{"семь просмотров — предел 1.5", recsys.WorkSignals{Views: 7, ViewedAt: recent}, recsys.KindViews, 1.5, 1.5},
		{"сто просмотров — предел 1.5", recsys.WorkSignals{Views: 100, ViewedAt: recent}, recsys.KindViews, 1.5, 1.5},
		{"скрыто из ленты", recsys.WorkSignals{Dismissed: true, DismissedAt: recent}, recsys.KindDismissed, -3, -3},
		{"никогда не спрашивать", recsys.WorkSignals{PromptNever: true, PromptAt: recent}, recsys.KindPromptNever, -1, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := recsys.Score(c.s, now, w)
			require.Len(t, a.Parts, 1, "ровно одно слагаемое: %v", a.Parts)
			require.InDelta(t, c.part, a.Parts[c.kind], 1e-9)
			require.InDelta(t, c.score, a.Score, 1e-3)
		})
	}
}

// TestScore_ReadingStates — границы «читает» / «бросил» / ничего.
func TestScore_ReadingStates(t *testing.T) {
	w := recsys.DefaultWeights
	day := 24 * time.Hour
	check := func(name string, f float64, age time.Duration, want recsys.Kind) {
		t.Run(name, func(t *testing.T) {
			a := recsys.Score(recsys.WorkSignals{Fraction: fp(f), ReadAt: now.Add(-age), Opened: true}, now, w)
			if want == "" {
				require.Empty(t, a.Parts)
				return
			}
			require.Contains(t, a.Parts, want)
			require.Len(t, a.Parts, 1)
		})
	}
	check("доля 0.6 через 90 дней — ещё читает", 0.6, 90*day, recsys.KindReading)
	check("доля 0.6 через 91 день — ничего", 0.6, 91*day, "")
	check("доля 0.5 вчера — ничего (ниже порога)", 0.5, day, "")
	check("доля 0.24 через 180 дней — бросил", 0.24, 180*day, recsys.KindDropped)
	check("доля 0.24 через 179 дней — ещё не бросил", 0.24, 179*day, "")
	check("доля 0.25 через 300 дней — ничего (на пороге)", 0.25, 300*day, "")

	t.Run("дочитано перебивает долю", func(t *testing.T) {
		a := recsys.Score(recsys.WorkSignals{Completed: true, CompletedAt: now, Fraction: fp(0.1), ReadAt: now.Add(-400 * day), Opened: true}, now, w)
		require.Contains(t, a.Parts, recsys.KindCompleted)
		require.NotContains(t, a.Parts, recsys.KindDropped)
	})
	t.Run("Kindle: доли нет — ни читает, ни бросил", func(t *testing.T) {
		a := recsys.Score(recsys.WorkSignals{Acquired: true, ReadAt: now.Add(-400 * day)}, now, w)
		require.Equal(t, map[recsys.Kind]float64{recsys.KindAcquired: 1}, a.Parts)
	})
	t.Run("приобретено и открыто — намерение погашено", func(t *testing.T) {
		a := recsys.Score(recsys.WorkSignals{Acquired: true, Opened: true, Fraction: fp(0.3), ReadAt: now}, now, w)
		require.Empty(t, a.Parts)
	})
}

// TestScore_Decay — всё, кроме оценки, тает вдвое за HalfLife; оценка — нет.
func TestScore_Decay(t *testing.T) {
	w := recsys.DefaultWeights
	old := now.Add(-w.HalfLife)
	a := recsys.Score(recsys.WorkSignals{Rating: 5, RatedAt: old, Completed: true, CompletedAt: old, Favorite: true, ShelfAt: old}, now, w)
	require.InDelta(t, 0.5, a.Decay, 1e-9)
	require.InDelta(t, 5+0.5*(3+2), a.Score, 1e-9)
	require.Equal(t, old, a.LastAt)

	fresh := recsys.Score(recsys.WorkSignals{Completed: true, CompletedAt: now}, now, w)
	require.InDelta(t, 1, fresh.Decay, 1e-9)

	// Последнее событие любого вида продлевает свежесть всей работы.
	revived := recsys.Score(recsys.WorkSignals{Completed: true, CompletedAt: old, Views: 1, ViewedAt: now}, now, w)
	require.InDelta(t, 1, revived.Decay, 1e-9)

	noDecay := w
	noDecay.HalfLife = 0
	require.InDelta(t, 1, recsys.Score(recsys.WorkSignals{Completed: true, CompletedAt: old}, now, noDecay).Decay, 1e-9)
}

// TestScore_Combined — оценка + дочитано + подчёркивания + просмотры складываются.
func TestScore_Combined(t *testing.T) {
	a := recsys.Score(recsys.WorkSignals{
		Rating: 5, RatedAt: now, Completed: true, CompletedAt: now,
		Annotations: 3, AnnotatedAt: now, Views: 3, ViewedAt: now, Opened: true,
	}, now, recsys.DefaultWeights)
	require.InDelta(t, 5+3+1.5+1.0, a.Score, 1e-9)
	require.Equal(t, "work 0: 10.50 = rating 5 + 1.00×(annotations 1.5 + completed 3 + views 1)", a.String())
}

func TestAffinity_String(t *testing.T) {
	require.Equal(t, "work 7: 0.00 = (нет сигналов)", recsys.Affinity{WorkID: 7, Parts: map[recsys.Kind]float64{}, Decay: 1}.String())
	only := recsys.Score(recsys.WorkSignals{WorkID: 9, Rating: 2, RatedAt: now}, now, recsys.DefaultWeights)
	require.Equal(t, "work 9: -3.00 = rating -3", only.String())
	var p *recsys.Profile
	require.Equal(t, "work 1: профиль пуст", p.Explain(1))
	require.True(t, p.IsEmpty())
}
