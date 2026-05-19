package catalog

import (
	"context"
	"fmt"
)

// Перечисляющие методы для OPDS-каталога. Detail-методы (GetAuthor /
// GetSeries) собирают тяжёлый граф для одной записи; здесь — лёгкие
// постраничные списки для навигации.

// AuthorEntry — строка в постраничном списке авторов.
type AuthorEntry struct {
	ID        int64  `json:"id"`
	FullName  string `json:"full_name"`
	BookCount int    `json:"book_count"`
}

// SeriesEntry — строка в постраничном списке серий.
type SeriesEntry struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	AuthorName string `json:"author_name,omitempty"` // если у серии один автор в БД
	BookCount  int    `json:"book_count"`
}

// GenreEntry — узел дерева жанров.
type GenreEntry struct {
	ID        int64  `json:"id"`
	Code      string `json:"code"`    // FB2-код, например "sf_action"
	Display   string `json:"display"` // RU-имя если есть, иначе EN, иначе code
	BookCount int    `json:"book_count"`
}

// ListAuthors — постраничный список авторов, отсортированный по
// фамилии (как для алфавитной навигации в OPDS). Считает книги через
// один JOIN+GROUP BY, чтобы избежать N+1 при page-size в сотни.
//
// limit ограничен сверху 500 (защита от случайного "GET /opds/authors?n=999999"),
// дефолт 50. offset — сколько пропустить.
func (s *Service) ListAuthors(ctx context.Context, limit, offset int) ([]AuthorEntry, int, error) {
	limit, offset = sanitizePaging(limit, offset)

	var total int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM authors`,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count authors: %w", err)
	}

	// COUNT(b.id) а не COUNT(ba.book_id) — иначе фильтр по deleted в
	// LEFT JOIN не применяется к счётчику: ba-строка остаётся, b пустой,
	// но COUNT(ba.*) её всё равно считает. b.id NULL при deleted'е → не идёт в счёт.
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.last_name, a.first_name, a.middle_name, COUNT(b.id)::int
		FROM authors a
		LEFT JOIN book_authors ba ON ba.author_id = a.id
		LEFT JOIN books b ON b.id = ba.book_id AND b.deleted = false
		GROUP BY a.id
		ORDER BY a.last_name, a.first_name, a.middle_name, a.id
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list authors: %w", err)
	}
	defer rows.Close()

	out := make([]AuthorEntry, 0, limit)
	for rows.Next() {
		var (
			a                   AuthorEntry
			last, first, middle string
		)
		if err := rows.Scan(&a.ID, &last, &first, &middle, &a.BookCount); err != nil {
			return nil, 0, fmt.Errorf("scan author: %w", err)
		}
		a.FullName = fullName(last, first, middle)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// ListSeries — постраничный список серий, отсортированный по title.
// AuthorName заполняется только если у серии есть series.author_id
// (FK на authors). Это удобство для OPDS-клиентов: можно показать
// "Серия / Автор" в одной строке.
func (s *Service) ListSeries(ctx context.Context, limit, offset int) ([]SeriesEntry, int, error) {
	limit, offset = sanitizePaging(limit, offset)

	var total int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM series`,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count series: %w", err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT s.id, s.title,
		       COALESCE(a.last_name, '') AS la,
		       COALESCE(a.first_name, '') AS fi,
		       COALESCE(a.middle_name, '') AS mi,
		       (SELECT COUNT(*) FROM books b
		        WHERE b.series_id = s.id AND b.deleted = false)::int AS book_count
		FROM series s
		LEFT JOIN authors a ON a.id = s.author_id
		ORDER BY s.title, s.id
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list series: %w", err)
	}
	defer rows.Close()

	out := make([]SeriesEntry, 0, limit)
	for rows.Next() {
		var (
			e          SeriesEntry
			la, fi, mi string
		)
		if err := rows.Scan(&e.ID, &e.Title, &la, &fi, &mi, &e.BookCount); err != nil {
			return nil, 0, fmt.Errorf("scan series: %w", err)
		}
		if la != "" {
			e.AuthorName = fullName(la, fi, mi)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, nil
}

// ListGenres — все жанры, отсортированные по display-имени. В отличие
// от авторов/серий жанров в БД ~250 (закрытый FB2-словарь), пагинация
// не нужна — возвращаем весь список.
//
// Display — RU-имя если есть, EN если нет, code как последний fallback.
// Это парирует случаи "новый FB2-жанр без локализации в нашем словаре".
func (s *Service) ListGenres(ctx context.Context) ([]GenreEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT g.id, g.fb2_code,
		       COALESCE(NULLIF(g.name_ru, ''), NULLIF(g.name_en, ''), g.fb2_code) AS display,
		       (SELECT COUNT(*) FROM book_genres bg
		         JOIN books b ON b.id = bg.book_id
		         WHERE bg.genre_id = g.id AND b.deleted = false)::int AS book_count
		FROM genres g
		ORDER BY display, g.id
	`)
	if err != nil {
		return nil, fmt.Errorf("list genres: %w", err)
	}
	defer rows.Close()

	out := make([]GenreEntry, 0, 256)
	for rows.Next() {
		var g GenreEntry
		if err := rows.Scan(&g.ID, &g.Code, &g.Display, &g.BookCount); err != nil {
			return nil, fmt.Errorf("scan genre: %w", err)
		}
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// sanitizePaging — единая защита от мусорных значений: limit ∈ [1, 500],
// offset ≥ 0. 500 — компромисс: KOReader без проблем рендерит 200-300
// entries в одной OPDS-странице, выше — медленно скроллить.
func sanitizePaging(limit, offset int) (int, int) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
