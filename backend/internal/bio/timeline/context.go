// Package timeline — runtime био-таймлайна: сев встроенного датасета в PG,
// чтение таймлайна автора, окно «в жизни автора в это время» для карточки
// книги, админское «скрыть веху». Внешних вызовов нет.
package timeline

import (
	"sort"

	"github.com/skriptes/skriptes/backend/internal/bio"
)

// Relation — как веха относится к периоду написания книги.
type Relation struct {
	// Kind: during (внутри периода написания), same_year (период — один
	// год), before (за Years лет до начала работы).
	Kind  string `json:"kind"`
	Years int    `json:"years,omitempty"`
}

// ContextItem — строка окна карточки книги.
type ContextItem struct {
	Event    bio.Event `json:"event"`
	Relation Relation  `json:"relation"`
	Linked   bool      `json:"linked"`
	Note     string    `json:"note,omitempty"`
}

func relationTo(e bio.Event, p bio.Written) (Relation, bool) {
	a := bio.YearOf(e.Date.From)
	b := e.EndYear()
	if a <= p.To && b >= p.From {
		if p.From == p.To {
			return Relation{Kind: "same_year"}, true
		}
		return Relation{Kind: "during"}, true
	}
	if b < p.From {
		return Relation{Kind: "before", Years: p.From - b}, true
	}
	return Relation{}, false // после написания — не показываем
}

// Window — окно вех вокруг периода написания (план v3, API life-context).
// Приоритет отбора: линкованные на работу → major в окне → major за ≤ 2 года
// до → minor в окне → если набралось < 2, ближайшая major за ≤ 10 лет до.
// Кап 4; показ — хронологически. Якоря рождения/смерти — только по линку.
// hidden — скрытые админом ключи, не попадают никогда.
func Window(a *bio.Author, ref string, p bio.Written, hidden map[string]bool) []ContextItem {
	type cand struct {
		e   bio.Event
		rel Relation
		l   *bio.WorkLink
	}
	var cands []cand
	for _, e := range a.Events {
		if hidden[e.Key] {
			continue
		}
		rel, ok := relationTo(e, p)
		if !ok {
			continue
		}
		var link *bio.WorkLink
		for i := range e.Works {
			if ref != "" && e.Works[i].Ref == ref {
				link = &e.Works[i]
				break
			}
		}
		anchor := e.Type == bio.EventBirth || e.Type == bio.EventDeath
		if anchor && link == nil {
			continue
		}
		cands = append(cands, cand{e: e, rel: rel, l: link})
	}
	picked := map[string]bool{}
	var out []ContextItem
	take := func(pred func(c cand) bool) {
		for _, c := range cands {
			if len(out) >= 4 || picked[c.e.Key] || !pred(c) {
				continue
			}
			picked[c.e.Key] = true
			it := ContextItem{Event: c.e, Relation: c.rel, Linked: c.l != nil}
			if c.l != nil {
				it.Note = c.l.Note
			}
			out = append(out, it)
		}
	}
	inWindow := func(c cand) bool { return c.rel.Kind != "before" }
	before := func(c cand, max int) bool { return c.rel.Kind == "before" && c.rel.Years <= max }
	take(func(c cand) bool { return c.l != nil })
	take(func(c cand) bool { return c.e.Tier == bio.TierMajor && inWindow(c) })
	take(func(c cand) bool { return c.e.Tier == bio.TierMajor && before(c, 2) })
	take(func(c cand) bool { return c.e.Tier == bio.TierMinor && inWindow(c) })
	if len(out) < 2 {
		best := -1
		for i, c := range cands {
			if picked[c.e.Key] || c.e.Tier != bio.TierMajor || !before(c, 10) {
				continue
			}
			if best < 0 || c.e.EndYear() > cands[best].e.EndYear() {
				best = i
			}
		}
		if best >= 0 {
			c := cands[best]
			picked[c.e.Key] = true
			out = append(out, ContextItem{Event: c.e, Relation: c.rel})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Event.Date.From < out[j].Event.Date.From })
	return out
}
