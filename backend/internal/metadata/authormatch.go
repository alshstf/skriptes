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
//
// Фамилия и имя из нескольких частей («Ле Гуин», «Гарсиа Маркес», «Жан-Кристоф»)
// сверяются по частям: каждая значимая часть должна быть среди слов кандидата,
// частицы (де, ле, ван, фон…) необязательны. Раньше часть склеивалась в один
// токен («leguin») и такие авторы не совпадали никогда (#348).
func authorNameMatches(q AuthorQuery, candidate string) bool {
	last := nameTokens(q.LastName)
	if len(last) == 0 {
		return true // нет даже фамилии — нечем проверять
	}
	cand := nameTokens(candidate)
	for _, p := range requiredParts(last) {
		if !anyTokenMatches(cand, p.lat, p.cyr) {
			return false // фамилии нет в кандидате — точно не он
		}
	}
	// Имя — первое слово поля целиком (дефисное «Жан-Кристоф» — обе части); дальше
	// в поле бывают второе имя и инициал («Урсула К», «Лайон Спрэг») — их, как и
	// отчество, не сверяем.
	firstWord := ""
	if w := strings.Fields(q.FirstName); len(w) > 0 {
		firstWord = w[0]
	}
	first := nameTokens(firstWord)
	if len(first) == 0 {
		return true // имени нет — гейтим только по фамилии
	}
	for _, p := range requiredParts(first) {
		if !anyTokenMatches(cand, p.lat, p.cyr) && !initialMatches(cand, p.lat) && !anyTokenOneGap(cand, p.lat) {
			return false
		}
	}
	return true
}

// nameParticles — частицы составных фамилий (латиницей после translitName): в
// источнике их пишут по-разному или опускают («Вогт, Альфред ван», «Гуин»).
var nameParticles = map[string]bool{
	"de": true, "di": true, "da": true, "del": true, "della": true, "dello": true, "du": true, "dyu": true,
	"le": true, "la": true, "van": true, "von": true, "fon": true, "der": true, "den": true, "ter": true,
	"ten": true, "ibn": true, "ben": true, "bin": true, "al": true, "el": true, "o": true,
}

// requiredParts — части имени, которые обязаны совпасть: все, кроме частиц (если
// имя из одних частиц — все).
func requiredParts(parts []nameToken) []nameToken {
	if len(parts) == 1 {
		return parts
	}
	out := make([]nameToken, 0, len(parts))
	for _, p := range parts {
		if !nameParticles[p.lat] {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return parts
	}
	return out
}

// anyTokenOneGap — имя отличается одной вставленной или пропущенной буквой
// (Фритц/Фриц, Наталия/Наталья) — варианты передачи ИМЕНИ, в том числе в одном
// алфавите. Замена буквы (Мария/Марина) — нет. Только для имени: у фамилий так
// совпали бы разные люди («Юрмин»/«Юрин»).
func anyTokenOneGap(tokens []nameToken, target string) bool {
	if len(target) < 4 {
		return false
	}
	for _, t := range tokens {
		a, b := t.lat, target
		if len(a) < len(b) {
			a, b = b, a
		}
		if len(a) != len(b)+1 {
			continue
		}
		for i := 0; i < len(a); i++ {
			if a[:i]+a[i+1:] == b {
				return true
			}
		}
	}
	return false
}

// nameToken — токен имени кандидата латиницей и признак, что он был кириллицей.
type nameToken struct {
	lat string
	cyr bool
}

func hasCyrillic(s string) bool {
	for _, r := range s {
		if _, ok := cyrToLat[r]; ok {
			return true
		}
		if r >= 'А' && r <= 'я' {
			return true
		}
	}
	return false
}

// nameTokens — разбивает имя-кандидат на транслитерированные латиницей токены
// (по пробелам/запятым/дефисам/точкам), пустые отбрасывает.
func nameTokens(s string) []nameToken {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == ',' || r == '.' || r == '-' || r == '\t' || r == '\u00a0'
	})
	out := make([]nameToken, 0, len(fields))
	for _, f := range fields {
		if t := translitName(f); t != "" {
			out = append(out, nameToken{lat: t, cyr: hasCyrillic(f)})
		}
	}
	return out
}

// anyTokenMatches — есть ли среди токенов совпадение с target: точное либо с
// расстоянием Левенштейна ≤1 для токенов длиной ≥3 — но только между разными
// алфавитами, где расхождение даёт транслитерация (Лиза→liza ≈ Lisa→lisa,
// Лев→lev ≈ Leo→leo). В одном алфавите — только точное: «Фирсов» ≈ «Фурсов»,
// «Мария» ≈ «Марина» давали чужие био (#280). Совсем короткие (≤2) — тоже
// только точно, чтобы не плодить совпадения на инициалах/частицах.
func anyTokenMatches(tokens []nameToken, target string, targetCyr bool) bool {
	for _, t := range tokens {
		if t.lat == target {
			return true
		}
		if t.cyr != targetCyr && len(target) >= 3 && len(t.lat) >= 3 && levenshtein(t.lat, target) <= 1 {
			return true
		}
		// В одном алфавите — только разные передачи одного иностранного имени:
		// удвоенные согласные («Флевеллинг» ~ «Флевелинг»), H как Г или Х
		// («Херберт» ~ «Герберт»), S как З или С («Зузак» ~ «Зусак»).
		if len(target) >= 5 && transcriptionKey(t.lat) == transcriptionKey(target) {
			return true
		}
	}
	return false
}

