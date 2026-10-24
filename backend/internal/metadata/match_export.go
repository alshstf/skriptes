package metadata

// Сверка имён и названий для внешних пакетов — импорт истории чтения
// (recsys/histimport, план recsys/04): те же гейты, что у обогащения (грабля 13,
// точность важнее полноты), только кандидат — строка из чужого файла.

// AuthorNameMatches — автор каталога (фамилия, имя) — это человек из строки
// имени candidate («Аркадий Стругацкий», «Стругацкий, Аркадий», «Arkady
// Strugatsky»): совпали фамилия и имя (инициал, устойчивая передача —
// Уильям/William — тоже). Нет имени у автора — достаточно фамилии.
func AuthorNameMatches(lastName, firstName, candidate string) bool {
	return authorNameMatches(AuthorQuery{LastName: lastName, FirstName: firstName}, candidate)
}

// AuthorSurnameMatches — в строке имени есть фамилия автора каталога (все
// значимые части, без частиц), имя не сверяется.
func AuthorSurnameMatches(lastName, candidate string) bool {
	last := nameTokens(lastName)
	if len(last) == 0 {
		return false
	}
	cand := nameTokens(candidate)
	for _, p := range requiredParts(last) {
		if !anyTokenMatches(cand, p.lat, p.cyr) {
			return false
		}
	}
	return true
}

// WorkTitleKey — ключ сравнения названий (нижний регистр, ё→е, только буквы и
// цифры через пробел).
func WorkTitleKey(s string) string { return workTitleKey(s) }

// TitlesMatch — названия одного произведения: равны по ключу или одно — начало
// другого с подзаголовком («Dune: A Novel»).
func TitlesMatch(a, b string) bool { return titlesMatch(a, b) }

// AuthorLooseMatches — имя совпало лишь нестрого: разные передачи иностранного
// или русского имени (Dostoevsky/Достоевский, Ширли/Шерли). Так совпадают и
// разные люди (Фирсов/Фурсов), поэтому — только с подтверждением книгой, как
// MatchLoose у обогащения (импорт: точное название работы).
func AuthorLooseMatches(lastName, firstName, candidate string) bool {
	return looseNameMatches(AuthorQuery{LastName: lastName, FirstName: firstName}, candidate)
}
