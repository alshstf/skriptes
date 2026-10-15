// Package textnorm — свёртка текста для поиска, общая для индексации и запроса.
package textnorm

import "strings"

var yo = strings.NewReplacer("ё", "е", "Ё", "Е")

// FoldYo заменяет «ё» на «е». Meilisearch 1.13 их не приравнивает, а пишут
// чаще через «е»: «три мушкетера» не находило «Три мушкетёра», короткие «лёд»,
// «ёж» не находились вовсе (#278). Сворачиваются обе стороны — поисковые поля
// индекса и запрос.
func FoldYo(s string) string {
	if !strings.ContainsAny(s, "ёЁ") {
		return s
	}
	return yo.Replace(s)
}

// FoldYoAll — FoldYo для каждого элемента (новый слайс).
func FoldYoAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = FoldYo(s)
	}
	return out
}

var name = strings.NewReplacer("ё", "е", "Ё", "Е", "э", "е", "Э", "Е", "й", "и", "Й", "И")

// FoldName — свёртка для сравнения ИМЁН и названий в Postgres (подсказки
// авторов и серий, поиск на /authors, #516): кроме «ё»→«е» ещё «э»→«е» и
// «й»→«и» — самые частые разночтения транслитерации («Брэдбери/Бредбери»,
// «Хайнлайн/Хаинлаин»). Зеркало SQL-выражения NameFoldSQL; Meili-поля
// сворачиваются только FoldYo (у Meili своя терпимость к опечаткам).
func FoldName(s string) string {
	if !strings.ContainsAny(s, "ёЁэЭйЙ") {
		return s
	}
	return name.Replace(s)
}

// NameFoldSQL — SQL-выражение той же свёртки над колонкой expr; trigram-индексы
// authors_name_fold_trgm / series_title_fold_trgm (миграция 0059) построены по
// нему же — выражение в запросе обязано совпадать с индексным.
func NameFoldSQL(expr string) string {
	return "translate(" + expr + "::text, 'ёэй', 'ееи')"
}
