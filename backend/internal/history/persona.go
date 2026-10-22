package history

import (
	"context"
	"fmt"
	"time"

	"github.com/skriptes/skriptes/backend/internal/recsys"
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
// разные коэффициенты (см. bonus*/malus* в internal/books/service.go).
type PersonaProfile struct {
	FavoriteAuthors map[int64]struct{}
	FavoriteSeries  map[int64]struct{}
	FavoriteBooks   map[int64]struct{}

	// BookActivity[bookID] = сумма весов view/read событий ИМЕННО для этой
	// книги. Сильный персональный сигнал: "пользователь уже открывал /
	// скачивал эту книгу — наверняка хочет её снова увидеть в поиске".
	BookActivity map[int64]float64

	// FavoriteWorks / WorkActivity — те же сигналы, сведённые к работе
	// (books.work_id): выдача по индексу works несёт id РАБОТЫ, а id работ и
	// изданий — разные последовательности с пересечением. Сравнивать id работы с
	// BookActivity нельзя: бонус «уже открывал» доставался чужой работе с тем же
	// номером (#399).
	FavoriteWorks map[int64]struct{}
	WorkActivity  map[int64]float64

	// AuthorActivity[authorID] = сумма весов событий с книгами этого автора.
	AuthorActivity map[int64]float64
	// SeriesActivity[seriesID] = аналогично для серий.
	SeriesActivity map[int64]float64
	// GenreActivity[fb2_code] = аналогично для жанров.
	GenreActivity map[string]float64

	// Негативы (план recsys/03, задача 3.5) — мягкий малус в пересортировке
	// поиска (books.applyPersonaBoost); позитивную часть профиля не меняют.
	// Определения — общие с моделью близости (internal/recsys): «не
	// предлагать» автора/цикл — blocked_authors/blocked_series
	// (recsys.LoadBlocklist); «не интересно» — feed_dismissals с причиной
	// not_interested или без неё (recsys.DismissReason.Negative); «бросил» —
	// recsys.DefaultWeights.IsDropped (не дочитано, доля < DroppedMaxFraction,
	// движение старше DroppedMinAge). Работы — по work_id (#399): для индекса
	// изданий хит несёт work_id документа.
	BlockedAuthors     map[int64]struct{}
	BlockedSeries      map[int64]struct{}
	NotInterestedWorks map[int64]struct{}
	DroppedWorks       map[int64]struct{}
}

// MaxPersonaAuthors — у книги с бо́льшим числом авторов (антологии, сборники
// журналов) авторские сигналы не работают ни при сборке профиля, ни при
// пересортировке: открытая антология не делает «интересными» сотни её авторов, а
// подписка на одного из 334 авторов «Тени над рекою» не ставит её первой на
// «собачье сердце» (#399).
const MaxPersonaAuthors = 3

// IsEmpty — true если у пользователя нет ни одного сигнала.
// Тогда re-ranking бессмыслен — экономим CPU и возвращаем результат
// Meili как есть.
func (p PersonaProfile) IsEmpty() bool {
	return len(p.FavoriteAuthors) == 0 &&
		len(p.FavoriteSeries) == 0 &&
		len(p.FavoriteBooks) == 0 &&
		len(p.BookActivity) == 0 &&
		len(p.FavoriteWorks) == 0 &&
		len(p.WorkActivity) == 0 &&
		len(p.AuthorActivity) == 0 &&
		len(p.SeriesActivity) == 0 &&
		len(p.GenreActivity) == 0 &&
		len(p.BlockedAuthors) == 0 &&
		len(p.BlockedSeries) == 0 &&
		len(p.NotInterestedWorks) == 0 &&
		len(p.DroppedWorks) == 0
}

// PersonaProfile собирает все нужные сигналы из БД несколькими короткими запросами.
// Запросы не паралеллим — все идут на тот же pool, обычно <5 мс каждый
// для нашей шкалы (тысячи views на пользователя). Параллелизм не даст
// выигрыша, а добавит сложность.
func (s *Service) PersonaProfile(ctx context.Context, userID int64) (PersonaProfile, error) {
	p := PersonaProfile{
		FavoriteAuthors: map[int64]struct{}{},
		FavoriteSeries:  map[int64]struct{}{},
		FavoriteBooks:   map[int64]struct{}{},
		BookActivity:    map[int64]float64{},
		FavoriteWorks:   map[int64]struct{}{},
		WorkActivity:    map[int64]float64{},
		AuthorActivity:  map[int64]float64{},
		SeriesActivity:  map[int64]float64{},
		GenreActivity:   map[string]float64{},

		BlockedAuthors:     map[int64]struct{}{},
		BlockedSeries:      map[int64]struct{}{},
		NotInterestedWorks: map[int64]struct{}{},
		DroppedWorks:       map[int64]struct{}{},
	}

	// 1. Подписки. Три простых SELECT id WHERE user_id — каждый кладёт
	// найденное в свой set.
	for _, q := range []struct {
		sql string
		dst map[int64]struct{}
	}{
		{`SELECT author_id FROM favorite_authors WHERE user_id = $1`, p.FavoriteAuthors},
		{`SELECT series_id FROM favorite_series  WHERE user_id = $1`, p.FavoriteSeries},
		// Книжное избранное теперь = членство в служебной полке kind='favorites' (миграция 0023).
		{`SELECT cb.book_id FROM user_collection_books cb JOIN user_collections c ON c.id = cb.collection_id WHERE c.user_id = $1 AND c.kind = 'favorites'`, p.FavoriteBooks},
		{`SELECT DISTINCT b.work_id FROM user_collection_books cb JOIN user_collections c ON c.id = cb.collection_id JOIN books b ON b.id = cb.book_id WHERE c.user_id = $1 AND c.kind = 'favorites' AND b.work_id IS NOT NULL`, p.FavoriteWorks},
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

	// 2a. Та же активность по работам — для выдачи по индексу works.
	rowsWA, err := s.pool.Query(ctx, `
		WITH events AS (
			SELECT book_id, 1.0::float AS w FROM views WHERE user_id = $1
			UNION ALL
			SELECT book_id, 3.0::float AS w FROM reads WHERE user_id = $1
		)
		SELECT b.work_id, sum(e.w)
		FROM events e
		JOIN books b ON b.id = e.book_id
		WHERE b.work_id IS NOT NULL
		GROUP BY b.work_id
	`, userID)
	if err != nil {
		return PersonaProfile{}, fmt.Errorf("work activity: %w", err)
	}
	for rowsWA.Next() {
		var (
			id int64
			w  float64
		)
		if err := rowsWA.Scan(&id, &w); err != nil {
			rowsWA.Close()
			return PersonaProfile{}, err
		}
		p.WorkActivity[id] = w
	}
	rowsWA.Close()
	if err := rowsWA.Err(); err != nil {
		return PersonaProfile{}, err
	}

	// 3. Активность по авторам.
	//
	// UNION ALL с весами 1.0 (view) и 3.0 (read), плюс агрегация по
	// автору. Если у книги два соавтора, эвент засчитывается обоим —
	// это ОК: интерес одинаково распределяется. Книги, где авторов больше
	// MaxPersonaAuthors (антологии), не в счёт.
	rows, err := s.pool.Query(ctx, `
		WITH events AS (
			SELECT book_id, 1.0::float AS w FROM views WHERE user_id = $1
			UNION ALL
			SELECT book_id, 3.0::float AS w FROM reads WHERE user_id = $1
		)
		SELECT ba.author_id, sum(e.w)
		FROM events e
		JOIN book_authors ba ON ba.book_id = e.book_id
		WHERE (SELECT count(*) FROM book_authors x WHERE x.book_id = e.book_id) <= $2
		GROUP BY ba.author_id
	`, userID, MaxPersonaAuthors)
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
	if err := rows.Err(); err != nil {
		return PersonaProfile{}, err
	}

	// 6. Негативы (recsys/03, 3.5) — три коротких запроса с предикатами
	// recsys, а не recsys.BuildProfile целиком: он делает 14 запросов (и
	// метаданные работ), а персона грузится на каждый поиск и подсказку.
	blockedAuthors, blockedSeries, err := recsys.LoadBlocklist(ctx, s.pool, userID)
	if err != nil {
		return PersonaProfile{}, err
	}
	for _, id := range blockedAuthors {
		p.BlockedAuthors[id] = struct{}{}
	}
	for _, id := range blockedSeries {
		p.BlockedSeries[id] = struct{}{}
	}

	rows, err = s.pool.Query(ctx, `SELECT work_id, COALESCE(reason, '') FROM feed_dismissals WHERE user_id = $1`, userID)
	if err != nil {
		return PersonaProfile{}, fmt.Errorf("dismissals: %w", err)
	}
	for rows.Next() {
		var (
			id     int64
			reason string
		)
		if err := rows.Scan(&id, &reason); err != nil {
			rows.Close()
			return PersonaProfile{}, err
		}
		if recsys.DismissReason(reason).Negative() {
			p.NotInterestedWorks[id] = struct{}{}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return PersonaProfile{}, err
	}

	// «Бросил» — агрегат чтения по работе, как recsys.LoadSignals: дочитанное
	// хотя бы в одном издании не брошено.
	now := time.Now()
	rows, err = s.pool.Query(ctx, `
		SELECT b.work_id, bool_or(r.completed_at IS NOT NULL), max(r.fraction)::float8, max(r.updated_at)
		FROM reads r
		JOIN books b ON b.id = r.book_id
		WHERE r.user_id = $1 AND b.work_id IS NOT NULL
		GROUP BY b.work_id
	`, userID)
	if err != nil {
		return PersonaProfile{}, fmt.Errorf("dropped: %w", err)
	}
	for rows.Next() {
		var sig recsys.WorkSignals
		if err := rows.Scan(&sig.WorkID, &sig.Completed, &sig.Fraction, &sig.ReadAt); err != nil {
			rows.Close()
			return PersonaProfile{}, err
		}
		if recsys.DefaultWeights.IsDropped(sig, now) {
			p.DroppedWorks[sig.WorkID] = struct{}{}
		}
	}
	rows.Close()
	return p, rows.Err()
}
