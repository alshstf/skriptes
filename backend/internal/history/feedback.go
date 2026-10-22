package history

import (
	"context"
	"fmt"
	"time"

	"github.com/skriptes/skriptes/backend/internal/recsys"
)

// Явная обратная связь (план recsys/03): причины низкой оценки (3.1), «не
// предлагать» автора/цикл (3.2), полка «Хочу прочитать» (3.3), лог настроения
// сессии (3.4). Модель близости
// читает всё это в internal/recsys (LoadSignals, LoadBlocklist); здесь — запись
// и чтение для карточек и профиля.

// ── 3.1 Причины низкой оценки ───────────────────────────────────────

// SetRatingReasons — заменить набор причин низкой оценки работы (пустой —
// убрать все). Неизвестная причина — ErrInvalidRatingReason. Без оценки
// причины тоже допустимы (оценку могли снять раньше — тогда их уже нет).
func (s *Service) SetRatingReasons(ctx context.Context, userID, workID int64, reasons []recsys.RatingReason) error {
	seen := map[recsys.RatingReason]struct{}{}
	uniq := make([]string, 0, len(reasons))
	for _, r := range reasons {
		if !recsys.IsRatingReason(string(r)) {
			return ErrInvalidRatingReason
		}
		if _, dup := seen[r]; dup {
			continue
		}
		seen[r] = struct{}{}
		uniq = append(uniq, string(r))
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("set rating reasons: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM rating_reasons WHERE user_id = $1 AND work_id = $2 AND NOT (reason = ANY($3))`,
		userID, workID, uniq); err != nil {
		return fmt.Errorf("set rating reasons: delete: %w", err)
	}
	if len(uniq) > 0 {
		if _, err := tx.Exec(ctx, `
			INSERT INTO rating_reasons (user_id, work_id, reason)
			SELECT $1, $2, unnest($3::text[])
			ON CONFLICT (user_id, work_id, reason) DO NOTHING`, userID, workID, uniq); err != nil {
			return fmt.Errorf("set rating reasons: insert: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("set rating reasons: commit: %w", err)
	}
	return nil
}

// RatingReasons — причины низкой оценки работы в порядке указания.
func (s *Service) RatingReasons(ctx context.Context, userID, workID int64) ([]recsys.RatingReason, error) {
	rows, err := s.pool.Query(ctx, `SELECT reason FROM rating_reasons WHERE user_id = $1 AND work_id = $2 ORDER BY at, reason`, userID, workID)
	if err != nil {
		return nil, fmt.Errorf("query rating reasons: %w", err)
	}
	defer rows.Close()
	out := make([]recsys.RatingReason, 0, 2)
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		out = append(out, recsys.RatingReason(r))
	}
	return out, rows.Err()
}

// ── 3.4 Настроение сессии ────────────────────────────────────────────

// LogMood — записать выбор чипа настроения на Главной (session_mood,
// append-only): само настроение живёт в браузере и уходит параметром mood= в
// GET /me/recommendations, лог — чтобы потом видеть, что человек выбирает по
// вечерам (приёмка 3.7). Неизвестное — ErrInvalidMood.
func (s *Service) LogMood(ctx context.Context, userID int64, mood recsys.Mood) error {
	if !recsys.IsMood(string(mood)) {
		return ErrInvalidMood
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO session_mood (user_id, mood) VALUES ($1, $2)`, userID, string(mood)); err != nil {
		return fmt.Errorf("log mood: %w", err)
	}
	return nil
}

// ── 3.2 «Не предлагать» автора / цикл ───────────────────────────────

// BlockAuthor / UnblockAuthor / IsAuthorBlocked — «не предлагать автора»:
// только рекомендации и новинки по подпискам, каталог и поиск не меняются.
// Идемпотентны.
func (s *Service) BlockAuthor(ctx context.Context, userID, authorID int64) error {
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO blocked_authors (user_id, author_id) VALUES ($1, $2)
		ON CONFLICT (user_id, author_id) DO NOTHING`, userID, authorID); err != nil {
		return fmt.Errorf("block author: %w", err)
	}
	return nil
}

func (s *Service) UnblockAuthor(ctx context.Context, userID, authorID int64) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM blocked_authors WHERE user_id = $1 AND author_id = $2`, userID, authorID); err != nil {
		return fmt.Errorf("unblock author: %w", err)
	}
	return nil
}

func (s *Service) IsAuthorBlocked(ctx context.Context, userID, authorID int64) (bool, error) {
	var ok bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM blocked_authors WHERE user_id = $1 AND author_id = $2)`,
		userID, authorID).Scan(&ok); err != nil {
		return false, fmt.Errorf("query author blocked: %w", err)
	}
	return ok, nil
}

// BlockSeries / UnblockSeries / IsSeriesBlocked — то же для цикла.
func (s *Service) BlockSeries(ctx context.Context, userID, seriesID int64) error {
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO blocked_series (user_id, series_id) VALUES ($1, $2)
		ON CONFLICT (user_id, series_id) DO NOTHING`, userID, seriesID); err != nil {
		return fmt.Errorf("block series: %w", err)
	}
	return nil
}

func (s *Service) UnblockSeries(ctx context.Context, userID, seriesID int64) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM blocked_series WHERE user_id = $1 AND series_id = $2`, userID, seriesID); err != nil {
		return fmt.Errorf("unblock series: %w", err)
	}
	return nil
}

