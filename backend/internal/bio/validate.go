package bio

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Problem — нарушение правил датасета. Error — файл не принимается (загрузчик
// падает, генератор отбраковывает); Warn — показывается в отчёте ревью.
type Problem struct {
	Severity string // "error" | "warn"
	Path     string // "events[loss-1868].short"
	Msg      string
}

func (p Problem) String() string { return p.Severity + " " + p.Path + ": " + p.Msg }

// Problems — список с удобными фильтрами.
type Problems []Problem

// Errors — только ошибки.
func (ps Problems) Errors() Problems {
	var out Problems
	for _, p := range ps {
		if p.Severity == "error" {
			out = append(out, p)
		}
	}
	return out
}

func (ps Problems) Error() string {
	parts := make([]string, 0, len(ps))
	for _, p := range ps {
		parts = append(parts, p.String())
	}
	return strings.Join(parts, "; ")
}

var (
	qidRe      = regexp.MustCompile(`^Q[1-9]\d*$`)
	keyRe      = regexp.MustCompile(`^([a-z]+)-(\d{4})(?:-\d+)?$`)
	refRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	yearRe     = regexp.MustCompile(`^\d{4}$`)
	monthRe    = regexp.MustCompile(`^\d{4}-\d{2}$`)
	dayRe      = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	fourDigits = regexp.MustCompile(`\d{4}`)
)

// YearOf — год из ISO-строки любой точности; 0, если не разобрать.
func YearOf(iso string) int {
	if len(iso) < 4 {
		return 0
	}
	y, err := strconv.Atoi(iso[:4])
	if err != nil {
		return 0
	}
	return y
}

// EndYear — год окончания вехи: To у периода, иначе From.
func (e Event) EndYear() int {
	if e.To() != "" {
		return YearOf(e.To())
	}
	return YearOf(e.Date.From)
}

// To — удобный доступ к концу периода.
func (e Event) To() string { return e.Date.To }

// IsPeriod — веха-период (каторга, эмиграция): есть To и он позже From по году.
func (e Event) IsPeriod() bool {
	return e.Date.To != "" && YearOf(e.Date.To) > YearOf(e.Date.From)
}

func validISO(s string, p Precision) bool {
	switch p {
	case PrecisionYear:
		return yearRe.MatchString(s)
	case PrecisionMonth:
		if !monthRe.MatchString(s) {
			return false
		}
		_, err := time.Parse("2006-01", s)
		return err == nil
	case PrecisionDay:
		if !dayRe.MatchString(s) {
			return false
		}
		_, err := time.Parse("2006-01-02", s)
		return err == nil
	}
	return false
}

// validLifeDate — born/died: любая из трёх точностей.
func validLifeDate(s string) bool {
	return validISO(s, PrecisionYear) || validISO(s, PrecisionMonth) || validISO(s, PrecisionDay)
}

func isEventType(t EventType) bool {
	for _, k := range EventTypes {
		if k == t {
			return true
		}
	}
	return false
}

// Validate — все правила датасета для одного автора (план v3, «Принципы
// данных»). Возвращает и ошибки, и предупреждения; пустой список — всё чисто.
func Validate(a *Author) Problems {
	var ps Problems
	errf := func(path, format string, args ...any) {
		ps = append(ps, Problem{Severity: "error", Path: path, Msg: fmt.Sprintf(format, args...)})
	}
	warnf := func(path, format string, args ...any) {
		ps = append(ps, Problem{Severity: "warn", Path: path, Msg: fmt.Sprintf(format, args...)})
	}

	if !qidRe.MatchString(a.QID) {
		errf("qid", "ожидается Q<число>, получено %q", a.QID)
	}
	if strings.TrimSpace(a.Name) == "" {
		errf("name", "пусто")
	}
	if !validLifeDate(a.Born) {
		errf("born", "не ISO-дата: %q", a.Born)
	}
	if a.Died != "" && !validLifeDate(a.Died) {
		errf("died", "не ISO-дата: %q", a.Died)
	}
	born := YearOf(a.Born)
	died := YearOf(a.Died)
	if born > 0 && died > 0 && died < born {
		errf("died", "раньше рождения")
	}
	if len(a.Sources) == 0 {
		warnf("sources", "нет источников для атрибуции")
	}

	// Главы: уникальные ключи, from ≤ to, по порядку, внутри жизни.
	chapterKeys := map[string]bool{}
	prevTo := 0
	for i, c := range a.Chapters {
		path := fmt.Sprintf("chapters[%d]", i)
		if c.Key == "" || !refRe.MatchString(c.Key) {
			errf(path+".key", "ключ %q: ожидается [a-z0-9-]", c.Key)
		}
		if chapterKeys[c.Key] {
			errf(path+".key", "дубль %q", c.Key)
		}
		chapterKeys[c.Key] = true
		if strings.TrimSpace(c.Title) == "" {
			errf(path+".title", "пусто")
		}
		if c.From > c.To {
			errf(path, "from %d > to %d", c.From, c.To)
		}
		if i > 0 && c.From <= prevTo {
			errf(path, "главы пересекаются или не по порядку (%d ≤ %d)", c.From, prevTo)
		}
		prevTo = c.To
		if born > 0 && c.From < born-1 {
			errf(path, "начинается до рождения")
		}
		if died > 0 && c.To > died+1 {
			errf(path, "заканчивается после смерти")
		}
	}

	// Работы: уникальные ref, период внутри жизни.
	workRefs := map[string]bool{}
	var titles []string
	for i, w := range a.Works {
		path := fmt.Sprintf("works[%d]", i)
		if !refRe.MatchString(w.Ref) {
			errf(path+".ref", "ожидается [a-z0-9-], получено %q", w.Ref)
		}
		if workRefs[w.Ref] {
			errf(path+".ref", "дубль %q", w.Ref)
		}
		workRefs[w.Ref] = true
		if strings.TrimSpace(w.Title) == "" {
			errf(path+".title", "пусто")
		} else {
			titles = append(titles, w.Title)
		}
		if w.QID != "" && !qidRe.MatchString(w.QID) {
			errf(path+".qid", "не QID: %q", w.QID)
		}
		if w.Written != nil {
			if w.Written.From > w.Written.To {
				errf(path+".written", "from %d > to %d", w.Written.From, w.Written.To)
			}
			if born > 0 && w.Written.From < born {
				errf(path+".written", "раньше рождения")
			}
			if died > 0 && w.Written.To > died {
				errf(path+".written", "после смерти")
			}
		}
	}

	// Вехи.
	keys := map[string]bool{}
	seenTypeYear := map[string]int{}
	major, total := 0, 0
	for i, e := range a.Events {
		path := fmt.Sprintf("events[%d:%s]", i, e.Key)
		total++
		if e.Tier == TierMajor {
			if e.Type != EventBirth && e.Type != EventDeath {
				major++
			}
		} else if e.Tier != TierMinor {
			errf(path+".tier", "ожидается major|minor, получено %q", e.Tier)
		}
		if !isEventType(e.Type) {
			errf(path+".type", "неизвестный тип %q", e.Type)
		}
		m := keyRe.FindStringSubmatch(e.Key)
		if m == nil {
			errf(path+".key", "ожидается <type>-<yyyy>[-<n>], получено %q", e.Key)
		} else {
			if m[1] != string(e.Type) {
				errf(path+".key", "тип в ключе %q ≠ type %q", m[1], e.Type)
			}
			if y := YearOf(e.Date.From); y > 0 && m[2] != strconv.Itoa(y) {
				errf(path+".key", "год в ключе %s ≠ год даты %d", m[2], y)
			}
		}
		if keys[e.Key] {
			errf(path+".key", "дубль ключа")
		}
		keys[e.Key] = true

		if !validISO(e.Date.From, e.Date.Precision) {
			errf(path+".date.from", "%q не соответствует точности %q", e.Date.From, e.Date.Precision)
		}
		if e.Date.To != "" {
			if !validISO(e.Date.To, e.Date.Precision) {
				errf(path+".date.to", "%q не соответствует точности %q", e.Date.To, e.Date.Precision)
			} else if e.Date.To < e.Date.From {
				errf(path+".date.to", "раньше from")
			}
		}
		y := YearOf(e.Date.From)
		if born > 0 && y > 0 && y < born-1 {
			errf(path+".date", "год %d раньше рождения %d", y, born)
		}
		if died > 0 && y > 0 && y > died+1 {
			errf(path+".date", "год %d позже смерти %d", y, died)
		}
		if e.Type != EventBirth && e.Type != EventDeath && !e.IsPeriod() {
			ty := string(e.Type) + "-" + strconv.Itoa(y)
			seenTypeYear[ty]++
			if seenTypeYear[ty] == 3 {
				warnf(path, "третья веха типа %s в %d году — не дубли ли", e.Type, y)
			}
		}

		// short: 1..60 символов, без года и названий работ.
		short := strings.TrimSpace(e.Short)
		switch n := utf8.RuneCountInString(short); {
		case n == 0:
			errf(path+".short", "пусто")
		case n > MaxShortRunes:
			errf(path+".short", "%d символов > %d", n, MaxShortRunes)
		}
		if fourDigits.MatchString(short) {
			errf(path+".short", "год в формулировке: %q", short)
		}
		if t := titleIn(short, titles); t != "" {
			errf(path+".short", "название работы «%s» в формулировке", t)
		}
		if n := utf8.RuneCountInString(e.Long); n > MaxLongRunes {
			errf(path+".long", "%d символов > %d", n, MaxLongRunes)
		}
		if e.Chapter != "" && len(a.Chapters) > 0 && !chapterKeys[e.Chapter] {
			errf(path+".chapter", "нет главы %q", e.Chapter)
		}
		if e.Chapter == "" && len(a.Chapters) > 0 {
			warnf(path+".chapter", "веха без главы")
		}
		for j, l := range e.Works {
			lp := fmt.Sprintf("%s.works[%d]", path, j)
			if !workRefs[l.Ref] {
				errf(lp+".ref", "нет работы %q", l.Ref)
			}
			if n := utf8.RuneCountInString(l.Note); n > MaxNoteRunes {
				errf(lp+".note", "%d символов > %d", n, MaxNoteRunes)
			}
		}
		if e.Evidence == nil && e.Type != EventBirth && e.Type != EventDeath {
			warnf(path+".evidence", "нет основания (цитаты из источника)")
		}
	}
	if major > MaxMajorEvents {
		errf("events", "major-вех %d > %d", major, MaxMajorEvents)
	}
	if total > MaxEvents {
		errf("events", "вех %d > %d", total, MaxEvents)
	}
	if !sort.SliceIsSorted(a.Events, func(i, j int) bool { return a.Events[i].Date.From < a.Events[j].Date.From }) {
		warnf("events", "не отсортированы по дате")
	}
	return ps
}

// titleIn — название работы (≥ 4 символов) встречается в формулировке; «Игрок»
// в «Роман надиктован…» не встречается, «Идиот» в «Выход „Идиота“» — да.
func titleIn(short string, titles []string) string {
	ls := strings.ToLower(short)
	for _, t := range titles {
		lt := strings.ToLower(strings.TrimSpace(t))
		if utf8.RuneCountInString(lt) < 4 {
			continue
		}
		if strings.Contains(ls, lt) {
			return t
		}
	}
	return ""
}