// transcriptionKey — ключ сравнения фамилии в одном алфавите: без удвоенных
// букв, г→h и з→s (кроме «zh» — это Ж, не Ш). Гласные не трогаем: «Фирсов» и
// «Фурсов» — разные люди (#280).
func transcriptionKey(s string) string {
	b := []byte(s)
	for i, c := range b {
		switch {
		case c == 'g':
			b[i] = 'h'
		case c == 'z' && (i+1 == len(b) || b[i+1] != 'h'):
			b[i] = 's'
		}
	}
	return collapseDoubles(string(b))
}

// initialMatches — совпадение по инициалу: кандидат «Л.» (токен из одной буквы)
// против имени «Лиза», или наоборот.
func initialMatches(tokens []nameToken, target string) bool {
	if target == "" {
		return false
	}
	for _, t := range tokens {
		if len(t.lat) == 1 && t.lat[0] == target[0] {
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

// collapseDoubles — подряд идущие одинаковые буквы сводятся к одной.
func collapseDoubles(s string) string {
	var b strings.Builder
	var prev rune
	for i, r := range s {
		if i > 0 && r == prev {
			continue
		}
		b.WriteRune(r)
		prev = r
	}
	return b.String()
}

// mentionsAuthor — в тексте статьи есть и фамилия, и имя автора (без различия
// регистра, ё/е и знаков ударения). Нужно, когда редирект ведёт на статью под
// другим именем: псевдоним «Акунин, Борис» для «Чхартишвили Григорий» — это
// тот же человек, если статья называет настоящее имя; «Флинт» → «Флит» — нет.
func mentionsAuthor(text string, q AuthorQuery) bool {
	norm := func(s string) string {
		var b strings.Builder
		for _, r := range strings.ToLower(s) {
			switch r {
			case '\u0301', '\u0300':
				continue
			case 'ё':
				r = 'е'
			}
			b.WriteRune(r)
		}
		return b.String()
	}
	t := norm(text)
	last := norm(strings.TrimSpace(q.LastName))
	if last == "" || !strings.Contains(t, last) {
		return false
	}
	first := norm(strings.TrimSpace(q.FirstName))
	return first == "" || strings.Contains(t, first)
}

// looseNameMatches — имя кандидата совпадает с автором лишь нестрого: каждая
// значимая часть фамилии и имени равна по «скелету» (looseKey) и отличается не
// больше чем на 3 правки. Это разные передачи иностранного имени кириллицей
// (Ширли/Шерли, Гуидо/Гвидо, Килуорт/Килворт, Глик/Глейк, Дзюнпэй/Дзюмпэй,
// Муравейка/Муравейко), но так совпадают и разные люди (Фирсов/Фурсов) —
// поэтому такой кандидат принимается только с книгой автора в Wikidata (MatchLoose).
func looseNameMatches(q AuthorQuery, candidate string) bool {
	cand := nameTokens(candidate)
	parts := requiredParts(nameTokens(q.LastName))
	if w := strings.Fields(q.FirstName); len(w) > 0 {
		parts = append(parts, requiredParts(nameTokens(w[0]))...)
	}
	if len(parts) == 0 || len(cand) == 0 {
		return false
	}
	for _, p := range parts {
		pk := looseKey(p.lat)
		if len(pk) < 2 {
			return false
		}
		found := false
		for _, t := range cand {
			if t.lat == p.lat || (looseKey(t.lat) == pk && levenshtein(t.lat, p.lat) <= 3) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// looseKey — «скелет» имени латиницей: без гласных (u, v, w, y — тоже: «у» и «в»
// передают одно и то же, Гуидо/Гвидо), созвучные согласные сведены (k/c/q, s/z,
// t/d, p/b, m/n, g/h), без удвоений; первая буква-гласная — «a».
func looseKey(s string) string {
	r := strings.NewReplacer("sch", "s", "sh", "s", "ch", "c", "zh", "s", "kh", "h", "ts", "c", "ph", "f", "th", "t", "dz", "c")
	s = r.Replace(s)
	var b strings.Builder
	for i, c := range s {
		switch c {
		case 'a', 'e', 'i', 'o', 'u', 'y', 'v', 'w', 'j':
			if i == 0 {
				b.WriteByte('a')
			}
			continue
		case 'c', 'k', 'q':
			c = 'k'
		case 'z':
			c = 's'
		case 'd':
			c = 't'
		case 'b':
			c = 'p'
		case 'm':
			c = 'n'
		case 'g':
			c = 'h'
		}
		b.WriteRune(c)
	}
	return collapseDoubles(b.String())
}
