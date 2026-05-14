package history

import (
	"context"
	"fmt"
)

// PersonaProfile — агрегированный сигнал-портрет пользователя для
// персонализированного re-ranking'а поисковой выдачи.
//
// Структура читается за один build из всех таблиц активности (favorites
// + views + reads). Используется как read-only snapshot — поэтому
// предпочитаем мапы вместо слайсов: типичный lookup в re-ranking'е —
// "есть ли этот автор у пользователя в любимых".
//
// Веса в Activity-мапах:
//   - view: +1
//   - read: +3
//
// "Подписки" (FavoriteAuthors/Series) — отдельный мап, а не запись с
// большим весом в Activity: эти бонусы суммируются и могут хотеться
// разные коэффициенты (см. RankingWeights в internal/books).
type PersonaProfile struct {
	FavoriteAuthors map[int64]struct{}
	FavoriteSeries  map[int64]struct{}
	FavoriteBooks   map[int64]struct{}

	// BookActivity[bookID] = сумма весов view/read событий ИМЕННО для этой
	// книги. Сильный персональный сигнал: "пользователь уже открывал /
	// скачивал эту книгу — наверняка хочет её снова увидеть в поиске".
	BookActivity map[int64]float64
	// AuthorActivity[authorID] = сумма весов событий с книгами этого автора.
	AuthorActivity map[int64]float64
	// SeriesActivity[seriesID] = аналогично для серий.
	SeriesActivity map[int64]float64
	// GenreActivity[fb2_code] = аналогично для жанров.
	GenreActivity map[string]float64
}

// IsEmpty — true если у пользователя нет ни одного сигнала.
// Тогда re-ranking бессмыслен — экономим CPU и возвращаем результат
// Meili как есть.
func (p PersonaProfile) IsEmpty() bool {
	return len(p.FavoriteAuthors) == 0 &&
		len(p.FavoriteSeries) == 0 &&
		len(p.FavoriteBooks) == 0 &&
		len(p.BookActivity) == 0 &&
		len(p.AuthorActivity) == 0 &&
		len(p.SeriesActivity) == 0 &&
		len(p.GenreActivity) == 0
}

