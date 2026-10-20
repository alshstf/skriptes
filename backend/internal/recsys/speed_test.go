package recsys_test

import (
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/recsys"
	"github.com/stretchr/testify/require"
)

func TestEstimateSpeed(t *testing.T) {
	t0 := time.Date(2026, 10, 1, 20, 0, 0, 0, time.UTC)
	const size = 1_000_000 // 1 МБ
	sample := func(book int64, f float64, at time.Time) recsys.PositionSample {
		return recsys.PositionSample{BookID: book, Fraction: f, At: at, SizeBytes: size}
	}

	t.Run("одна сессия: 10 % за 20 минут = 500 байт/мин", func(t *testing.T) {
		s := recsys.EstimateSpeed([]recsys.PositionSample{
			sample(1, 0.10, t0), sample(1, 0.15, t0.Add(10*time.Minute)), sample(1, 0.20, t0.Add(20*time.Minute)),
		})
		require.True(t, s.Known())
		require.InDelta(t, 5000, s.BytesPerMin, 1e-6)
		require.Equal(t, 1, s.Sessions)
		require.Equal(t, 160, s.MinutesFor(800_000))
	})

	t.Run("разрыв 30 минут делит сессии; медиана по сессиям", func(t *testing.T) {
		s := recsys.EstimateSpeed([]recsys.PositionSample{
			sample(1, 0.0, t0), sample(1, 0.10, t0.Add(10*time.Minute)), // 10 000 б/мин
			sample(1, 0.10, t0.Add(60*time.Minute)), sample(1, 0.12, t0.Add(70*time.Minute)), // 2 000 б/мин
			sample(2, 0.0, t0.Add(24*time.Hour)), sample(2, 0.04, t0.Add(24*time.Hour+10*time.Minute)), // 4 000 б/мин
		})
		require.Equal(t, 3, s.Sessions)
		require.InDelta(t, 4000, s.BytesPerMin, 1e-6)
	})

	t.Run("листание назад не считается чтением, но время идёт", func(t *testing.T) {
		s := recsys.EstimateSpeed([]recsys.PositionSample{
			sample(1, 0.50, t0), sample(1, 0.10, t0.Add(5*time.Minute)), sample(1, 0.15, t0.Add(10*time.Minute)),
		})
		require.InDelta(t, 50_000/10.0, s.BytesPerMin, 1e-6)
	})

	t.Run("короткая сессия, большой файл, одна точка — скорости нет", func(t *testing.T) {
		require.False(t, recsys.EstimateSpeed([]recsys.PositionSample{
			sample(1, 0.1, t0), sample(1, 0.2, t0.Add(time.Minute)),
		}).Known(), "короче MinSessionDuration")
		big := recsys.PositionSample{BookID: 3, SizeBytes: recsys.MaxSpeedFileBytes + 1}
		require.False(t, recsys.EstimateSpeed([]recsys.PositionSample{
			big, {BookID: 3, Fraction: 0.5, At: t0.Add(10 * time.Minute), SizeBytes: big.SizeBytes},
		}).Known())
		require.False(t, recsys.EstimateSpeed([]recsys.PositionSample{sample(1, 0.1, t0)}).Known())
		require.False(t, recsys.EstimateSpeed(nil).Known())
		require.Equal(t, 0, recsys.Speed{}.MinutesFor(100))
	})

	t.Run("медиана — по последним 20 сессиям", func(t *testing.T) {
		var ps []recsys.PositionSample
		// 25 сессий по одной книге в разные дни: первые 5 — очень быстрые.
		for i := 0; i < 25; i++ {
			day := t0.Add(time.Duration(i) * 24 * time.Hour)
			step := 0.01
			if i < 5 {
				step = 0.5
			}
			ps = append(ps, sample(9, 0, day), sample(9, step, day.Add(10*time.Minute)))
		}
		s := recsys.EstimateSpeed(ps)
		require.Equal(t, recsys.SpeedSessions, s.Sessions)
		require.InDelta(t, 1000, s.BytesPerMin, 1e-6, "быстрые старые сессии за окном")
	})
}
