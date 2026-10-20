package history

import (
	"context"
	"fmt"
	"time"

	"github.com/skriptes/skriptes/backend/internal/recsys"
)

// Журнал позиций чтения (план recsys/01, задача 1.2): read_positions —
// append-only доля прочитанного по изданию; из него — личная скорость чтения
// и «время дочитать». reads хранит только последнее состояние.

const (
	// positionMinStep / positionMinGap — строка журнала пишется, если доля
	// сдвинулась не меньше шага ИЛИ прошло не меньше gap от прошлой строки:
	// ридер шлёт позицию с дебаунсом 3 с, на каждый перелист строка не нужна.
	positionMinStep = 0.01
	positionMinGap  = 10 * time.Minute
	// compactAfter — старше этого журнал прореживается до строки в день на книгу.
	compactAfter = 90 * 24 * time.Hour
)

// appendPosition — строка журнала по правилу positionMinStep/positionMinGap.
// Отдельно от upsert'а reads (та запись — источник правды для позиции).
// fraction в PG — REAL: 0.10 хранится как 0.1000000015, поэтому сравнение с
// допуском 1e-6, иначе сдвиг ровно на шаг терялся бы.
func (s *Service) appendPosition(ctx context.Context, userID, bookID int64, fraction float64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO read_positions (user_id, book_id, fraction, at)
		SELECT $1, $2, $3::float8, now()
		WHERE NOT EXISTS (
			SELECT 1 FROM (
				SELECT fraction, at FROM read_positions
				WHERE user_id = $1 AND book_id = $2
				ORDER BY at DESC LIMIT 1
			) last
			WHERE last.at > now() - $4::interval AND abs(last.fraction::float8 - $3::float8) + 1e-6 < $5::float8
		)
	`, userID, bookID, fraction, positionMinGap.String(), positionMinStep)
	if err != nil {
		return fmt.Errorf("append read position: %w", err)
	}
	return nil
}

// ReadingSpeed — личная скорость чтения пользователя по журналу позиций
// (recsys.EstimateSpeed). Считается на лету: на нашей шкале журнал на
// пользователя — сотни строк, миллисекунды.
func (s *Service) ReadingSpeed(ctx context.Context, userID int64) (recsys.Speed, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT p.book_id, p.fraction, p.at, b.size_bytes
		FROM read_positions p
		JOIN books b ON b.id = p.book_id
		WHERE p.user_id = $1
		ORDER BY p.book_id, p.at
	`, userID)
	if err != nil {
		return recsys.Speed{}, fmt.Errorf("read positions: %w", err)
	}
	defer rows.Close()
	var samples []recsys.PositionSample
	for rows.Next() {
		var ps recsys.PositionSample
		var fr float32
		if err := rows.Scan(&ps.BookID, &fr, &ps.At, &ps.SizeBytes); err != nil {
			return recsys.Speed{}, err
		}
		ps.Fraction = float64(fr)
		samples = append(samples, ps)
	}
	if err := rows.Err(); err != nil {
		return recsys.Speed{}, err
	}
	return recsys.EstimateSpeed(samples), nil
}

// CompactReadPositions — старше compactAfter оставить по одной (последней)
// строке в день на (пользователь, издание). Идемпотентно; на старте и раз в
// сутки (cmd/skriptes). Возвращает число удалённых строк.
func (s *Service) CompactReadPositions(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM read_positions p
		USING (
			SELECT user_id, book_id, at,
			       row_number() OVER (PARTITION BY user_id, book_id, date_trunc('day', at) ORDER BY at DESC) AS rn
			FROM read_positions
			WHERE at < now() - $1::interval
		) d
		WHERE p.user_id = d.user_id AND p.book_id = d.book_id AND p.at = d.at AND d.rn > 1
	`, compactAfter.String())
	if err != nil {
		return 0, fmt.Errorf("compact read positions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// MinutesLeft — «ещё ≈ N минут» для издания размером size с долей fraction;
// 0 — скорость неизвестна или файл с картинками (recsys.MaxSpeedFileBytes).
func MinutesLeft(speed recsys.Speed, size int64, fraction float64) int {
	if size <= 0 || size > recsys.MaxSpeedFileBytes {
		return 0
	}
	left := float64(size) * (1 - fraction)
	if left <= 0 {
		return 0
	}
	return speed.MinutesFor(int64(left))
}
