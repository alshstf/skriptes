package recsys

import (
	"context"
	"sort"
	"time"
)

// ToReadReminder — «Вы хотели прочитать» (план recsys/03, задача 3.3): работы
// с полки «Хочу прочитать», лежащие там дольше ToReadMinAge и с тех пор не
// тронутые (не прочитаны, не открыты, не приобретены, не скрыты — Engaged).
// Напоминание, а не лента: состав меняется раз в неделю (сдвиг по ISO-неделе,
// как у «Перечитать»), чтобы одни и те же книги не маячили каждый день.
// Порядок — дольше лежит, тот первее.
type ToReadReminder struct{}

func (ToReadReminder) Name() string         { return "to_read_reminder" }
func (ToReadReminder) Section() SectionKind { return SectionToRead }

// ToReadMinAge — сколько книга должна пролежать на полке, прежде чем напомнить.
const ToReadMinAge = 60 * 24 * time.Hour

func (ToReadReminder) Candidates(_ context.Context, _ Querier, p *Profile, n int) ([]Candidate, error) {
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
		if !s.ToRead || s.ToReadAt.IsZero() || now.Sub(s.ToReadAt) < ToReadMinAge || p.Engaged(id) {
			continue
		}
		elig = append(elig, cand{workID: id, at: s.ToReadAt})
	}
	if len(elig) == 0 {
		return nil, nil
	}
	sort.Slice(elig, func(i, j int) bool {
		if !elig[i].at.Equal(elig[j].at) {
			return elig[i].at.Before(elig[j].at)
		}
		return elig[i].workID < elig[j].workID
	})
	year, week := now.ISOWeek()
	start := (year*53 + week) % len(elig)
	out := make([]Candidate, 0, n)
	for i := 0; i < len(elig) && len(out) < n; i++ {
		c := elig[(start+i)%len(elig)]
		out = append(out, Candidate{
			WorkID: c.workID,
			Score:  float64(len(elig) - i),
			Reason: Reason{Kind: ReasonToRead, At: timePtr(c.at)},
		})
	}
	return out, nil
}
