package gen

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/skriptes/skriptes/backend/internal/bio"
)

// Старый стиль. Датасет хранит даты нового стиля (григорианский), а русская
// Википедия даёт события в России до 1 (14) февраля 1918 одиночной датой по
// юлианскому календарю («14 декабря 1825 года»). Модель помечает такую дату
// (old_style), а пересчитывает программа: арифметику дат модели путают на
// границе месяца. Эксперимент 2026-10-15: Sonnet 5.5 переписал 21 дату
// старого стиля как новый у 5 русских авторов (Fable — 1).

// oldStyleEnd — первый день нового стиля в России (по старому стилю: после
// 31 января 1918 сразу 14 февраля).
const oldStyleEnd = "1918-02-01"

// julianToGregorian — день по юлианскому календарю → ISO-день по
// григорианскому. Разница — 10 дней до 1700-02-28, 11 в XVIII веке, 12 в
// XIX, 13 в 1900–2100 (меняется после 29 февраля юлианского вековых годов).
func julianToGregorian(iso string) (string, bool) {
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return "", false
	}
	y := t.Year()
	if t.Month() <= time.February {
		y--
	}
	// Юлианское 29 февраля вековых годов (1800, 1900) time.Parse не примет —
	// таких дат в источниках практически нет, остаются как есть.
	return t.AddDate(0, 0, y/100-y/400-2).Format("2006-01-02"), true
}

// convertOldStyle — дата вехи, помеченная моделью как старый стиль,
// переводится в новый. Только точность "day" (месяц ±12 дней не уточнишь)
// и только до 1918.
func convertOldStyle(from, to string, p bio.Precision) (string, string) {
	if p != bio.PrecisionDay {
		return from, to
	}
	conv := func(s string) string {
		if s == "" || s >= oldStyleEnd {
			return s
		}
		if g, ok := julianToGregorian(s); ok {
			return g
		}
		return s
	}
	return conv(from), conv(to)
}

var (
	monthsGen = []string{"января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа",
		"сентября", "октября", "ноября", "декабря"}
	monthAlt = strings.Join(monthsGen, "|")
	// Двойная дата в преамбуле статьи: «28 августа [9 сентября] 1828»,
	// «3 [15] мая 1891», «26 мая (6 июня) 1799».
	dualLeadRe = regexp.MustCompile(`\d{1,2}(?:\s+(?:` + monthAlt + `))?\s*[\[(]\d{1,2}(?:\s+(?:` + monthAlt + `))?[\])]`)
	// Двойная дата в цитате — открывающая скобка и число: «22 декабря 1849
	// (3 января 1850 года)».
	dualQuoteRe = regexp.MustCompile(`[\[(]\s*\d{1,2}[\s\])]`)
)

// oldStyleArticle — статья ведёт даты по старому стилю: русская, с двойной
// датой в первой строке-преамбуле (рождение или смерть подданного Российской
// империи).
func oldStyleArticle(src *Sources) bool {
	for _, art := range src.Articles {
		if art.Lang != "ru" {
			continue
		}
		lead, _, _ := strings.Cut(art.Text, "\n")
		if r := []rune(lead); len(r) > 600 {
			lead = string(r[:600])
		}
		return dualLeadRe.MatchString(strings.ToLower(lead))
	}
	return false
}

// literalDay — «14 декабря» для "1825-12-14".
func literalDay(iso string) string {
	t, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return ""
	}
	return strconv.Itoa(t.Day()) + " " + monthsGen[t.Month()-1]
}

var digitBefore = regexp.MustCompile(`\d$`)

// containsLiteralDay — «14 декабря» в цитате как отдельное число (не «24 декабря»).
func containsLiteralDay(quote, lit string) bool {
	q := strings.ToLower(strings.ReplaceAll(quote, " ", " "))
	for i := 0; ; {
		j := strings.Index(q[i:], lit)
		if j < 0 {
			return false
		}
		if !digitBefore.MatchString(q[:i+j]) {
			return true
		}
		i += j + len(lit)
	}
}

// fixOldStyleCopies — страховка к пометке old_style: модель переписала
// одиночную дату русской статьи старого стиля как новый (точность "day", дата до
// 1918 и не из фактов Wikidata, в цитате-основании из ru-статьи ровно этот
// «день месяц» без двойной записи). Дата пересчитывается в новый стиль, точность понижается до месяца:
// за границей статья могла дать и новый стиль, а месяц при этом обычно верен.
// Год может сдвинуться (конец декабря) — ключ вехи переназначается.
func fixOldStyleCopies(a *bio.Author, src *Sources) []Flag {
	if !oldStyleArticle(src) {
		return nil
	}
	// Даты Wikidata — новый стиль: совпала с фактом — модель не ошиблась.
	wd := map[string]bool{}
	for _, f := range src.Facts {
		for _, d := range []string{f.Date, f.Start, f.End, f.Born, f.Died} {
			wd[d] = true
		}
	}
	var flags []Flag
	for i := range a.Events {
		e := &a.Events[i]
		if e.Type == bio.EventBirth || e.Type == bio.EventDeath || e.Date.Precision != bio.PrecisionDay ||
			e.Date.From >= oldStyleEnd || wd[e.Date.From] || e.Evidence == nil || e.Evidence.Source != "ruwiki" {
			continue
		}
		q, lit := e.Evidence.Quote, literalDay(e.Date.From)
		if lit == "" || dualQuoteRe.MatchString(q) || !containsLiteralDay(q, lit) {
			continue
		}
		was := e.Date.From
		from, to := convertOldStyle(e.Date.From, e.Date.To, bio.PrecisionDay)
		e.Date.From = from[:7]
		if len(to) >= 7 {
			e.Date.To = to[:7]
		}
		e.Date.Precision = bio.PrecisionMonth
		msg := fmt.Sprintf("дата %s — старый стиль из цитаты: новый стиль %s, точность понижена до месяца", was, from)
		if bio.YearOf(from) != bio.YearOf(was) {
			old := e.Key
			e.Key = freeKey(a, string(e.Type)+"-"+strconv.Itoa(bio.YearOf(from)))
			msg += fmt.Sprintf("; ключ %s → %s", old, e.Key)
		}
		flags = append(flags, Flag{Key: e.Key, Msg: msg})
	}
	return flags
}

// freeKey — base или base-2, base-3… — первый не занятый другой вехой.
func freeKey(a *bio.Author, base string) string {
	used := map[string]bool{}
	for _, e := range a.Events {
		used[e.Key] = true
	}
	key := base
	for n := 2; used[key]; n++ {
		key = base + "-" + strconv.Itoa(n)
	}
	return key
}

// oldStyleYearShift — дата нового стиля в первые дни января до 1918 может
// быть пересчитанным концом декабря прошлого года: в цитате-основании тогда
// стоит прошлый год.
func oldStyleYearShift(iso string) bool {
	return iso < oldStyleEnd && len(iso) == 10 && iso[5:7] == "01" && iso[8:10] <= "13"
}
