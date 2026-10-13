package gen

import (
	"fmt"
	"strings"

	"github.com/skriptes/skriptes/backend/internal/bio"
)

// ReviewReport — markdown для ревью одного автора: что вошло, что выброшено и
// почему, что помечено.
func ReviewReport(a *bio.Author, flags []Flag, src *Sources) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s (%s)\n\n", a.Name, a.QID)
	fmt.Fprintf(&b, "Жизнь: %s — %s. Источники: ", a.Born, a.Died)
	for i, s := range a.Sources {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "%s (rev %d)", s.ID, s.Rev)
	}
	fmt.Fprintf(&b, ". Фактов Wikidata: %d.\n\n", len(src.Facts))

	major, minor := 0, 0
	for _, e := range a.Events {
		if e.Tier == bio.TierMajor {
			major++
		} else {
			minor++
		}
	}
	fmt.Fprintf(&b, "## Вехи: %d (major %d, minor %d)\n\n", len(a.Events), major, minor)
	fmt.Fprintf(&b, "| год | tier | тип | short | работы | основание |\n|---|---|---|---|---|---|\n")
	for _, e := range a.Events {
		var works []string
		for _, l := range e.Works {
			if l.Note != "" {
				works = append(works, l.Ref+" («"+l.Note+"»)")
			} else {
				works = append(works, l.Ref)
			}
		}
		ev := ""
		if e.Evidence != nil {
			ev = e.Evidence.Quote
			if r := []rune(ev); len(r) > 90 {
				ev = string(r[:90]) + "…"
			}
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n", yearLabel(e), e.Tier, e.Type, cell(e.Short), cell(strings.Join(works, "; ")), cell(ev))
	}
	b.WriteString("\n## Главы\n\n")
	for _, c := range a.Chapters {
		fmt.Fprintf(&b, "- %d–%d — %s\n", c.From, c.To, c.Title)
	}
	b.WriteString("\n## Работы и периоды написания\n\n")
	for _, w := range a.Works {
		if w.Written != nil {
			fmt.Fprintf(&b, "- %s — %d–%d", w.Title, w.Written.From, w.Written.To)
		} else {
			fmt.Fprintf(&b, "- %s — период не указан", w.Title)
		}
		if w.QID != "" {
			fmt.Fprintf(&b, " (%s)", w.QID)
		}
		b.WriteString("\n")
	}
	var dropped, flagged []Flag
	for _, f := range flags {
		if f.Dropped {
			dropped = append(dropped, f)
		} else {
			flagged = append(flagged, f)
		}
	}
	fmt.Fprintf(&b, "\n## Выброшено проверкой: %d\n\n", len(dropped))
	for _, f := range dropped {
		fmt.Fprintf(&b, "- `%s` — %s\n", f.Key, f.Msg)
	}
	fmt.Fprintf(&b, "\n## Замечания: %d\n\n", len(flagged))
	for _, f := range flagged {
		fmt.Fprintf(&b, "- `%s` — %s\n", f.Key, f.Msg)
	}
	return b.String()
}

func yearLabel(e bio.Event) string {
	a := bio.YearOf(e.Date.From)
	if b := e.EndYear(); b > a {
		return fmt.Sprintf("%d–%d", a, b)
	}
	return fmt.Sprint(a)
}

func cell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", "\\|"), "\n", " ")
}
