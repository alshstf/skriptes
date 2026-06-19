package catalog

import (
	"context"
	"fmt"
	"strings"
)

// SuggestAuthors — typeahead по авторам.
//
// Стратегия:
//   - ПОДСТРОЧНОЕ совпадение по normalized_name (CITEXT), регистр-нечувствительно:
//     "достоев" → "Достоевский …", но и "роберт" → "Гэлбрейт Роберт" (имя — не
//     первое слово). Префиксные совпадения ранжируются выше (см. ORDER BY).
//   - GIN trigram index (authors_normalized_trgm) ускоряет ILIKE '%…%' на
//     запросах ≥3 символов; на коротких (1-2 символа) планировщик может
//     выбрать seq scan, но при ~50-100K авторов это всё ещё <50 мс.
//   - сортировка: сначала префиксные совпадения, затем по числу книг
//     (популярные наверху), потом по нормализованному имени для стабильности.
func (s *Service) SuggestAuthors(ctx context.Context, query string, limit int) ([]AuthorSuggest, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return []AuthorSuggest{}, nil
	}
	if limit <= 0 || limit > 20 {
		limit = 5
	}

	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.last_name, a.first_name, a.middle_name,
		       (SELECT COUNT(*) FROM book_authors ba
		        JOIN books b ON b.id = ba.book_id
		        WHERE ba.author_id = a.id AND b.deleted = false) AS cnt
		FROM authors a
		WHERE a.normalized_name::text ILIKE '%' || $1 || '%'
		ORDER BY (a.normalized_name::text ILIKE $1 || '%') DESC,
		         cnt DESC, a.normalized_name::text
		LIMIT $2
	`, q, limit)
	if err != nil {
		return nil, fmt.Errorf("query author suggestions: %w", err)
	}
	defer rows.Close()
	out := make([]AuthorSuggest, 0, limit)
	for rows.Next() {
		var (
			id                  int64
			last, first, middle string
			cnt                 int
		)
		if err := rows.Scan(&id, &last, &first, &middle, &cnt); err != nil {
			return nil, err
		}
		out = append(out, AuthorSuggest{
			ID:        id,
			FullName:  fullName(last, first, middle),
			BookCount: cnt,
		})
	}
	return out, rows.Err()
}

// SuggestSeries — typeahead по сериям.
// Принцип тот же, что и для авторов: ПОДСТРОЧНОЕ ILIKE на normalized_title +
// trigram GIN index (так "страйк" находит «Корморан Страйк» — не первое слово);
// префиксные совпадения ранжируются выше. AuthorName заполняется LEFT JOIN
// если серия привязана к одному автору (это поле опционально в схеме).
func (s *Service) SuggestSeries(ctx context.Context, query string, limit int) ([]SeriesSuggest, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return []SeriesSuggest{}, nil
	}
	if limit <= 0 || limit > 20 {
		limit = 5
	}

	rows, err := s.pool.Query(ctx, `
		SELECT s.id, s.title,
		       COALESCE(NULLIF(TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name)), ''), '') AS author_name,
		       (SELECT COUNT(*) FROM books b
		        WHERE b.series_id = s.id AND b.deleted = false) AS cnt
		FROM series s
		LEFT JOIN authors a ON a.id = s.author_id
		WHERE s.normalized_title::text ILIKE '%' || $1 || '%'
		ORDER BY (s.normalized_title::text ILIKE $1 || '%') DESC,
		         cnt DESC, s.normalized_title::text
		LIMIT $2
	`, q, limit)
	if err != nil {
		return nil, fmt.Errorf("query series suggestions: %w", err)
	}
	defer rows.Close()
	out := make([]SeriesSuggest, 0, limit)
	for rows.Next() {
		var (
			id     int64
			title  string
			author string
			cnt    int
		)
		if err := rows.Scan(&id, &title, &author, &cnt); err != nil {
			return nil, err
		}
		out = append(out, SeriesSuggest{
			ID:         id,
			Title:      title,
			AuthorName: author,
			BookCount:  cnt,
		})
	}
	return out, rows.Err()
}
