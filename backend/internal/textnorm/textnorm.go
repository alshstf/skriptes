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
