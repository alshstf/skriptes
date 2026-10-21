package recsys

import (
	"context"
	"sort"
	"time"
)

// Reread — «Перечитать» (план recsys/02, задача 2.4): работа с оценкой 5,
// прочитанная больше RereadMinAge назад и с тех пор не открывавшаяся (ни
// движения по изданию, ни приобретения), не скрытая из ленты. Одна карточка:
// детерминированный выбор по неделе года, чтобы лента не прыгала от визита к
// визиту, но раз в неделю предлагала другую. Нарочно отдаёт прочитанную работу.
type Reread struct{}

func (Reread) Name() string         { return "reread" }
func (Reread) Section() SectionKind { return SectionReread }

// RereadMinAge — сколько должно пройти с прочтения (план: три года).
const RereadMinAge = 3 * 365 * 24 * time.Hour

func (Reread) Candidates(_ context.Context, _ Querier, p *Profile, n int) ([]Candidate, error) {
	if p == nil || n <= 0 {
		return nil, nil
	}
	now := p.BuiltAt
	if now.IsZero() {
		now = time.Now()
	}
	type cand struct {
		workID int64
		at     time.Time
	}
	var elig []cand
	for id, s := range p.Signals {
		if s.Rating != 5 || s.Dismissed || s.PromptNever {
			continue
		}
		at := s.FinishedAt()
		if at.IsZero() || now.Sub(at) < RereadMinAge {
			continue
		}
		// Открывалась после прочтения — не предлагаем: пользователь и так помнит.
		if s.ReadAt.After(at.Add(24 * time.Hour)) {
			continue
		}
		elig = append(elig, cand{workID: id, at: at})
	}
	if len(elig) == 0 {
		return nil, nil
	}
	sort.Slice(elig, func(i, j int) bool { return elig[i].workID < elig[j].workID })
	year, week := now.ISOWeek()
	start := (year*53 + week) % len(elig)
	out := make([]Candidate, 0, n)
	for i := 0; i < len(elig) && len(out) < n; i++ {
		c := elig[(start+i)%len(elig)]
		out = append(out, Candidate{
			WorkID: c.workID,
			Score:  float64(len(elig) - i),
			Reason: Reason{Kind: ReasonReread, At: timePtr(c.at)},
		})
	}
	return out, nil
}
