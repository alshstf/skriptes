package catalog

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/skriptes/skriptes/backend/internal/books"
)

// seriesOrderOf — ключ сортировки списка книг по series_order; nil → в конец.
func seriesOrderOf(b books.ListItem) int {
	if b.SeriesOrder != nil {
		return *b.SeriesOrder
	}
	return int(^uint(0) >> 1) // max int
}

// seriesSortItem — вход каскада сортировки книг внутри ОДНОЙ серии.
// Поля-указатели nil = значение отсутствует в БД.
type seriesSortItem struct {
	bookID      int64
	serNo       *int
	writtenYear *int
	editionYear *int
	title       string // сырое название — для эвристики порядка
	normTitle   string // normalized_title — финальный стабильный тайбрейк
	dateAdded   time.Time
}

// assignSeriesOrder сортирует книги ОДНОЙ серии и возвращает map[bookID]rank
// (0-based). Две стратегии:
//
//   - Есть хотя бы один ser_no → ser_no-КАРКАС (assignBySerNoBackbone):
//     нумерованные книги встают на свой номер, БЕЗ-номерные ВСТАВЛЯЮТСЯ по году
//     (written_year ∥ edition_year) в подходящий пропуск, а не уходят в конец.
//     Это чинит частый случай, когда у серии ser_no проставлен у большинства, но
//     не у всех (раньше all-or-nothing отбрасывал ser_no целиком и сортировка
//     валилась на date_added — порядок добавления в библиотеку).
//
//   - Ни у кого нет ser_no → каскад (assignByCascade): written_year → edition_year
//     → эвристика номера из названия → date_added (all-or-nothing на уровень).
//
// Работает на ПРЕДСТАВИТЕЛЯХ работ (издания уже схлопнуты по work_id вызывающим).
// Финальный стабильный тайбрейк везде — normalized_title.
func assignSeriesOrder(items []seriesSortItem) map[int64]int {
	if len(items) == 0 {
		return map[int64]int{}
	}
	for _, it := range items {
		if it.serNo != nil {
			return assignBySerNoBackbone(items)
		}
	}
	return assignByCascade(items)
}

// secOf — вторичный непрерывный сигнал порядка: год написания, иначе год издания.
func secOf(it seriesSortItem) (int, bool) {
	if it.writtenYear != nil {
		return *it.writtenYear, true
	}
	if it.editionYear != nil {
		return *it.editionYear, true
	}
	return 0, false
}

