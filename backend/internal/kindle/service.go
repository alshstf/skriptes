// Package kindle управляет списком Kindle-адресатов пользователя
// и реализует Send-to-Kindle поверх internal/email + internal/converter.
package kindle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound — запрошенный target не существует или принадлежит
// другому пользователю.
var ErrNotFound = errors.New("kindle target not found")

// ErrDuplicate — попытка добавить email, который уже есть у этого user.
var ErrDuplicate = errors.New("kindle target already exists")

// ErrInvalidEmail — поле email не похоже на kindle.com адрес.
// Amazon Kindle требует адрес вида name@kindle.com; мы это не
// валидируем строго (могут быть @free.kindle.com и т.д.), но
// проверяем минимально что есть @ и domain.
var ErrInvalidEmail = errors.New("invalid kindle email")

// Target — одна запись из kindle_targets.
type Target struct {
	ID        int64     `json:"id"`
	Label     string    `json:"label"`
	Email     string    `json:"email"`
	CreatedAt time.Time `json:"created_at"`
}

// Service — read+write по таблице kindle_targets.
type Service struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// List возвращает все targets пользователя, упорядоченные по дате
// создания (старые сверху — стабильный порядок для UI).
func (s *Service) List(ctx context.Context, userID int64) ([]Target, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, label, email, created_at
		FROM kindle_targets
		WHERE user_id = $1
		ORDER BY created_at, id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("query targets: %w", err)
	}
	defer rows.Close()
	out := make([]Target, 0)
	for rows.Next() {
		var t Target
		if err := rows.Scan(&t.ID, &t.Label, &t.Email, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Get — single target по id с проверкой принадлежности user'у.
func (s *Service) Get(ctx context.Context, userID, id int64) (Target, error) {
	var t Target
	err := s.pool.QueryRow(ctx, `
		SELECT id, label, email, created_at
		FROM kindle_targets
		WHERE user_id = $1 AND id = $2
	`, userID, id).Scan(&t.ID, &t.Label, &t.Email, &t.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Target{}, ErrNotFound
		}
		return Target{}, fmt.Errorf("query target: %w", err)
	}
	return t, nil
}

// Add создаёт новую запись. Email/Label обрезаем по пробелам, базовая
// валидация email — есть @ и часть после.
func (s *Service) Add(ctx context.Context, userID int64, label, email string) (Target, error) {
	label = strings.TrimSpace(label)
	email = strings.TrimSpace(email)
	if !isPlausibleEmail(email) {
		return Target{}, ErrInvalidEmail
	}
	if label == "" {
		label = "Kindle"
	}

	var t Target
	err := s.pool.QueryRow(ctx, `
		INSERT INTO kindle_targets (user_id, label, email)
		VALUES ($1, $2, $3)
		ON CONFLICT (user_id, email) DO NOTHING
		RETURNING id, label, email, created_at
	`, userID, label, email).Scan(&t.ID, &t.Label, &t.Email, &t.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Target{}, ErrDuplicate
		}
		return Target{}, fmt.Errorf("insert target: %w", err)
	}
	return t, nil
}

// Update меняет label и email существующего target'а. Возвращает
// ErrNotFound если такого target'а у пользователя нет.
func (s *Service) Update(ctx context.Context, userID, id int64, label, email string) (Target, error) {
	label = strings.TrimSpace(label)
	email = strings.TrimSpace(email)
	if !isPlausibleEmail(email) {
		return Target{}, ErrInvalidEmail
	}
	if label == "" {
		label = "Kindle"
	}
	var t Target
	err := s.pool.QueryRow(ctx, `
		UPDATE kindle_targets
		SET label = $1, email = $2
		WHERE user_id = $3 AND id = $4
		RETURNING id, label, email, created_at
	`, label, email, userID, id).Scan(&t.ID, &t.Label, &t.Email, &t.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Target{}, ErrNotFound
		}
		// UNIQUE constraint violation для (user_id, email) — другой target
		// уже имеет такой адрес.
		if strings.Contains(err.Error(), "kindle_targets_user_id_email_key") {
			return Target{}, ErrDuplicate
		}
		return Target{}, fmt.Errorf("update target: %w", err)
	}
	return t, nil
}

// Delete удаляет запись. Возвращает ErrNotFound если такого id нет
// (или принадлежит другому пользователю).
func (s *Service) Delete(ctx context.Context, userID, id int64) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM kindle_targets WHERE user_id = $1 AND id = $2`,
		userID, id,
	)
	if err != nil {
		return fmt.Errorf("delete target: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// isPlausibleEmail — минимальная проверка. Не RFC 5321 — просто чтобы
// отсеять очевидно невалидные строки (без @ или без domain). Строгая
// валидация здесь была бы избыточна: ошибки доставки всё равно
// возвращаются от SMTP.
func isPlausibleEmail(s string) bool {
	at := strings.IndexByte(s, '@')
	if at <= 0 || at == len(s)-1 {
		return false
	}
	if strings.Contains(s[at+1:], "@") {
		return false
	}
	if !strings.Contains(s[at+1:], ".") {
		return false
	}
	return true
}
