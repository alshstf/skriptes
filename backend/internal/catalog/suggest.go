package catalog

import (
	"context"
	"fmt"
	"strings"

	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/textnorm"
)

// SuggestAuthors — typeahead по авторам.
//
// Стратегия:
//   - ПОДСТРОЧНОЕ совпадение по normalized_name (CITEXT), регистр-нечувствительно:
//     "достоев" → "Достоевский …", но и "роберт" → "Гэлбрейт Роберт" (имя — не
//     первое слово). Префиксные совпадения ранжируются выше (см. ORDER BY).
//   - без различия «ё»/«е» (#278): «семенов» находит «Семёнов» — replace() с
//     обеих сторон; GIN trigram index по тому же выражению
//     (authors_name_yo_trgm, миграция 0042) ускоряет ILIKE '%…%' на
//     запросах ≥3 символов; на коротких (1-2 символа) планировщик может
//     выбрать seq scan, но при ~50-100K авторов это всё ещё <50 мс.
//   - сортировка: сначала префиксные совпадения, затем по числу книг
//     (популярные наверху), потом по нормализованному имени для стабильности.
//   - видимость (грабля №14, #289): счётчик — по живым книгам, не скрытым
//     жанрами/языками/«Скрывать сборники» (как в списке и на карточке); авторы
//     без видимых книг не подсказываются — иначе «Вазов Иван (170)» при скрытом
//     болгарском вёл на карточку с 0 книг.
func (s *Service) SuggestAuthors(ctx context.Context, query string, limit int, excludeGenres, excludeLangs []string, hideCompilations bool) ([]AuthorSuggest, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return []AuthorSuggest{}, nil
	}
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	exClause, exArgs := bookExclusionClause(3, excludeGenres, excludeLangs, hideCompilations)
	args := append([]any{textnorm.FoldYo(escapeLike(q)), limit}, exArgs...)

	rows, err := s.pool.Query(ctx, `
		SELECT id, last_name, first_name, middle_name, note, cnt FROM (
			SELECT a.id, a.last_name, a.first_name, a.middle_name, COALESCE(a.name_note, '') AS note,
			       (SELECT COUNT(DISTINCT COALESCE(b.work_id, -b.id)) FROM book_authors ba
			        JOIN books b ON b.id = ba.book_id
			        WHERE ba.author_id = a.id AND b.deleted = false`+exClause+`) AS cnt,
			       (replace(a.normalized_name::text, 'ё', 'е') ILIKE $1 || '%' ESCAPE '\') AS prefix,
			       a.normalized_name::text AS nn
			FROM authors a
			WHERE replace(a.normalized_name::text, 'ё', 'е') ILIKE '%' || $1 || '%' ESCAPE '\'
		) x
		WHERE cnt > 0
		ORDER BY prefix DESC, cnt DESC, nn
		LIMIT $2
	`, args...)
	if err != nil {
		return nil, fmt.Errorf("query author suggestions: %w", err)
	}
	defer rows.Close()
	out := make([]AuthorSuggest, 0, limit)
	for rows.Next() {
		var (
			id                  int64
			last, first, middle string
			note                string
			cnt                 int
		)
		if err := rows.Scan(&id, &last, &first, &middle, &note, &cnt); err != nil {
			return nil, err
		}
		out = append(out, AuthorSuggest{
			ID:        id,
			FullName:  fullName(last, first, middle),
			Note:      books.DisplayNote(note),
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
// Видимость — как у SuggestAuthors (#289): пустые и целиком скрытые серии не
// подсказываются.
func (s *Service) SuggestSeries(ctx context.Context, query string, limit int, excludeGenres, excludeLangs []string, hideCompilations bool) ([]SeriesSuggest, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return []SeriesSuggest{}, nil
	}
	if limit <= 0 || limit > 20 {
		limit = 5
	}
	exClause, exArgs := bookExclusionClause(3, excludeGenres, excludeLangs, hideCompilations)
	args := append([]any{textnorm.FoldYo(escapeLike(q)), limit}, exArgs...)

	rows, err := s.pool.Query(ctx, `
		SELECT id, title, author_name, cnt FROM (
			SELECT s.id, s.title,
			       COALESCE(NULLIF(TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name)), ''), '') AS author_name,
			       (SELECT COUNT(DISTINCT COALESCE(b.work_id, -b.id)) FROM books b
			        WHERE b.series_id = s.id AND b.deleted = false`+exClause+`) AS cnt,
			       (replace(s.normalized_title::text, 'ё', 'е') ILIKE $1 || '%' ESCAPE '\') AS prefix,
			       s.normalized_title::text AS nt
			FROM series s
			LEFT JOIN authors a ON a.id = s.author_id
			WHERE replace(s.normalized_title::text, 'ё', 'е') ILIKE '%' || $1 || '%' ESCAPE '\'
		) x
		WHERE cnt > 0
		ORDER BY prefix DESC, cnt DESC, nt
		LIMIT $2
	`, args...)
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

// escapeLike экранирует спецсимволы LIKE (%, _ и сам \) — запрос пользователя
// ищется как текст, а не как шаблон (#309).
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
