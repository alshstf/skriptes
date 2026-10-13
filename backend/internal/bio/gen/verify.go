package gen

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/skriptes/skriptes/backend/internal/bio"
)

// Flag — замечание проверки к вехе (или файлу). Dropped — веха выброшена.
type Flag struct {
	Key     string
	Msg     string
	Dropped bool
}

func (f Flag) String() string {
	if f.Dropped {
		return "DROP " + f.Key + ": " + f.Msg
	}
	return "flag " + f.Key + ": " + f.Msg
}

// Verify — автопроверка черновика по источникам (план v3, шаг 4): основание
// находится в тексте; год вехи есть в основании или в факте Wikidata;
// утраты/браки/дети сверяются с Wikidata (±1 год); правила датасета
// (bio.Validate). Отбраковка, не правка: веха с проваленной жёсткой проверкой
// выбрасывается, остальное — флаг в отчёт.
func Verify(a *bio.Author, src *Sources) (*bio.Author, []Flag) {
	var flags []Flag
	norm := make([]string, 0, len(src.Articles))
	for _, art := range src.Articles {
		norm = append(norm, normalize(art.Text))
	}
	factIndex := buildFactIndex(src)

	out := *a
	out.Events = nil
	for _, e := range a.Events {
		if e.Type == bio.EventBirth || e.Type == bio.EventDeath {
			out.Events = append(out.Events, e)
			continue
		}
		year := bio.YearOf(e.Date.From)
		if e.Evidence == nil || strings.TrimSpace(e.Evidence.Quote) == "" {
			flags = append(flags, Flag{Key: e.Key, Msg: "нет основания", Dropped: true})
			continue
		}
		q := e.Evidence.Quote
		if strings.HasPrefix(strings.ToLower(q), "wikidata ") {
			// Факт только из Wikidata: год должен совпасть с одним из фактов.
			if !factIndex.hasYear(year) {
				flags = append(flags, Flag{Key: e.Key, Msg: fmt.Sprintf("основание ссылается на Wikidata, но факта с годом %d нет", year), Dropped: true})
				continue
			}
		} else {
			if !quoteFound(q, norm) {
				flags = append(flags, Flag{Key: e.Key, Msg: "цитата-основание не найдена в тексте статьи", Dropped: true})
				continue
			}
			if !containsYear(q, year) && !factIndex.hasYear(year) {
				// Год есть не в цитате и не в фактах — вероятно, модель
				// перенесла дату из другого места. Жёстко.
				flags = append(flags, Flag{Key: e.Key, Msg: fmt.Sprintf("год %d не найден ни в основании, ни в фактах Wikidata", year), Dropped: true})
				continue
			}
		}
		// Сверка с Wikidata по типу: утрата/брак/ребёнок — есть ли факт в ±1 год.
		switch e.Type {
		case bio.EventLoss:
			if !factIndex.has("died", year) && factIndex.anyRelatives() {
				flags = append(flags, Flag{Key: e.Key, Msg: fmt.Sprintf("утрата в %d: в Wikidata нет смерти родных в этот год (±1)", year)})
			}
		case bio.EventLove:
			if factIndex.anyOf("P26") && !factIndex.has("P26", year) {
				flags = append(flags, Flag{Key: e.Key, Msg: fmt.Sprintf("брак/любовь в %d: не совпадает с браками Wikidata (±1)", year)})
			}
		case bio.EventChild:
			if factIndex.anyOf("P40") && !factIndex.has("P40", year) {
				flags = append(flags, Flag{Key: e.Key, Msg: fmt.Sprintf("рождение ребёнка в %d: в Wikidata нет ребёнка с таким годом (±1)", year)})
			}
		}
		out.Events = append(out.Events, e)
	}

	// Правила датасета: ошибки по конкретной вехе — выбрасываем веху, ошибки
	// файла — флаг (файл не принимается без ручной правки).
	for {
		ps := bio.Validate(&out).Errors()
		if len(ps) == 0 {
			break
		}
		dropped := false
		for _, p := range ps {
			if k := eventKeyFromPath(p.Path); k != "" {
				if removeEvent(&out, k) {
					flags = append(flags, Flag{Key: k, Msg: p.Msg, Dropped: true})
					dropped = true
				}
			} else {
				flags = append(flags, Flag{Key: p.Path, Msg: p.Msg})
			}
		}
		if !dropped {
			break
		}
	}
	for _, p := range bio.Validate(&out) {
		if p.Severity == "warn" {
			flags = append(flags, Flag{Key: p.Path, Msg: p.Msg})
		}
	}
	return &out, flags
}

