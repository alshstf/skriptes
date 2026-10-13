package gen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/skriptes/skriptes/backend/internal/bio"
)

// EvalResult — сравнение сгенерированного файла с золотым (ручным) по одному
// автору. Критерии плана: recall major-вех ≥ 80 %, 0 ошибок дат в major,
// формат 100 %.
type EvalResult struct {
	QID          string
	GoldenMajor  int
	HitMajor     int      // major золотого набора, найденные в генерации (тип + год)
	MissedMajor  []string // short золотых major без пары
	ExtraMajor   []string // major генерации без пары в золотом (кандидаты на precision-ревью)
	DateErrors   []string // тип совпал, год ±1–3 — вероятная ошибка даты
	FormatErrors int      // ошибок bio.Validate у сгенерированного
}

// Recall — доля найденных major.
func (r EvalResult) Recall() float64 {
	if r.GoldenMajor == 0 {
		return 1
	}
	return float64(r.HitMajor) / float64(r.GoldenMajor)
}

// Eval — сопоставление вех по (тип, год); якоря рождения/смерти не считаются.
func Eval(golden, generated *bio.Author) EvalResult {
	r := EvalResult{QID: golden.QID, FormatErrors: len(bio.Validate(generated).Errors())}
	type k struct {
		t bio.EventType
		y int
	}
	gen := map[k][]bio.Event{}
	for _, e := range generated.Events {
		gen[k{e.Type, bio.YearOf(e.Date.From)}] = append(gen[k{e.Type, bio.YearOf(e.Date.From)}], e)
	}
	matchedGen := map[string]bool{}
	for _, g := range golden.Events {
		if g.Tier != bio.TierMajor || g.Type == bio.EventBirth || g.Type == bio.EventDeath {
			continue
		}
		r.GoldenMajor++
		y := bio.YearOf(g.Date.From)
		if c := take(gen, k{g.Type, y}); c != nil {
			r.HitMajor++
			matchedGen[c.Key] = true
			continue
		}
		near := ""
		for d := 1; d <= 3 && near == ""; d++ {
			for _, dy := range []int{y - d, y + d} {
				if c := take(gen, k{g.Type, dy}); c != nil {
					near = fmt.Sprintf("%s: золото %d, генерация %d (%s)", g.Short, y, dy, c.Short)
					matchedGen[c.Key] = true
					break
				}
			}
		}
		if near != "" {
			r.DateErrors = append(r.DateErrors, near)
		} else {
			r.MissedMajor = append(r.MissedMajor, fmt.Sprintf("%d %s", y, g.Short))
		}
	}
	for _, e := range generated.Events {
		if e.Tier == bio.TierMajor && e.Type != bio.EventBirth && e.Type != bio.EventDeath && !matchedGen[e.Key] {
			r.ExtraMajor = append(r.ExtraMajor, fmt.Sprintf("%d %s", bio.YearOf(e.Date.From), e.Short))
		}
	}
	sort.Strings(r.MissedMajor)
	sort.Strings(r.ExtraMajor)
	return r
}

// take — первый ещё не использованный кандидат по ключу; каждая сгенерированная
// веха закрывает не больше одной золотой (две утраты в один год — две пары).
func take[K comparable](m map[K][]bio.Event, key K) *bio.Event {
	cands := m[key]
	if len(cands) == 0 {
		return nil
	}
	c := cands[0]
	m[key] = cands[1:]
	return &c
}

// Report — текстовый отчёт по нескольким авторам.
func Report(results []EvalResult) string {
	var b strings.Builder
	totalG, totalH := 0, 0
	for _, r := range results {
		totalG += r.GoldenMajor
		totalH += r.HitMajor
		fmt.Fprintf(&b, "%s: recall %.0f%% (%d/%d), ошибок дат %d, лишних major %d, ошибок формата %d\n",
			r.QID, r.Recall()*100, r.HitMajor, r.GoldenMajor, len(r.DateErrors), len(r.ExtraMajor), r.FormatErrors)
		for _, m := range r.MissedMajor {
			fmt.Fprintf(&b, "  пропущено: %s\n", m)
		}
		for _, d := range r.DateErrors {
			fmt.Fprintf(&b, "  дата: %s\n", d)
		}
		for _, x := range r.ExtraMajor {
			fmt.Fprintf(&b, "  лишнее major: %s\n", x)
		}
	}
	if totalG > 0 {
		fmt.Fprintf(&b, "итого recall major: %.0f%% (%d/%d)\n", float64(totalH)/float64(totalG)*100, totalH, totalG)
	}
	return b.String()
}
