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

	// Механические огрехи модели правим, а не выбрасываем: snake_case в ключах,
	// длинный long, лишние major (этап B: у 7 авторов из 29 файл не грузился из-за
	// «time_machine», 21 major, long в 301 символ).
	flags = append(flags, normalizeRefs(&out)...)
	flags = append(flags, trimTexts(&out)...)
	flags = append(flags, fixWritten(&out)...)
	flags = append(flags, demoteExtraMajors(&out)...)
	flags = append(flags, capEvents(&out)...)

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

var slugDropRe = regexp.MustCompile(`[^a-z0-9-]+`)

// slug — ключ в формате датасета [a-z0-9-]: нижний регистр, подчёркивания и
// пробелы → дефис, остальное выбрасывается, дефисы схлопываются.
func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("_", "-", " ", "-").Replace(s)
	s = slugDropRe.ReplaceAllString(s, "")
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

// normalizeRefs — ref работ и ключи глав приводятся к [a-z0-9-] вместе со
// ссылками на них из вех. Пустой slug или слияние двух ключей в один — не
// правим, останется ошибкой валидации.
func normalizeRefs(a *bio.Author) []Flag {
	fix := func(keys []string) (map[string]string, []string) {
		m := map[string]string{}
		seen := map[string]bool{}
		for _, k := range keys {
			seen[k] = true
		}
		var changed []string
		for _, k := range keys {
			n := slug(k)
			if n == "" || n == k || seen[n] {
				continue
			}
			seen[n] = true
			m[k] = n
			changed = append(changed, k+"→"+n)
		}
		return m, changed
	}
	var flags []Flag
	refs := make([]string, 0, len(a.Works))
	for _, w := range a.Works {
		refs = append(refs, w.Ref)
	}
	refMap, changed := fix(refs)
	if len(changed) > 0 {
		for i := range a.Works {
			if n, ok := refMap[a.Works[i].Ref]; ok {
				a.Works[i].Ref = n
			}
		}
		for i := range a.Events {
			for j := range a.Events[i].Works {
				if n, ok := refMap[a.Events[i].Works[j].Ref]; ok {
					a.Events[i].Works[j].Ref = n
				}
			}
		}
		flags = append(flags, Flag{Key: "works", Msg: "ref приведены к [a-z0-9-]: " + strings.Join(changed, ", ")})
	}
	keys := make([]string, 0, len(a.Chapters))
	for _, c := range a.Chapters {
		keys = append(keys, c.Key)
	}
	keyMap, changed := fix(keys)
	if len(changed) > 0 {
		for i := range a.Chapters {
			if n, ok := keyMap[a.Chapters[i].Key]; ok {
				a.Chapters[i].Key = n
			}
		}
		for i := range a.Events {
			if n, ok := keyMap[a.Events[i].Chapter]; ok {
				a.Events[i].Chapter = n
			}
		}
		flags = append(flags, Flag{Key: "chapters", Msg: "ключи приведены к [a-z0-9-]: " + strings.Join(changed, ", ")})
	}
	return flags
}

// trimTexts — long длиннее лимита усекается по границе предложения (если
// остаётся хотя бы половина лимита), иначе по слову с многоточием; note
// длиннее лимита снимается (линк остаётся). short не правим — это формулировка.
func trimTexts(a *bio.Author) []Flag {
	var flags []Flag
	for i := range a.Events {
		e := &a.Events[i]
		if n := len([]rune(e.Long)); n > bio.MaxLongRunes {
			e.Long = cutRunes(e.Long, bio.MaxLongRunes)
			flags = append(flags, Flag{Key: e.Key, Msg: fmt.Sprintf("long усечён: %d → %d символов", n, len([]rune(e.Long)))})
		}
		for j := range e.Works {
			if n := len([]rune(e.Works[j].Note)); n > bio.MaxNoteRunes {
				e.Works[j].Note = ""
				flags = append(flags, Flag{Key: e.Key, Msg: fmt.Sprintf("note к %s снята: %d символов > %d", e.Works[j].Ref, n, bio.MaxNoteRunes)})
			}
		}
	}
	return flags
}

