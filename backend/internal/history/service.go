// Package history — учёт активности пользователя (views, reads, favorites).
//
// Используется как:
//   - Источник данных для персонализированного re-ranking (PR4): bonus к
//     score книги если автор/серия уже встречались пользователю.
//   - Источник данных для UI: список избранного, "недавно открытые",
//     статистика чтения.
//
// Запись событий идёт fire-and-forget из HTTP-handler'ов (см. internal/api):
// ошибка записи не должна ломать основной запрос (загрузку карточки или
// скачивание книги), поэтому ошибки только логируются.
package history

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound — для get-операций.
var ErrNotFound = errors.New("not found")

// Service — единая точка для всех операций с историей.
// Все методы потокобезопасны (pgxpool сам управляет конкурентным доступом).
type Service struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// RecordView — фиксирует факт открытия карточки книги пользователем.
//
// Вставка идёт без upsert: views — лог-таблица (timeseries), несколько
// записей за день для одной (user, book) пары — это нормально и даёт
// корректную статистику "сколько раз открывали".
func (s *Service) RecordView(ctx context.Context, userID, bookID int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO views (user_id, book_id) VALUES ($1, $2)
	`, userID, bookID)
	if err != nil {
		return fmt.Errorf("insert view: %w", err)
	}
	return nil
}

// RecordRead — фиксирует факт скачивания/чтения книги.
//
// reads имеет PRIMARY KEY (user_id, book_id) и хранит "последнее
// взаимодействие" — обновляем updated_at при повторном скачивании,
// last_pos оставляем как есть (потом will be updated by reader).
func (s *Service) RecordRead(ctx context.Context, userID, bookID int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO reads (user_id, book_id, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (user_id, book_id)
		DO UPDATE SET updated_at = now()
	`, userID, bookID)
	if err != nil {
		return fmt.Errorf("upsert read: %w", err)
	}
	return nil
}

// AddFavorite — добавить книгу в избранное.
// Идемпотентна: повторный вызов не падает и не меняет added_at.
func (s *Service) AddFavorite(ctx context.Context, userID, bookID int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO favorites (user_id, book_id) VALUES ($1, $2)
		ON CONFLICT (user_id, book_id) DO NOTHING
	`, userID, bookID)
	if err != nil {
		return fmt.Errorf("insert favorite: %w", err)
	}
	return nil
}

// RemoveFavorite — убрать из избранного. Идемпотентна.
func (s *Service) RemoveFavorite(ctx context.Context, userID, bookID int64) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM favorites WHERE user_id = $1 AND book_id = $2
	`, userID, bookID)
	if err != nil {
		return fmt.Errorf("delete favorite: %w", err)
	}
	return nil
}

// IsFavorite — true если пользователь добавил книгу в избранное.
func (s *Service) IsFavorite(ctx context.Context, userID, bookID int64) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM favorites WHERE user_id = $1 AND book_id = $2)
	`, userID, bookID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("query favorite: %w", err)
	}
	return exists, nil
}

// ListFavorites — последние limit книг в избранном пользователя.
// Возвращает ID + минимальные поля для рендера списка; полную карточку
// фронт получит отдельным запросом если нужно.
//
// Скрываем deleted-книги: если книгу убрали из коллекции (DEL=1), она
// не должна болтаться в "избранном" как зомби.
func (s *Service) ListFavorites(ctx context.Context, userID int64, limit, offset int) ([]FavoriteItem, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.pool.Query(ctx, `
		SELECT b.id, b.title, b.lang, b.lib_id, f.added_at,
		       COALESCE(
		           array_agg(DISTINCT TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name))) FILTER (WHERE a.id IS NOT NULL),
		           ARRAY[]::text[]
		       ),
		       ser.title
		FROM favorites f
		JOIN books b ON b.id = f.book_id AND b.deleted = false
		LEFT JOIN book_authors ba ON ba.book_id = b.id
		LEFT JOIN authors a ON a.id = ba.author_id
		LEFT JOIN series ser ON ser.id = b.series_id
		WHERE f.user_id = $1
		GROUP BY b.id, b.title, b.lang, b.lib_id, f.added_at, ser.title
		ORDER BY f.added_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query favorites: %w", err)
	}
	defer rows.Close()
	out := make([]FavoriteItem, 0)
	for rows.Next() {
		var (
			it     FavoriteItem
			lang   *string
			series *string
		)
		if err := rows.Scan(&it.ID, &it.Title, &lang, &it.LibID, &it.AddedAt, &it.Authors, &series); err != nil {
			return nil, err
		}
		if lang != nil {
			it.Lang = *lang
		}
		if series != nil {
			it.Series = *series
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// RecentViews — последние просмотры пользователя (для "недавно открытые").
// Возвращает по 1 записи на (book_id) — последний viewed_at.
func (s *Service) RecentViews(ctx context.Context, userID int64, limit int) ([]ViewedItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		SELECT b.id, b.title, MAX(v.viewed_at) AS last_viewed,
		       COALESCE(
		           array_agg(DISTINCT TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name))) FILTER (WHERE a.id IS NOT NULL),
		           ARRAY[]::text[]
		       )
		FROM views v
		JOIN books b ON b.id = v.book_id AND b.deleted = false
		LEFT JOIN book_authors ba ON ba.book_id = b.id
		LEFT JOIN authors a ON a.id = ba.author_id
		WHERE v.user_id = $1
		GROUP BY b.id, b.title
		ORDER BY last_viewed DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent views: %w", err)
	}
	defer rows.Close()
	out := make([]ViewedItem, 0)
	for rows.Next() {
		var it ViewedItem
		if err := rows.Scan(&it.ID, &it.Title, &it.LastViewedAt, &it.Authors); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// FavoritesCount — количество избранного у пользователя. Нужно для
// пагинации в UI; считается отдельным быстрым запросом, чтобы не
// тащить через GROUP BY в основной выборке.
func (s *Service) FavoritesCount(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM favorites f
		JOIN books b ON b.id = f.book_id AND b.deleted = false
		WHERE f.user_id = $1
	`, userID).Scan(&n)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("count favorites: %w", err)
	}
	return n, nil
}