// assignBySerNoBackbone — ser_no как каркас + вставка без-номерных книг по году.
//
// Ключ сортировки (float):
//   - книга с ser_no → key = ser_no;
//   - без ser_no, но с годом и есть датированные нумерованные «якоря» → key =
//     (max ser_no среди якорей, чей год ≤ год книги) + 0.5; то есть книга садится
//     СРАЗУ ПОСЛЕ последнего номера, который её не позже → попадает в пропуск.
//     Год раньше всех датированных якорей → перед самым ранним из них (−0.5);
//   - без года (или вставлять не по чему) → в хвост (maxSer+1).
//
// Тайбрейк на равном ключе: датированные раньше бездатных, затем год, date_added,
// normalized_title (два «orphan» в одном пропуске упорядочатся по году/названию).
func assignBySerNoBackbone(items []seriesSortItem) map[int64]int {
	type anchor struct{ serNo, sec int }
	var dated []anchor
	maxSer := 0
	minDatedSer, haveMinDated := 0, false
	for _, it := range items {
		if it.serNo == nil {
			continue
		}
		if *it.serNo > maxSer {
			maxSer = *it.serNo
		}
		if sec, ok := secOf(it); ok {
			dated = append(dated, anchor{serNo: *it.serNo, sec: sec})
			if !haveMinDated || *it.serNo < minDatedSer {
				minDatedSer, haveMinDated = *it.serNo, true
			}
		}
	}

	type keyed struct {
		bookID    int64
		key       float64
		sec       int
		hasSec    bool
		dateAdded time.Time
		normTitle string
	}
	keys := make([]keyed, 0, len(items))
	for _, it := range items {
		sec, hasSec := secOf(it)
		k := keyed{bookID: it.bookID, sec: sec, hasSec: hasSec, dateAdded: it.dateAdded, normTitle: it.normTitle}
		switch {
		case it.serNo != nil:
			k.key = float64(*it.serNo)
		case hasSec && len(dated) > 0:
			best := -1
			for _, a := range dated {
				if a.sec <= sec && a.serNo > best {
					best = a.serNo
				}
			}
			if best >= 0 {
				k.key = float64(best) + 0.5 // в пропуск сразу за последним «не позже» номером
			} else {
				k.key = float64(minDatedSer) - 0.5 // раньше самого раннего датированного
			}
		default:
			k.key = float64(maxSer) + 1 // нечем вставлять → хвост
		}
		keys = append(keys, k)
	}

	sort.SliceStable(keys, func(i, j int) bool {
		if keys[i].key != keys[j].key {
			return keys[i].key < keys[j].key
		}
		if keys[i].hasSec != keys[j].hasSec {
			return keys[i].hasSec // датированные раньше бездатных
		}
		if keys[i].hasSec && keys[i].sec != keys[j].sec {
			return keys[i].sec < keys[j].sec
		}
		if !keys[i].dateAdded.Equal(keys[j].dateAdded) {
			return keys[i].dateAdded.Before(keys[j].dateAdded)
		}
		return keys[i].normTitle < keys[j].normTitle
	})
	out := make(map[int64]int, len(keys))
	for rank, k := range keys {
		out[k.bookID] = rank
	}
	return out
}

// assignByCascade — серия БЕЗ ser_no: порядок по каскаду pickSeriesLevel
// (written_year → edition_year → эвристика названия → date_added). Стабильный
// тайбрейк — normalized_title.
func assignByCascade(items []seriesSortItem) map[int64]int {
	keyFn := pickSeriesLevel(items)
	sorted := make([]seriesSortItem, len(items))
	copy(sorted, items)
	sort.SliceStable(sorted, func(i, j int) bool {
		ki, kj := keyFn(sorted[i]), keyFn(sorted[j])
		if ki != kj {
			return ki < kj
		}
		return sorted[i].normTitle < sorted[j].normTitle
	})
	out := make(map[int64]int, len(sorted))
	for rank, it := range sorted {
		out[it.bookID] = rank
	}
	return out
}

// pickSeriesLevel выбирает активный уровень каскада для серии и возвращает
// функцию ключа сортировки (int, по возрастанию) на этом уровне.
func pickSeriesLevel(items []seriesSortItem) func(seriesSortItem) int {
	if len(items) == 0 {
		return func(seriesSortItem) int { return 0 }
	}
	// 1. ser_no у всех.
	if allHave(items, func(it seriesSortItem) bool { return it.serNo != nil }) {
		return func(it seriesSortItem) int { return *it.serNo }
	}
	// 2. written_year у всех.
	if allHave(items, func(it seriesSortItem) bool { return it.writtenYear != nil }) {
		return func(it seriesSortItem) int { return *it.writtenYear }
	}
	// 3. edition_year у всех.
	if allHave(items, func(it seriesSortItem) bool { return it.editionYear != nil }) {
		return func(it seriesSortItem) int { return *it.editionYear }
	}
	// 4. эвристика названия — только если уверенно у всех.
	if ord := titleOrdinals(items); ord != nil {
		return func(it seriesSortItem) int { return ord[it.bookID] }
	}
	// 5. date_added (последний резерв). Используем Unix-секунды; нулевое время
	//    (NULL date_added) уедет в начало — приемлемо как крайний фолбэк.
	return func(it seriesSortItem) int { return int(it.dateAdded.Unix()) }
}

func allHave(items []seriesSortItem, pred func(seriesSortItem) bool) bool {
	for _, it := range items {
		if !pred(it) {
			return false
		}
	}
	return true
}

