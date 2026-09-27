// Package logredact маскирует секреты в логах.
//
// Ключи TMDB и Google Books передаются в query-строке (api_key=, key=), а ошибки
// net/http (*url.Error) несут полный URL запроса — без маскирования ключ уходил в
// журнал при любой сетевой ошибке (alshstf/plans#7).
package logredact

import (
	"log/slog"
	"regexp"
	"strings"
)

// secretParam — значение секретного параметра запроса до разделителя (&, пробел,
// кавычка). `\b` не даёт задеть поля вроде work_key=.
var secretParam = regexp.MustCompile(`(?i)\b(api_key|apikey|key|access_token|token)=([^&\s"'\\]+)`)

// String заменяет значения секретных параметров в s на REDACTED.
func String(s string) string {
	if !strings.Contains(s, "=") {
		return s
	}
	return secretParam.ReplaceAllString(s, "${1}=REDACTED")
}

// ReplaceAttr — для slog.HandlerOptions: строки и ошибки (включая сообщение
// записи) проходят через String.
func ReplaceAttr(_ []string, a slog.Attr) slog.Attr {
	switch a.Value.Kind() {
	case slog.KindString:
		if s := a.Value.String(); strings.Contains(s, "=") {
			if r := String(s); r != s {
				return slog.String(a.Key, r)
			}
		}
	case slog.KindAny:
		if err, ok := a.Value.Any().(error); ok && err != nil {
			s := err.Error()
			if r := String(s); r != s {
				return slog.String(a.Key, r)
			}
		}
	}
	return a
}
