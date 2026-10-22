package recsys

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Querier — срез *pgxpool.Pool / pgx.Tx, нужный загрузчику.
type Querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// WorkSignals — сырые сигналы пользователя по одной работе, сведённые со всех
// её изданий. Чтение, просмотры, полки и подчёркивания в БД живут по изданию;
// оценки, скрытое из ленты и отказы оценивать — по работе.
type WorkSignals struct {
	WorkID int64

	// Оценка 1–5 (0 — нет) и когда поставлена.
	Rating  int
	RatedAt time.Time

	// Чтение. Completed — дочитано хотя бы одно издание. Fraction —
	// наибольшая доля прочитанного среди изданий (nil — ни одно не открывалось
	// в веб-ридере: Kindle-чтение доли не даёт). ReadAt — последнее движение по
	// любому изданию (reads.updated_at: позиция, приобретение, отметка).
	// Acquired — скачано через OPDS / отправлено на Kindle. Opened — есть
	// позиция, доля или завершение.
	Completed   bool
	CompletedAt time.Time
	Fraction    *float64
	ReadAt      time.Time
	Acquired    bool
	Opened      bool

	// Полки: ★ избранное (служебная полка kind='favorites'), хотя бы одна
	// своя полка (kind='user') и «Хочу прочитать» (kind='to_read', recsys/03
	// 3.3; ToReadAt — когда положили).
	Favorite bool
	OnShelf  bool
	ShelfAt  time.Time
	ToRead   bool
	ToReadAt time.Time

	// Причины низкой оценки (recsys/03, 3.1; пусто — не указывал).
	RatingReasons []RatingReason

	// Подчёркивания и закладки: сколько и последнее.
	Annotations int
	AnnotatedAt time.Time

	// Просмотры карточек изданий: сколько и последний.
	Views    int
	ViewedAt time.Time

	// Скрыто из ленты (с причиной — DismissReason; пустая = «не интересно»);
	// «никогда не спрашивать оценку».
	Dismissed     bool
	DismissReason DismissReason
	DismissedAt   time.Time
	PromptNever   bool
	PromptAt      time.Time
}

// HasReason — среди причин низкой оценки есть r.
func (s WorkSignals) HasReason(r RatingReason) bool {
	for _, x := range s.RatingReasons {
		if x == r {
			return true
		}
	}
	return false
}

// LastAt — последнее событие любого вида (нулевое время, если событий нет).
func (s WorkSignals) LastAt() time.Time {
	var last time.Time
	for _, t := range []time.Time{
		s.RatedAt, s.CompletedAt, s.ReadAt, s.ShelfAt, s.ToReadAt, s.AnnotatedAt,
		s.ViewedAt, s.DismissedAt, s.PromptAt,
	} {
		if t.After(last) {
			last = t
		}
	}
	return last
}