func cutRunes(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	for i := limit - 1; i >= limit/2; i-- {
		if strings.ContainsRune(".!?»", r[i]) && (i+1 == len(r) || r[i+1] == ' ') {
			return strings.TrimSpace(string(r[:i+1]))
		}
	}
	cut := limit - 1
	for i := cut; i > limit/2; i-- {
		if r[i] == ' ' {
			cut = i
			break
		}
	}
	return strings.TrimRight(string(r[:cut]), " ,;:—-") + "…"
}

// demoteExtraMajors — major сверх лимита (рождение и смерть не в счёт)
// понижаются до minor: сначала поздние вехи без линков на работы, потом поздние
// с линками.
func demoteExtraMajors(a *bio.Author) []Flag {
	var majors []int
	for i, e := range a.Events {
		if e.Tier == bio.TierMajor && e.Type != bio.EventBirth && e.Type != bio.EventDeath {
			majors = append(majors, i)
		}
	}
	extra := len(majors) - bio.MaxMajorEvents
	if extra <= 0 {
		return nil
	}
	var flags []Flag
	demote := func(linked bool) {
		for k := len(majors) - 1; k >= 0 && extra > 0; k-- {
			e := &a.Events[majors[k]]
			if e.Tier != bio.TierMajor || (len(e.Works) > 0) != linked {
				continue
			}
			e.Tier = bio.TierMinor
			extra--
			flags = append(flags, Flag{Key: e.Key, Msg: fmt.Sprintf("понижена до minor: major-вех больше %d", bio.MaxMajorEvents)})
		}
	}
	demote(false)
	demote(true)
	return flags
}

// capEvents — вех сверх лимита: выбрасываются поздние minor без линков на работы;
// если таких не хватает, остальное остаётся ошибкой файла.
func capEvents(a *bio.Author) []Flag {
	var flags []Flag
	for len(a.Events) > bio.MaxEvents {
		idx := -1
		for i := len(a.Events) - 1; i >= 0; i-- {
			e := a.Events[i]
			if e.Tier == bio.TierMinor && len(e.Works) == 0 && e.Type != bio.EventBirth && e.Type != bio.EventDeath {
				idx = i
				break
			}
		}
		if idx < 0 {
			break
		}
		flags = append(flags, Flag{Key: a.Events[idx].Key, Msg: fmt.Sprintf("вех больше %d — поздняя minor без линков выброшена", bio.MaxEvents), Dropped: true})
		a.Events = append(a.Events[:idx:idx], a.Events[idx+1:]...)
	}
	return flags
}

// fixWritten — период написания за пределами жизни: модель берёт годы посмертных
// изданий. Начало после смерти — период снимается (работа остаётся для линков),
// конец после смерти — обрезается годом смерти; то же симметрично для рождения.
func fixWritten(a *bio.Author) []Flag {
	born, died := bio.YearOf(a.Born), bio.YearOf(a.Died)
	var flags []Flag
	for i := range a.Works {
		w := &a.Works[i]
		if w.Written == nil {
			continue
		}
		was := *w.Written
		switch {
		case died > 0 && w.Written.From > died, born > 0 && w.Written.To < born:
			w.Written = nil
			flags = append(flags, Flag{Key: "works." + w.Ref, Msg: fmt.Sprintf("written %d–%d вне жизни (%d–%d) — снят, издание посмертное или ошибка", was.From, was.To, born, died)})
			continue
		case died > 0 && w.Written.To > died:
			w.Written.To = died
		case born > 0 && w.Written.From < born:
			w.Written.From = born
		}
		if *w.Written != was {
			flags = append(flags, Flag{Key: "works." + w.Ref, Msg: fmt.Sprintf("written %d–%d обрезан годами жизни: %d–%d", was.From, was.To, w.Written.From, w.Written.To)})
		}
	}
	return flags
}
