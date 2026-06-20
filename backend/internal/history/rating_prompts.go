package history

import (
	"context"
	"fmt"
)

// Отложенные запросы оценки. 99.9% чтения — на Kindle, точной синхронизации
// статуса нет, поэтому «вероятно прочитано» аппроксимируем по моменту
// приобретения (acquired_at) + явным сигналам чтения. См. PR «rating prompts».

// RateableItem — книга (work-level) в блоке «Оцените прочитанное».
// ID — представительное издание (для on-demand обложки), WorkID — id работы
// для ссылки на карточку.
type RateableItem struct {
	ID        int64    `json:"id"`
	WorkID    int64    `json:"work_id,omitempty"`
	Title     string   `json:"title"`
	Authors   []string `json:"authors"`
	Series    string   `json:"series,omitempty"`
	LibID     string   `json:"lib_id"`
	CoverPath string   `json:"cover_path,omitempty"`
}

// RecordAcquisition — фиксирует ПЕРВОЕ приобретение издания (Send-to-Kindle /
// скачивание). acquired_at ставится один раз (COALESCE — самое раннее не
// перетирается повторными скачиваниями); по нему через задержку книга станет
// пригодной к запросу оценки. updated_at бампается всегда (слабый сигнал интереса
// для re-ranking, как старый RecordRead).
func (s *Service) RecordAcquisition(ctx context.Context, userID, bookID int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO reads (user_id, book_id, acquired_at, updated_at)
		VALUES ($1, $2, now(), now())
		ON CONFLICT (user_id, book_id)
		DO UPDATE SET acquired_at = COALESCE(reads.acquired_at, now()), updated_at = now()
	`, userID, bookID)
	if err != nil {
		return fmt.Errorf("record acquisition: %w", err)
	}
	return nil
}

// RateableWorks — пул блока «Оцените прочитанное»: работы, которые пользователь
// вероятно прочитал и ещё не оценил. Пригодность (на работу, агрегируя издания):
//   - read_signal = любое издание отмечено «Прочитана» ИЛИ web-fraction ≥ 0.95
//     → пригодна СРАЗУ (без ожидания задержки);
//   - acquired_eligible = min(acquired_at) ≤ now() − delay.
//
// Исключаем: уже оценённые; скрытые 'never' (бесповоротно — «не буду оценивать»);
// 'snooze' (пока snoozed_until в будущем). read_signal сортируется выше отложенных.
func (s *Service) RateableWorks(ctx context.Context, userID int64, delayDays, limit int) ([]RateableItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if delayDays < 1 {
		delayDays = 30
	}
	rows, err := s.pool.Query(ctx, `
		WITH p0 AS (
			SELECT $1::bigint AS uid, $2::int AS delay_days
		),
		wr AS (
			SELECT b.work_id AS work_id,
			       min(r.acquired_at) AS acquired_at,
			       bool_or(r.completed_at IS NOT NULL) AS completed,
			       max(COALESCE(r.fraction, 0)) AS max_fraction
			FROM reads r
			JOIN books b ON b.id = r.book_id AND b.deleted = false AND b.work_id IS NOT NULL
			WHERE r.user_id = (SELECT uid FROM p0)
			GROUP BY b.work_id
		),
		elig AS (
			SELECT wr.work_id, wr.acquired_at,
			       (wr.completed OR wr.max_fraction >= 0.95) AS read_signal
			FROM wr
			WHERE NOT EXISTS (
			          SELECT 1 FROM book_ratings br
			          WHERE br.user_id = (SELECT uid FROM p0) AND br.work_id = wr.work_id)
			  AND (
			          wr.completed OR wr.max_fraction >= 0.95
			          OR (wr.acquired_at IS NOT NULL
			              AND wr.acquired_at <= now() - make_interval(days => (SELECT delay_days FROM p0)))
			      )
			  AND NOT EXISTS (
			          SELECT 1 FROM book_rating_prompts p
			          WHERE p.user_id = (SELECT uid FROM p0) AND p.work_id = wr.work_id
			            AND (
			                p.state = 'never'
			                OR (p.state = 'snooze' AND p.snoozed_until > now())
			            )
			      )
			ORDER BY (wr.completed OR wr.max_fraction >= 0.95) DESC, wr.acquired_at DESC NULLS LAST
			LIMIT $3
		),
		rep AS (
			SELECT e.work_id, e.read_signal, e.acquired_at,
			       (SELECT bb.id FROM books bb
			        WHERE bb.work_id = e.work_id AND bb.deleted = false
			        ORDER BY bb.id LIMIT 1) AS rep_id
			FROM elig e
		)
		SELECT rep.work_id, b.id, b.title, b.lib_id, ser.title,
		       COALESCE(b.cover_path, (
		           SELECT bb.cover_path FROM books bb
		           WHERE bb.work_id = rep.work_id AND bb.deleted = false
		             AND bb.cover_path IS NOT NULL AND bb.cover_path <> ''
		           ORDER BY bb.id LIMIT 1
		       ), ''),
		       COALESCE(
		           array_agg(DISTINCT TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name))) FILTER (WHERE a.id IS NOT NULL),
		           ARRAY[]::text[]
		       )
		FROM rep
		JOIN books b ON b.id = rep.rep_id
		LEFT JOIN series ser ON ser.id = b.series_id
		LEFT JOIN book_authors ba ON ba.book_id = b.id
		LEFT JOIN authors a ON a.id = ba.author_id
		GROUP BY rep.work_id, rep.read_signal, rep.acquired_at, b.id, b.title, b.lib_id, ser.title, b.cover_path
		ORDER BY rep.read_signal DESC, rep.acquired_at DESC NULLS LAST
	`, userID, delayDays, limit)
	if err != nil {
		return nil, fmt.Errorf("query rateable works: %w", err)
	}
	defer rows.Close()
	out := make([]RateableItem, 0)
	for rows.Next() {
		var (
			it     RateableItem
			workID *int64
			series *string
		)
		if err := rows.Scan(&workID, &it.ID, &it.Title, &it.LibID, &series, &it.CoverPath, &it.Authors); err != nil {
			return nil, err
		}
		if workID != nil {
			it.WorkID = *workID
		}
		if series != nil {
			it.Series = *series
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// DismissRatingPrompt — «не буду оценивать»: бесповоротно убрать книгу из блока
// «Оцените прочитанное» (даже если она прочитана). Оценить всё равно можно с
// карточки книги; на сам факт оценки/прочтения это скрытие не влияет.
func (s *Service) DismissRatingPrompt(ctx context.Context, userID, workID int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO book_rating_prompts (user_id, work_id, state, snoozed_until, updated_at)
		VALUES ($1, $2, 'never', NULL, now())
		ON CONFLICT (user_id, work_id)
		DO UPDATE SET state = 'never', snoozed_until = NULL, updated_at = now()
	`, userID, workID)
	if err != nil {
		return fmt.Errorf("dismiss rating prompt: %w", err)
	}
	return nil
}

// SnoozeRatingPrompt — «ещё не прочитал»: скрыть запрос на delay дней, потом
// спросить снова.
func (s *Service) SnoozeRatingPrompt(ctx context.Context, userID, workID int64, delayDays int) error {
	if delayDays < 1 {
		delayDays = 30
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO book_rating_prompts (user_id, work_id, state, snoozed_until, updated_at)
		VALUES ($1, $2, 'snooze', now() + make_interval(days => $3::int), now())
		ON CONFLICT (user_id, work_id)
		DO UPDATE SET state = 'snooze', snoozed_until = now() + make_interval(days => $3::int), updated_at = now()
	`, userID, workID, delayDays)
	if err != nil {
		return fmt.Errorf("snooze rating prompt: %w", err)
	}
	return nil
}
