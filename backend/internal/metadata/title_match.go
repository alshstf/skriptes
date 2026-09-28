package metadata

import (
	"regexp"
	"strings"
	"unicode"
)

// workTitleKey — ключ сравнения названий: нижний регистр, ё→е, только буквы,
// цифры и одиночные пробелы.
func workTitleKey(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r == 'ё':
			r = 'е'
		case unicode.IsLetter(r) || unicode.IsDigit(r):
		default:
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

// titlesMatch — названия одного произведения: совпадают по ключу или одно —
// начало другого с подзаголовком («Мастер и Маргарита. Роман», «Dune: A Novel»).
// Пустое не совпадает ни с чем.
func titlesMatch(a, b string) bool {
	ka, kb := workTitleKey(a), workTitleKey(b)
	if ka == "" || kb == "" {
		return false
	}
	if ka == kb {
		return true
	}
	if len(ka) > len(kb) {
		ka, kb = kb, ka
	}
	return strings.HasPrefix(kb, ka+" ") && strings.Count(ka, " ") >= 1
}

// volumeRe — номер тома/книги/части в названии: «Свечка. Том 2», «Книга 3»,
// «Часть II», «Vol. 1».
var volumeRe = regexp.MustCompile(`(?i)(?:^|[^\p{L}])(том|книга|часть|volume|vol|book|part)\s*\.?\s*(\d+|[ivxlc]+)(?:[^\p{L}\d]|$)`)

// volumeNumber — «том 2» из названия (вид + номер в нижнем регистре), "" если нет.
func volumeNumber(title string) string {
	m := volumeRe.FindStringSubmatch(title)
	if m == nil {
		return ""
	}
	return strings.ToLower(m[1]) + " " + strings.ToLower(m[2])
}

// isStubSrcTitle — «название оригинала», которое на деле заглушка: chitanka
// пишет «(no data for original title)» в сотни fb2 (прод 2026-09: 836 книг,
// и byTrans склеивал по ней 110 разных болгарских текстов в одну работу, #279);
// строка без букв и цифр («???») — тоже.
func isStubSrcTitle(s string) bool {
	t := strings.ToLower(strings.TrimSpace(s))
	if t == "(no data for original title)" || t == "no data for original title" {
		return true
	}
	return t != "" && workTitleKey(t) == ""
}
