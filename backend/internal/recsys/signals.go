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

	// Полки: ★ избранное (служебная полка kind='favorites') и хотя бы одна
	// своя полка (kind='user').
	Favorite bool
	OnShelf  bool
	ShelfAt  time.Time

	// Подчёркивания и закладки: сколько и последнее.
	Annotations int
	AnnotatedAt time.Time

	// Просмотры карточек изданий: сколько и последний.
	Views    int
	ViewedAt time.Time

	// Скрыто из ленты подписок; «никогда не спрашивать оценку».
	Dismissed   bool
	DismissedAt time.Time
	PromptNever bool
	PromptAt    time.Time
}

// LastAt — последнее событие любого вида (нулевое время, если событий нет).
func (s WorkSignals) LastAt() time.Time {
	var last time.Time
	for _, t := range []time.Time{
		s.RatedAt, s.CompletedAt, s.ReadAt, s.ShelfAt, s.AnnotatedAt,
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
	// свои полки — kind='user'.
	if err := each(ctx, q, `
		SELECT b.work_id, bool_or(c.kind = 'favorites'), bool_or(c.kind = 'user'), max(cb.added_at)
		FROM user_collection_books cb
		JOIN user_collections c ON c.id = cb.collection_id
		JOIN books b ON b.id = cb.book_id
		WHERE c.user_id = $1 AND b.work_id IS NOT NULL
		GROUP BY b.work_id`,
		[]any{userID}, func(rows pgx.Rows) error {
			var (
				id       int64
				fav, usr bool
				at       time.Time
			)
			if err := rows.Scan(&id, &fav, &usr, &at); err != nil {
				return err
			}
			s := get(id)
			s.Favorite, s.OnShelf, s.ShelfAt = fav, usr, at
			return nil
		}); err != nil {
		return nil, fmt.Errorf("shelves: %w", err)
	}

	// 6. Скрыто из ленты (по работе).
	if err := each(ctx, q, `SELECT work_id, dismissed_at FROM feed_dismissals WHERE user_id = $1`,
		[]any{userID}, func(rows pgx.Rows) error {
			var (
				id int64
				at time.Time
			)
			if err := rows.Scan(&id, &at); err != nil {
				return err
			}
			s := get(id)
			s.Dismissed, s.DismissedAt = true, at
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

	return out, nil
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