func (s *Service) IsSeriesBlocked(ctx context.Context, userID, seriesID int64) (bool, error) {
	var ok bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM blocked_series WHERE user_id = $1 AND series_id = $2)`,
		userID, seriesID).Scan(&ok); err != nil {
		return false, fmt.Errorf("query series blocked: %w", err)
	}
	return ok, nil
}

// BlockedAuthorItem / BlockedSeriesItem — строки списка «Не предлагать» в профиле.
type BlockedAuthorItem struct {
	ID       int64     `json:"id"`
	FullName string    `json:"full_name"`
	AddedAt  time.Time `json:"added_at"`
}

type BlockedSeriesItem struct {
	ID         int64     `json:"id"`
	Title      string    `json:"title"`
	AuthorName string    `json:"author_name,omitempty"`
	AddedAt    time.Time `json:"added_at"`
}

// Blocklist — GET /api/me/blocklist: что пользователь просил не предлагать.
type Blocklist struct {
	Authors []BlockedAuthorItem `json:"authors"`
	Series  []BlockedSeriesItem `json:"series"`
}

// ListBlocklist — блок-лист пользователя, свежие сверху. Скрытых на инстансе
// авторов не показываем (их и так нет нигде); серии — все.
func (s *Service) ListBlocklist(ctx context.Context, userID int64) (Blocklist, error) {
	out := Blocklist{Authors: []BlockedAuthorItem{}, Series: []BlockedSeriesItem{}}
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name)), x.added_at
		FROM blocked_authors x
		JOIN authors a ON a.id = x.author_id AND NOT a.instance_hidden
		WHERE x.user_id = $1
		ORDER BY x.added_at DESC, a.id`, userID)
	if err != nil {
		return out, fmt.Errorf("query blocked authors: %w", err)
	}
	for rows.Next() {
		var it BlockedAuthorItem
		if err := rows.Scan(&it.ID, &it.FullName, &it.AddedAt); err != nil {
			rows.Close()
			return out, err
		}
		out.Authors = append(out.Authors, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return out, err
	}
	rows, err = s.pool.Query(ctx, `
		SELECT s.id, s.title,
		       COALESCE((SELECT TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name))
		                 FROM authors a WHERE a.id = s.author_id), ''),
		       x.added_at
		FROM blocked_series x
		JOIN series s ON s.id = x.series_id
		WHERE x.user_id = $1
		ORDER BY x.added_at DESC, s.id`, userID)
	if err != nil {
		return out, fmt.Errorf("query blocked series: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var it BlockedSeriesItem
		if err := rows.Scan(&it.ID, &it.Title, &it.AuthorName, &it.AddedAt); err != nil {
			return out, err
		}
		out.Series = append(out.Series, it)
	}
	return out, rows.Err()
}

// ── 3.3 Полка «Хочу прочитать» ──────────────────────────────────────

// toReadName — имя служебной полки kind='to_read' (одна на пользователя, 0068).
const toReadName = "Хочу прочитать"

// AddToRead — положить книгу на «Хочу прочитать» (членство в служебной полке
// kind='to_read', как AddFavorite для ★). Идемпотентна.
func (s *Service) AddToRead(ctx context.Context, userID, bookID int64) error {
	if _, err := s.pool.Exec(ctx, `
		WITH shelf AS (
			INSERT INTO user_collections (user_id, name, kind)
			VALUES ($1, $3, 'to_read')
			ON CONFLICT (user_id) WHERE kind = 'to_read'
			DO UPDATE SET name = user_collections.name
			RETURNING id
		)
		INSERT INTO user_collection_books (collection_id, book_id)
		SELECT id, $2 FROM shelf
		ON CONFLICT (collection_id, book_id) DO NOTHING
	`, userID, bookID, toReadName); err != nil {
		return fmt.Errorf("add to read: %w", err)
	}
	return nil
}

// RemoveToRead — снять с полки все издания работы (как RemoveFavorite, #379).
func (s *Service) RemoveToRead(ctx context.Context, userID, bookID int64) error {
	if _, err := s.pool.Exec(ctx, `
		DELETE FROM user_collection_books cb
		USING user_collections c
		WHERE cb.collection_id = c.id AND c.user_id = $1 AND c.kind = 'to_read'
		  AND (cb.book_id = $2 OR cb.book_id IN (
		      SELECT b.id FROM books b
		      WHERE b.work_id = (SELECT work_id FROM books WHERE id = $2)))
	`, userID, bookID); err != nil {
		return fmt.Errorf("remove to read: %w", err)
	}
	return nil
}

// IsWorkToRead — любое издание работы лежит на «Хочу прочитать».
func (s *Service) IsWorkToRead(ctx context.Context, userID, workID int64) (bool, error) {
	var ok bool
	if err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1 FROM user_collection_books cb
			JOIN user_collections c ON c.id = cb.collection_id AND c.user_id = $1 AND c.kind = 'to_read'
			JOIN books b ON b.id = cb.book_id
			WHERE b.work_id = $2)`, userID, workID).Scan(&ok); err != nil {
		return false, fmt.Errorf("query work to read: %w", err)
	}
	return ok, nil
}