// PersonaProfile собирает все нужные сигналы из БД 4-мя запросами.
// Запросы не паралеллим — все идут на тот же pool, обычно <5 мс каждый
// для нашей шкалы (тысячи views на пользователя). Параллелизм не даст
// выигрыша, а добавит сложность.
func (s *Service) PersonaProfile(ctx context.Context, userID int64) (PersonaProfile, error) {
	p := PersonaProfile{
		FavoriteAuthors: map[int64]struct{}{},
		FavoriteSeries:  map[int64]struct{}{},
		FavoriteBooks:   map[int64]struct{}{},
		BookActivity:    map[int64]float64{},
		AuthorActivity:  map[int64]float64{},
		SeriesActivity:  map[int64]float64{},
		GenreActivity:   map[string]float64{},
	}

	// 1. Подписки. Три простых SELECT id WHERE user_id — каждый кладёт
	// найденное в свой set.
	for _, q := range []struct {
		sql string
		dst map[int64]struct{}
	}{
		{`SELECT author_id FROM favorite_authors WHERE user_id = $1`, p.FavoriteAuthors},
		{`SELECT series_id FROM favorite_series  WHERE user_id = $1`, p.FavoriteSeries},
		{`SELECT book_id   FROM favorites        WHERE user_id = $1`, p.FavoriteBooks},
	} {
		rs, err := s.pool.Query(ctx, q.sql, userID)
		if err != nil {
			return PersonaProfile{}, fmt.Errorf("query favorites: %w", err)
		}
		for rs.Next() {
			var id int64
			if err := rs.Scan(&id); err != nil {
				rs.Close()
				return PersonaProfile{}, err
			}
			q.dst[id] = struct{}{}
		}
		rs.Close()
		if err := rs.Err(); err != nil {
			return PersonaProfile{}, err
		}
	}

	// 2. Активность по конкретным книгам — сильнейший персональный сигнал
	// "пользователь уже смотрел эту книгу, наверняка хочет её снова найти".
	rowsBA, err := s.pool.Query(ctx, `
		SELECT book_id, sum(w) FROM (
			SELECT book_id, 1.0::float AS w FROM views WHERE user_id = $1
			UNION ALL
			SELECT book_id, 3.0::float AS w FROM reads WHERE user_id = $1
		) e
		GROUP BY book_id
	`, userID)
	if err != nil {
		return PersonaProfile{}, fmt.Errorf("book activity: %w", err)
	}
	for rowsBA.Next() {
		var (
			id int64
			w  float64
		)
		if err := rowsBA.Scan(&id, &w); err != nil {
			rowsBA.Close()
			return PersonaProfile{}, err
		}
		p.BookActivity[id] = w
	}
	rowsBA.Close()
	if err := rowsBA.Err(); err != nil {
		return PersonaProfile{}, err
	}

	// 3. Активность по авторам.
	//
	// UNION ALL с весами 1.0 (view) и 3.0 (read), плюс агрегация по
	// автору. Если у книги два соавтора, эвент засчитывается обоим —
	// это ОК: интерес одинаково распределяется.
	rows, err := s.pool.Query(ctx, `
		WITH events AS (
			SELECT book_id, 1.0::float AS w FROM views WHERE user_id = $1
			UNION ALL
			SELECT book_id, 3.0::float AS w FROM reads WHERE user_id = $1
		)
		SELECT ba.author_id, sum(e.w)
		FROM events e
		JOIN book_authors ba ON ba.book_id = e.book_id
		GROUP BY ba.author_id
	`, userID)
	if err != nil {
		return PersonaProfile{}, fmt.Errorf("author activity: %w", err)
	}
	for rows.Next() {
		var (
			id int64
			w  float64
		)
		if err := rows.Scan(&id, &w); err != nil {
			rows.Close()
			return PersonaProfile{}, err
		}
		p.AuthorActivity[id] = w
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return PersonaProfile{}, err
	}

	// 4. Активность по сериям (только книги, реально лежащие в серии).
	rows, err = s.pool.Query(ctx, `
		WITH events AS (
			SELECT book_id, 1.0::float AS w FROM views WHERE user_id = $1
			UNION ALL
			SELECT book_id, 3.0::float AS w FROM reads WHERE user_id = $1
		)
		SELECT b.series_id, sum(e.w)
		FROM events e
		JOIN books b ON b.id = e.book_id
		WHERE b.series_id IS NOT NULL
		GROUP BY b.series_id
	`, userID)
	if err != nil {
		return PersonaProfile{}, fmt.Errorf("series activity: %w", err)
	}
	for rows.Next() {
		var (
			id int64
			w  float64
		)
		if err := rows.Scan(&id, &w); err != nil {
			rows.Close()
			return PersonaProfile{}, err
		}
		p.SeriesActivity[id] = w
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return PersonaProfile{}, err
	}

	// 5. Активность по жанрам (по fb2_code, не id — Meili хранит коды).
	rows, err = s.pool.Query(ctx, `
		WITH events AS (
			SELECT book_id, 1.0::float AS w FROM views WHERE user_id = $1
			UNION ALL
			SELECT book_id, 3.0::float AS w FROM reads WHERE user_id = $1
		)
		SELECT g.fb2_code, sum(e.w)
		FROM events e
		JOIN book_genres bg ON bg.book_id = e.book_id
		JOIN genres g ON g.id = bg.genre_id
		GROUP BY g.fb2_code
	`, userID)
	if err != nil {
		return PersonaProfile{}, fmt.Errorf("genre activity: %w", err)
	}
	for rows.Next() {
		var (
			code string
			w    float64
		)
		if err := rows.Scan(&code, &w); err != nil {
			rows.Close()
			return PersonaProfile{}, err
		}
		p.GenreActivity[code] = w
	}
	rows.Close()
	return p, rows.Err()
}