// LoadSignals — все сигналы пользователя по работам: семь коротких запросов по
// user_id (у каждого — индекс), результат — по работе. Запросы не параллелим:
// на нашей шкале (тысячи событий на пользователя) каждый < 5 мс.
func LoadSignals(ctx context.Context, q Querier, userID int64) (map[int64]*WorkSignals, error) {
	out := map[int64]*WorkSignals{}
	get := func(workID int64) *WorkSignals {
		s, ok := out[workID]
		if !ok {
			s = &WorkSignals{WorkID: workID}
			out[workID] = s
		}
		return s
	}

	// 1. Оценки (по работе).
	if err := each(ctx, q, `SELECT work_id, rating, rated_at FROM book_ratings WHERE user_id = $1`,
		[]any{userID}, func(rows pgx.Rows) error {
			var (
				id int64
				r  int
				at time.Time
			)
			if err := rows.Scan(&id, &r, &at); err != nil {
				return err
			}
			s := get(id)
			s.Rating, s.RatedAt = r, at
			return nil
		}); err != nil {
		return nil, fmt.Errorf("ratings: %w", err)
	}

	// 2. Чтение — агрегат по изданиям работы.
	if err := each(ctx, q, `
		SELECT b.work_id,
		       bool_or(r.completed_at IS NOT NULL),
		       max(r.completed_at),
		       max(r.fraction)::float8,
		       max(r.updated_at),
		       bool_or(r.acquired_at IS NOT NULL),
		       bool_or(r.last_pos IS NOT NULL OR r.fraction IS NOT NULL OR r.completed_at IS NOT NULL)
		FROM reads r
		JOIN books b ON b.id = r.book_id
		WHERE r.user_id = $1 AND b.work_id IS NOT NULL
		GROUP BY b.work_id`,
		[]any{userID}, func(rows pgx.Rows) error {
			var (
				id          int64
				completed   bool
				completedAt *time.Time
				fraction    *float64
				readAt      time.Time
				acquired    bool
				opened      bool
			)
			if err := rows.Scan(&id, &completed, &completedAt, &fraction, &readAt, &acquired, &opened); err != nil {
				return err
			}
			s := get(id)
			s.Completed, s.Fraction, s.ReadAt, s.Acquired, s.Opened = completed, fraction, readAt, acquired, opened
			if completedAt != nil {
				s.CompletedAt = *completedAt
			}
			return nil
		}); err != nil {
		return nil, fmt.Errorf("reads: %w", err)
	}

	// 3. Просмотры карточек.
	if err := each(ctx, q, `
		SELECT b.work_id, count(*), max(v.viewed_at)
		FROM views v
		JOIN books b ON b.id = v.book_id
		WHERE v.user_id = $1 AND b.work_id IS NOT NULL
		GROUP BY b.work_id`,
		[]any{userID}, func(rows pgx.Rows) error {
			var (
				id int64
				n  int
				at time.Time
			)
			if err := rows.Scan(&id, &n, &at); err != nil {
				return err
			}
			s := get(id)
			s.Views, s.ViewedAt = n, at
			return nil
		}); err != nil {
		return nil, fmt.Errorf("views: %w", err)
	}

	// 4. Подчёркивания и закладки.
	if err := each(ctx, q, `
		SELECT b.work_id, count(*), max(a.created_at)
		FROM annotations a
		JOIN books b ON b.id = a.book_id
		WHERE a.user_id = $1 AND b.work_id IS NOT NULL
		GROUP BY b.work_id`,
		[]any{userID}, func(rows pgx.Rows) error {
			var (
				id int64
				n  int
				at time.Time
			)
			if err := rows.Scan(&id, &n, &at); err != nil {
				return err
			}
			s := get(id)
			s.Annotations, s.AnnotatedAt = n, at
			return nil
		}); err != nil {
		return nil, fmt.Errorf("annotations: %w", err)
	}

	// 5. Полки: ★ избранное — служебная полка kind='favorites' (миграция 0023),
	// свои полки — kind='user', «Хочу прочитать» — kind='to_read' (0068).
	if err := each(ctx, q, `
		SELECT b.work_id, bool_or(c.kind = 'favorites'), bool_or(c.kind = 'user'),
		       max(cb.added_at) FILTER (WHERE c.kind <> 'to_read'),
		       bool_or(c.kind = 'to_read'), max(cb.added_at) FILTER (WHERE c.kind = 'to_read')
		FROM user_collection_books cb
		JOIN user_collections c ON c.id = cb.collection_id
		JOIN books b ON b.id = cb.book_id
		WHERE c.user_id = $1 AND b.work_id IS NOT NULL
		GROUP BY b.work_id`,
		[]any{userID}, func(rows pgx.Rows) error {
			var (
				id                int64
				fav, usr, toRead  bool
				shelfAt, toReadAt *time.Time
			)
			if err := rows.Scan(&id, &fav, &usr, &shelfAt, &toRead, &toReadAt); err != nil {
				return err
			}
			s := get(id)
			s.Favorite, s.OnShelf, s.ToRead = fav, usr, toRead
			if shelfAt != nil {
				s.ShelfAt = *shelfAt
			}
			if toReadAt != nil {
				s.ToReadAt = *toReadAt
			}
			return nil
		}); err != nil {
		return nil, fmt.Errorf("shelves: %w", err)
	}

	// 6. Скрыто из ленты (по работе) с причиной.
	if err := each(ctx, q, `SELECT work_id, dismissed_at, COALESCE(reason, '') FROM feed_dismissals WHERE user_id = $1`,
		[]any{userID}, func(rows pgx.Rows) error {
			var (
				id     int64
				at     time.Time
				reason string
			)
			if err := rows.Scan(&id, &at, &reason); err != nil {
				return err
			}
			s := get(id)
			s.Dismissed, s.DismissedAt, s.DismissReason = true, at, DismissReason(reason)
			return nil
		}); err != nil {
		return nil, fmt.Errorf("dismissals: %w", err)
	}

	// 7. «Никогда не спрашивать оценку» (snooze — не сигнал).
	if err := each(ctx, q, `SELECT work_id, updated_at FROM book_rating_prompts WHERE user_id = $1 AND state = 'never'`,
		[]any{userID}, func(rows pgx.Rows) error {
			var (
				id int64
				at time.Time
			)
			if err := rows.Scan(&id, &at); err != nil {
				return err
			}
			s := get(id)
			s.PromptNever, s.PromptAt = true, at
			return nil
		}); err != nil {
		return nil, fmt.Errorf("rating prompts: %w", err)
	}

	// 8. Причины низкой оценки (recsys/03, 3.1) — в порядке чипов.
	if err := each(ctx, q, `SELECT work_id, reason FROM rating_reasons WHERE user_id = $1 ORDER BY work_id, at, reason`,
		[]any{userID}, func(rows pgx.Rows) error {
			var (
				id     int64
				reason string
			)
			if err := rows.Scan(&id, &reason); err != nil {
				return err
			}
			s := get(id)
			s.RatingReasons = append(s.RatingReasons, RatingReason(reason))
			return nil
		}); err != nil {
		return nil, fmt.Errorf("rating reasons: %w", err)
	}

	return out, nil
}

// LoadBlocklist — «не предлагать» (recsys/03, 3.2): авторы и циклы, которые
// пользователь исключил из рекомендаций. Это не скрытие контента: каталог и
// поиск их показывают; лента правил (Hydrate через Exclusions) и новинки по
// подпискам (history.SubscriptionFeed) — нет.
func LoadBlocklist(ctx context.Context, q Querier, userID int64) (authors, series []int64, err error) {
	if err := each(ctx, q, `SELECT author_id FROM blocked_authors WHERE user_id = $1 ORDER BY author_id`,
		[]any{userID}, func(rows pgx.Rows) error {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return err
			}
			authors = append(authors, id)
			return nil
		}); err != nil {
		return nil, nil, fmt.Errorf("blocked authors: %w", err)
	}
	if err := each(ctx, q, `SELECT series_id FROM blocked_series WHERE user_id = $1 ORDER BY series_id`,
		[]any{userID}, func(rows pgx.Rows) error {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return err
			}
			series = append(series, id)
			return nil
		}); err != nil {
		return nil, nil, fmt.Errorf("blocked series: %w", err)
	}
	return authors, series, nil
}

// each — Query + перебор строк с закрытием; fn вызывается на каждой строке.
func each(ctx context.Context, q Querier, sql string, args []any, fn func(pgx.Rows) error) error {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}
