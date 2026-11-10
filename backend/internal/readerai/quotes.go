package readerai

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/skriptes/skriptes/backend/internal/booktext"
)

// quoteRe — цитаты в ответе: «ёлочки» и “лапки”.
var quoteRe = regexp.MustCompile(`«([^«»]{12,400})»|“([^“”]{12,400})”`)

const maxQuotes = 5

// verifyQuotes оставляет из цитат ответа только те, что дословно есть в
// прочитанном тексте, и находит их раздел: по ним ридер показывает место.
// Цитата, которой в книге нет (модель пересказала своими словами или
// выдумала), ссылкой не становится.
func verifyQuotes(flat *booktext.Flat, upto int, answer string) []Quote {
	read := flat.Text[:upto]
	var out []Quote
	seen := map[string]bool{}
	for _, m := range quoteRe.FindAllStringSubmatch(answer, -1) {
		q := m[1]
		if q == "" {
			q = m[2]
		}
		if seen[q] || utf8.RuneCountInString(q) < 12 {
			continue
		}
		seen[q] = true
		// Ближе к месту чтения — вероятнее то самое место.
		start, end, ok := booktext.Locate(read, q, 1)
		// Locate умеет находить и по началу фрагмента — здесь нужна цитата целиком.
		if !ok || squash(read[start:end]) != squash(q) {
			continue
		}
		out = append(out, Quote{Text: read[start:end], Section: flat.Section(start)})
		if len(out) == maxQuotes {
			break
		}
	}
	return out
}

// squash — текст со схлопнутыми пробелами: сравнение цитаты с книгой.
func squash(s string) string { return strings.Join(strings.Fields(s), " ") }
