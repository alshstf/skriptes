package recsys

import (
	"sort"
	"time"
)

// Харнесс оценки (план recsys/01, задача 1.5), чистая часть: leave-last-out
// по сигналам пользователя и метрики по рангам спрятанных работ. Прогон —
// cmd/recseval.

// PositiveAt — работа «понравилась» (оценка ≥ 4 или дочитана) и когда; такие
// прячутся в leave-last-out и должны найтись рекомендателем.
func PositiveAt(s *WorkSignals) (time.Time, bool) {
	switch {
	case s == nil:
		return time.Time{}, false
	case s.Rating >= 4:
		return s.RatedAt, true
	case s.Completed:
		return s.CompletedAt, true
	}
	return time.Time{}, false
}

// LeaveLastOut — k последних положительных работ прячутся (hidden, новые
// первыми), остальные сигналы — rest (копия карты, исходная не меняется).
func LeaveLastOut(signals map[int64]*WorkSignals, k int) (hidden []int64, rest map[int64]*WorkSignals) {
	type pos struct {
		id int64
		at time.Time
	}
	var ps []pos
	for id, s := range signals {
		if at, ok := PositiveAt(s); ok {
			ps = append(ps, pos{id, at})
		}
	}
	sort.Slice(ps, func(i, j int) bool {
		if !ps[i].at.Equal(ps[j].at) {
			return ps[i].at.After(ps[j].at)
		}
		return ps[i].id > ps[j].id
	})
	if k > len(ps) {
		k = len(ps)
	}
	rest = make(map[int64]*WorkSignals, len(signals))
	for id, s := range signals {
		rest[id] = s
	}
	for _, p := range ps[:k] {
		hidden = append(hidden, p.id)
		delete(rest, p.id)
	}
	return hidden, rest
}

// Rank — позиция id в списке (1 — первый), 0 — нет.
func Rank(list []int64, id int64) int {
	for i, v := range list {
		if v == id {
			return i + 1
		}
	}
	return 0
}

// Metrics — агрегат по прогонам одного рекомендателя.
type Metrics struct {
	Users   int     // пользователей
	Hidden  int     // спрятанных работ всего
	Hit10   int     // спрятанных, попавших в топ-10
	Hit50   int     // … в топ-50
	RRSum   float64 // Σ 1/rank по пользователям (лучший ранг среди спрятанных)
	Recs    map[int64]struct{}
	Novel   int // рекомендаций вне «популярного топа»
	RecsAll int // рекомендаций всего (с повторами между пользователями)
}

func NewMetrics() *Metrics { return &Metrics{Recs: map[int64]struct{}{}} }

// Add — результат одного пользователя: список рекомендаций, спрятанные работы,
// popular — множество работ «популярного топа» (для новизны).
func (m *Metrics) Add(recs, hidden []int64, popular map[int64]struct{}) {
	m.Users++
	best := 0
	for _, h := range hidden {
		m.Hidden++
		r := Rank(recs, h)
		if r == 0 {
			continue
		}
		if r <= 10 {
			m.Hit10++
		}
		if r <= 50 {
			m.Hit50++
		}
		if best == 0 || r < best {
			best = r
		}
	}
	if best > 0 {
		m.RRSum += 1 / float64(best)
	}
	for _, id := range recs {
		m.Recs[id] = struct{}{}
		m.RecsAll++
		if _, ok := popular[id]; !ok {
			m.Novel++
		}
	}
}

// Summary — доли: hit@10 и hit@50 по спрятанным, MRR по пользователям,
// покрытие = разных рекомендованных / catalog, новизна = доля рекомендаций вне
// популярного топа.
type Summary struct {
	Users    int
	Hidden   int
	Hit10    float64
	Hit50    float64
	MRR      float64
	Coverage float64
	Novelty  float64
}

func (m *Metrics) Summary(catalog int64) Summary {
	s := Summary{Users: m.Users, Hidden: m.Hidden}
	if m.Hidden > 0 {
		s.Hit10 = float64(m.Hit10) / float64(m.Hidden)
		s.Hit50 = float64(m.Hit50) / float64(m.Hidden)
	}
	if m.Users > 0 {
		s.MRR = m.RRSum / float64(m.Users)
	}
	if catalog > 0 {
		s.Coverage = float64(len(m.Recs)) / float64(catalog)
	}
	if m.RecsAll > 0 {
		s.Novelty = float64(m.Novel) / float64(m.RecsAll)
	}
	return s
}
