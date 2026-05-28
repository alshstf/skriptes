package catalog

import (
	"context"
	"fmt"
	"strings"
)

// LanguageEntry — строка в списке языков коллекции (для панели фильтров и
// для разделов «Контент» в админке/профиле, где языки можно скрывать).
type LanguageEntry struct {
	Code      string `json:"code"`    // ISO-639 код из books.lang (напр. "ru")
	Display   string `json:"display"` // человекочитаемое имя ("Русский"), fallback — код
	BookCount int    `json:"book_count"`
}

// langNames — отображаемые имена для частых в fb2-коллекциях языков.
// Не претендует на полноту ISO-639: неизвестные коды показываем как есть
// (в верхнем регистре). Имена — по-русски, т.к. UI русскоязычный.
var langNames = map[string]string{
	"ru": "Русский",
	"en": "Английский",
	"uk": "Украинский",
	"be": "Белорусский",
	"de": "Немецкий",
	"fr": "Французский",
	"es": "Испанский",
	"it": "Итальянский",
	"pl": "Польский",
	"bg": "Болгарский",
	"cs": "Чешский",
	"sr": "Сербский",
	"pt": "Португальский",
	"nl": "Нидерландский",
	"sv": "Шведский",
	"fi": "Финский",
	"no": "Норвежский",
	"da": "Датский",
	"el": "Греческий",
	"tr": "Турецкий",
	"ja": "Японский",
	"zh": "Китайский",
	"ka": "Грузинский",
	"hy": "Армянский",
	"kk": "Казахский",
	"lv": "Латышский",
	"lt": "Литовский",
	"et": "Эстонский",
	"he": "Иврит",
	"la": "Латинский",
}

// LanguageDisplay возвращает отображаемое имя языка по коду. Экспортируется,
// чтобы тот же маппинг переиспользовали другие места (фасеты/чипы).
func LanguageDisplay(code string) string {
	if name, ok := langNames[strings.ToLower(code)]; ok {
		return name
	}
	return strings.ToUpper(code)
}

// ListLanguages — все языки, встречающиеся в живых (не удалённых) книгах,
// с числом книг. Отсортированы по убыванию количества (популярные сверху),
// tiebreaker — код. Полный список (без фильтрации по скрытым) — он нужен
// разделам «Контент», где скрытые языки как раз нужно показать, чтобы их
// можно было включить обратно.
func (s *Service) ListLanguages(ctx context.Context) ([]LanguageEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT lang, count(*)::int
		FROM books
		WHERE deleted = false AND lang IS NOT NULL AND lang <> ''
		GROUP BY lang
		ORDER BY count(*) DESC, lang
	`)
	if err != nil {
		return nil, fmt.Errorf("list languages: %w", err)
	}
	defer rows.Close()

	out := make([]LanguageEntry, 0, 16)
	for rows.Next() {
		var e LanguageEntry
		if err := rows.Scan(&e.Code, &e.BookCount); err != nil {
			return nil, fmt.Errorf("scan language: %w", err)
		}
		e.Display = LanguageDisplay(e.Code)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