var eventPathRe = regexp.MustCompile(`^events\[\d+:([^\]]+)\]`)

func eventKeyFromPath(path string) string {
	if m := eventPathRe.FindStringSubmatch(path); m != nil {
		return m[1]
	}
	return ""
}

func removeEvent(a *bio.Author, key string) bool {
	for i, e := range a.Events {
		if e.Key == key {
			a.Events = append(a.Events[:i:i], a.Events[i+1:]...)
			return true
		}
	}
	return false
}

// normalize — нижний регистр, ё→е, только буквы/цифры и одиночные пробелы:
// цитата модели может отличаться типографикой (кавычки, тире, неразрывные
// пробелы, сноски).
func normalize(s string) string {
	var b strings.Builder
	space := true
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.Is(unicode.Mn, r):
			continue // ударения и прочие комбинируемые знаки: «Михаи́л» = «Михаил»
		case r == 'ё':
			b.WriteRune('е')
			space = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			space = false
		default:
			if !space {
				b.WriteRune(' ')
				space = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// quoteFound — цитата как подстрока нормализованного текста. Цитата может
// состоять из нескольких фрагментов через «…» (соседние предложения, пропуск
// середины): каждый фрагмент длиной ≥ 12 символов должен найтись. Длинный
// цельный фрагмент (> 120 символов) принимаем по первым и последним 60 —
// модель иногда теряет середину без многоточия.
func quoteFound(raw string, texts []string) bool {
	var parts []string
	for _, p := range strings.FieldsFunc(raw, func(r rune) bool { return r == '…' }) {
		for _, pp := range strings.Split(p, "...") {
			if n := normalize(pp); len([]rune(n)) >= 12 {
				parts = append(parts, n)
			}
		}
	}
	if len(parts) == 0 {
		return false
	}
	for _, t := range texts {
		ok := true
		for _, q := range parts {
			if strings.Contains(t, q) {
				continue
			}
			r := []rune(q)
			if len(r) > 120 && strings.Contains(t, string(r[:60])) && strings.Contains(t, string(r[len(r)-60:])) {
				continue
			}
			ok = false
			break
		}
		if ok {
			return true
		}
	}
	return false
}

var yearInText = regexp.MustCompile(`\b1\d{3}\b|\b20\d{2}\b`)

func containsYear(s string, year int) bool {
	y := strconv.Itoa(year)
	for _, m := range yearInText.FindAllString(s, -1) {
		if m == y {
			return true
		}
	}
	return false
}

type factIndex struct {
	byProp map[string]map[int]bool // prop → годы (date/start/end)
	died   map[int]bool            // годы смерти родных (P22/P25/P40/P3373/P26)
	years  map[int]bool            // все годы всех фактов
	rel    bool
}

func buildFactIndex(src *Sources) *factIndex {
	fi := &factIndex{byProp: map[string]map[int]bool{}, died: map[int]bool{}, years: map[int]bool{}}
	add := func(prop string, iso string) {
		y := bio.YearOf(iso)
		if y == 0 {
			return
		}
		if fi.byProp[prop] == nil {
			fi.byProp[prop] = map[int]bool{}
		}
		fi.byProp[prop][y] = true
		fi.years[y] = true
	}
	for _, f := range src.Facts {
		add(f.Prop, f.Date)
		add(f.Prop, f.Start)
		add(f.Prop, f.End)
		switch f.Prop {
		case "P22", "P25", "P40", "P3373", "P26":
			fi.rel = true
			if y := bio.YearOf(f.Died); y > 0 {
				fi.died[y] = true
				fi.years[y] = true
			}
			if y := bio.YearOf(f.Born); y > 0 && f.Prop == "P40" {
				add("P40", f.Born)
			}
		}
	}
	return fi
}

func (fi *factIndex) hasYear(y int) bool { return fi.years[y] || fi.years[y-1] || fi.years[y+1] }
func (fi *factIndex) anyOf(prop string) bool {
	return len(fi.byProp[prop]) > 0
}
func (fi *factIndex) anyRelatives() bool { return fi.rel && len(fi.died) > 0 }
func (fi *factIndex) has(prop string, y int) bool {
	m := fi.byProp[prop]
	if prop == "died" {
		m = fi.died
	}
	return m[y] || m[y-1] || m[y+1]
}
