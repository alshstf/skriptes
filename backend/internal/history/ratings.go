package history

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Пользовательские оценки книг (work-level). Это ОТДЕЛЬНАЯ сущность от
// books.rating (LIBRATE из INPX — библиотечный рейтинг). Оцениваем логическую
// книгу (work_id), а не конкретное издание — как избранное/чтение. Шкала 1–5,
// одна оценка на (пользователь, работа); 0 не хранится (нет оценки = нет строки).

// ErrInvalidRating — оценка вне допустимого диапазона 1–5.
var ErrInvalidRating = errors.New("rating must be between 1 and 5")

// SetRating — поставить/изменить оценку пользователя работе. Повторный вызов
// обновляет оценку и rated_at. Снятие оценки — RemoveRating.
func (s *Service) SetRating(ctx context.Context, userID, workID int64, rating int) error {
	if rating < 1 || rating > 5 {
		return ErrInvalidRating
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO book_ratings (user_id, work_id, rating)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, work_id)
		DO UPDATE SET rating = EXCLUDED.rating, rated_at = now()
	`, userID, workID, rating)
	if err != nil {
		return fmt.Errorf("set rating: %w", err)
	}
	return nil
}

// RemoveRating — снять оценку. Идемпотентна.
func (s *Service) RemoveRating(ctx context.Context, userID, workID int64) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM book_ratings WHERE user_id = $1 AND work_id = $2
	`, userID, workID)
	if err != nil {
		return fmt.Errorf("remove rating: %w", err)
	}
	return nil
}

// UserRating — оценка пользователя работе. ok=false, если не оценивал.
func (s *Service) UserRating(ctx context.Context, userID, workID int64) (int, bool, error) {
	var rating int
	err := s.pool.QueryRow(ctx, `
		SELECT rating FROM book_ratings WHERE user_id = $1 AND work_id = $2
	`, userID, workID).Scan(&rating)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("query user rating: %w", err)
	}
	return rating, true, nil
}

// WorkRatingAggregate — средняя оценка работы по инстансу + число голосов.
// Если оценок нет — (0, 0, nil).
func (s *Service) WorkRatingAggregate(ctx context.Context, workID int64) (float64, int, error) {
	var (
		avg   pgtype.Float8
		count int
	)
	err := s.pool.QueryRow(ctx, `
		SELECT avg(rating)::float8, count(*)::int
		FROM book_ratings WHERE work_id = $1
	`, workID).Scan(&avg, &count)
	if err != nil {
		return 0, 0, fmt.Errorf("query rating aggregate: %w", err)
	}
	if !avg.Valid {
		return 0, 0, nil
	}
	return avg.Float64, count, nil
}
