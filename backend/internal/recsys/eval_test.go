package recsys_test

import (
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/recsys"
	"github.com/stretchr/testify/require"
)

func TestLeaveLastOut(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sig := map[int64]*recsys.WorkSignals{
		1: {WorkID: 1, Rating: 5, RatedAt: t0.Add(3 * time.Hour)},           // положительная, самая свежая
		2: {WorkID: 2, Completed: true, CompletedAt: t0.Add(2 * time.Hour)}, // положительная
		3: {WorkID: 3, Rating: 2, RatedAt: t0.Add(5 * time.Hour)},           // негатив — не прячем
		4: {WorkID: 4, Views: 3, ViewedAt: t0.Add(9 * time.Hour)},           // просмотр — не положительная
		5: {WorkID: 5, Rating: 4, RatedAt: t0.Add(1 * time.Hour)},           // положительная, старая
	}
	hidden, rest := recsys.LeaveLastOut(sig, 2)
	require.Equal(t, []int64{1, 2}, hidden)
	require.Len(t, rest, 3)
	require.Contains(t, rest, int64(5))
	require.Len(t, sig, 5, "исходная карта не меняется")

	hidden, rest = recsys.LeaveLastOut(sig, 10)
	require.Equal(t, []int64{1, 2, 5}, hidden, "k больше положительных — все")
	require.Len(t, rest, 2)
	_, ok := recsys.PositiveAt(nil)
	require.False(t, ok)
}

func TestMetrics(t *testing.T) {
	m := recsys.NewMetrics()
	popular := map[int64]struct{}{10: {}, 11: {}}
	recs := []int64{10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21}
	// пользователь 1: спрятаны 12 (ранг 3) и 21 (ранг 12) → hit10 1, hit50 2, RR 1/3
	m.Add(recs, []int64{12, 21}, popular)
	// пользователь 2: спрятана 99 — не найдена
	m.Add([]int64{10, 50}, []int64{99}, popular)
	s := m.Summary(100)
	require.Equal(t, 2, s.Users)
	require.Equal(t, 3, s.Hidden)
	require.InDelta(t, 1.0/3, s.Hit10, 1e-9)
	require.InDelta(t, 2.0/3, s.Hit50, 1e-9)
	require.InDelta(t, (1.0/3)/2, s.MRR, 1e-9)
	require.InDelta(t, 13.0/100, s.Coverage, 1e-9, "12 + 50 — 13 разных работ")
	require.InDelta(t, 11.0/14, s.Novelty, 1e-9, "из 14 рекомендаций 3 популярных")
	require.Equal(t, 0, recsys.Rank(recs, 999))
	require.Equal(t, 1, recsys.Rank(recs, 10))
}
