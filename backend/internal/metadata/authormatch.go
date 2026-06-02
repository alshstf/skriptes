package metadata

import "strings"

// authorNameMatches — проверка, что найденный внешним поиском кандидат
// правдоподобно совпадает с искомым автором: должны совпасть И фамилия, И имя
// (с транслитерацией Cyrillic↔Latin), а не одна фамилия.
//
// Цель — резать false positives «та же фамилия — другой человек»: запрос
// «Гарднер Лиза» (Lisa Gardner, детективы) не должен принимать кандидата
// «Иван Гарднер» (историк церковного пения). Консервативно: лучше вернуть
// «не нашли» (пустая карточка), чем подсунуть не того.
//
// Если у автора нет имени (только фамилия) — гейтить нечем, совпадения по
// фамилии достаточно (status quo для таких авторов). Так же отсекаются
// disambiguation-страницы вида «Гарднер» (нет имени → не пройдёт гейт по имени).
func authorNameMatches(q AuthorQuery, candidate string) bool {
	last := translitName(q.LastName)
	if last == "" {
		return true // нет даже фамилии — нечем проверять
	}
	cand := nameTokens(candidate)
	if !anyTokenMatches(cand, last) {
		return false // фамилии нет в кандидате — точно не он
	}
	first := translitName(q.FirstName)
	if first == "" {
		return true // имени нет — гейтим только по фамилии
	}
	return anyTokenMatches(cand, first) || initialMatches(cand, first)
}

// nameTokens — разбивает имя-кандидат на транслитерированные латиницей токены
// (по пробелам/запятым/дефисам/точкам), пустые отбрасывает.
func nameTokens(s string) []string {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == ',' || r == '.' || r == '-' || r == '\t' || r == ' '
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if t := translitName(f); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// anyTokenMatches — есть ли среди токенов совпадение с target: точное либо с
// расстоянием Левенштейна ≤1 для токенов длиной ≥3 (Лиза→liza ≈ Lisa→lisa,
// Лев→lev ≈ Leo→leo). Совсем короткие (≤2) требуют точного совпадения, чтобы
// не плодить ложные совпадения на инициалах/частицах.
func anyTokenMatches(tokens []string, target string) bool {
	for _, t := range tokens {
		if t == target {
			return true
		}
		if len(target) >= 3 && len(t) >= 3 && levenshtein(t, target) <= 1 {
			return true
		}
	}
	return false
}

// initialMatches — совпадение по инициалу: кандидат «Л.» (токен из одной буквы)
// против имени «Лиза», или наоборот.
func initialMatches(tokens []string, target string) bool {
	if target == "" {
		return false
	}
	for _, t := range tokens {
		if len(t) == 1 && t[0] == target[0] {
			return true
		}
	}
	return false
}

// translitName — нормализует имя в нижний регистр и латиницу: кириллица
// транслитерируется по распространённой схеме, латиница остаётся как есть,
// всё не-буквенное отбрасывается. Сравнение имён в едином латинском
// пространстве снимает проблему разных алфавитов.
func translitName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		if rep, ok := cyrToLat[r]; ok {
			b.WriteString(rep)
			continue
		}
		if r >= 'a' && r <= 'z' {
			b.WriteRune(r)
			continue
		}
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
		// прочее (пробелы внутри уже не приходят, диакритика, дефисы) — пропуск
	}
	return b.String()
}

// cyrToLat — практичная транслитерация русской кириллицы (нижний регистр).
// Цель — не «правильная» романизация, а стабильное сопоставление с латинскими
// формами имён, поэтому ё→e, й→i и т.п.
var cyrToLat = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e",
	'ж': "zh", 'з': "z", 'и': "i", 'й': "i", 'к': "k", 'л': "l", 'м': "m",
	'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u",
	'ф': "f", 'х': "h", 'ц': "c", 'ч': "ch", 'ш': "sh", 'щ': "sch",
	'ъ': "", 'ы': "y", 'ь': "", 'э': "e", 'ю': "yu", 'я': "ya",
	// украинские/прочие, иногда встречаются в каталоге
	'і': "i", 'ї': "i", 'є': "e", 'ґ': "g",
}

// levenshtein — расстояние редактирования (для коротких имён; O(len^2)).
func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min3(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
