package importer

import (
	"strings"
	"unicode"

	"github.com/skriptes/skriptes/backend/internal/inpx"
)

// normalize приводит строку к виду для сравнения / dedup:
// lowercase, trim, последовательности whitespace схлопываются в один пробел.
// Не агрессивная транслитерация / удаление знаков — этого пока достаточно
// для дедупликации по нормализованному ключу.
func normalize(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	prevSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !prevSpace {
				b.WriteByte(' ')
				prevSpace = true
			}
			continue
		}
		b.WriteRune(r)
		prevSpace = false
	}
	return b.String()
}

// normalizedAuthorName возвращает ключ дедупликации автора:
// "lastname firstname middlename" в нижнем регистре.
// Пустые части пропускаются — два автора с разной полнотой ФИО (одинаковая
// фамилия + инициал) дадут разные ключи, что корректно.
func normalizedAuthorName(a inpx.Author) string {
	parts := make([]string, 0, 3)
	if a.LastName != "" {
		parts = append(parts, a.LastName)
	}
	if a.FirstName != "" {
		parts = append(parts, a.FirstName)
	}
	if a.MiddleName != "" {
		parts = append(parts, a.MiddleName)
	}
	return normalize(strings.Join(parts, " "))
}

// fullAuthorName — display-форма для UI и Meili (без нормализации).
func fullAuthorName(a inpx.Author) string {
	parts := make([]string, 0, 3)
	if a.LastName != "" {
		parts = append(parts, a.LastName)
	}
	if a.FirstName != "" {
		parts = append(parts, a.FirstName)
	}
	if a.MiddleName != "" {
		parts = append(parts, a.MiddleName)
	}
	return strings.Join(parts, " ")
}