// titleOrdinals возвращает map[bookID]ordinal, если эвристика уверенно дала
// порядок для КАЖДОЙ книги серии; иначе nil (уровень пропускается).
func titleOrdinals(items []seriesSortItem) map[int64]int {
	out := make(map[int64]int, len(items))
	for _, it := range items {
		n, ok := parseTitleOrdinal(it.title)
		if !ok {
			return nil
		}
		out[it.bookID] = n
	}
	return out
}

// ── эвристика порядка из названия ──────────────────────────────────────────

// ordinalKeyword матчит «книга/том/часть/кн./ч.» + следующий токен (число,
// кириллическое порядковое слово или римскую цифру). Требуем ключевое слово,
// чтобы «Дюна 2» не ловилось как порядок.
var ordinalKeyword = regexp.MustCompile(`(?:книга|том|часть|кн\.|ч\.)\s+([\p{L}\d]+)`)

// cyrillicOrdinals — порядковые слова → число (ключи с «е» вместо «ё»; формы
// мужского/женского/среднего рода для «том первый / книга первая / часть первое»).
var cyrillicOrdinals = map[string]int{
	"первый": 1, "первая": 1, "первое": 1,
	"второй": 2, "вторая": 2, "второе": 2,
	"третий": 3, "третья": 3, "третье": 3,
	"четвертый": 4, "четвертая": 4, "четвертое": 4,
	"пятый": 5, "пятая": 5, "пятое": 5,
	"шестой": 6, "шестая": 6, "шестое": 6,
	"седьмой": 7, "седьмая": 7, "седьмое": 7,
	"восьмой": 8, "восьмая": 8, "восьмое": 8,
	"девятый": 9, "девятая": 9, "девятое": 9,
	"десятый": 10, "десятая": 10, "десятое": 10,
	"одиннадцатый": 11, "одиннадцатая": 11,
	"двенадцатый": 12, "двенадцатая": 12,
}

// parseTitleOrdinal извлекает порядковый номер тома из названия. Возвращает
// (n, true) только при УВЕРЕННОМ совпадении: ровно одно непротиворечивое
// значение. Понимает арабские числа, кириллические порядковые слова и римские
// цифры после ключевого слова. Неуверенно/неоднозначно → (0, false).
func parseTitleOrdinal(title string) (int, bool) {
	s := strings.ToLower(strings.ReplaceAll(title, "ё", "е"))
	matches := ordinalKeyword.FindAllStringSubmatch(s, -1)
	if len(matches) == 0 {
		return 0, false
	}
	found := -1
	for _, m := range matches {
		n, ok := resolveOrdinalToken(m[1])
		if !ok {
			continue
		}
		if found == -1 {
			found = n
		} else if found != n {
			return 0, false // несколько разных значений → неоднозначно
		}
	}
	if found <= 0 {
		return 0, false
	}
	return found, true
}

func resolveOrdinalToken(tok string) (int, bool) {
	if n, err := strconv.Atoi(tok); err == nil {
		if n >= 1 && n <= 999 {
			return n, true
		}
		return 0, false
	}
	if n, ok := cyrillicOrdinals[tok]; ok {
		return n, true
	}
	if n, ok := parseRoman(tok); ok {
		return n, true
	}
	return 0, false
}

// parseRoman — маленькие римские цифры (для «том II»). Только латинские i/v/x/l/c/d/m.
func parseRoman(s string) (int, bool) {
	vals := map[rune]int{'i': 1, 'v': 5, 'x': 10, 'l': 50, 'c': 100, 'd': 500, 'm': 1000}
	total, prev := 0, 0
	for _, r := range reverse(s) {
		v, ok := vals[r]
		if !ok {
			return 0, false
		}
		if v < prev {
			total -= v
		} else {
			total += v
			prev = v
		}
	}
	if total < 1 || total > 999 {
		return 0, false
	}
	return total, true
}

func reverse(s string) []rune {
	rs := []rune(s)
	for i, j := 0, len(rs)-1; i < j; i, j = i+1, j-1 {
		rs[i], rs[j] = rs[j], rs[i]
	}
	return rs
}
