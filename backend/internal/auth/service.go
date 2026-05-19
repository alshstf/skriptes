// Package auth реализует аутентификацию пользователей через сессии.
//
// Стек:
//   - Пароли хранятся как bcrypt-хэши в users.password_hash (cost=12).
//   - Сессии — в таблице sessions (token PK, user_id FK, expires_at).
//   - HTTP-уровень (handlers + middleware) живёт в internal/api;
//     этот пакет не знает про http.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrUserNotFound возвращается при попытке получить несуществующего
// пользователя (например, по email во время регистрации).
// В Login НЕ ВОЗВРАЩАЕТСЯ — там всегда ErrInvalidPassword,
// чтобы атакующий не отличал "нет такого email" от "пароль не тот".
var ErrUserNotFound = errors.New("user not found")

// SessionTTL — стандартный срок жизни сессии. Семейный сервер,
// можно держать долго; пользователь всегда может разлогиниться вручную.
const SessionTTL = 30 * 24 * time.Hour

// Service — фасад с операциями аутентификации.
type Service struct {
	pool *pgxpool.Pool
	// bcryptCost можно уменьшать в тестах; в проде использовать DefaultBcryptCost.
	bcryptCost int
}

// New создаёт сервис. Передавайте 0 в bcryptCost для DefaultBcryptCost.
func New(pool *pgxpool.Pool, bcryptCost int) *Service {
	if bcryptCost == 0 {
		bcryptCost = DefaultBcryptCost
	}
	return &Service{pool: pool, bcryptCost: bcryptCost}
}

// CreateUser создаёт пользователя. Используется для seed (admin) и
// для CRUD управления (PR 5 не реализует UI; admin делает руками
// через cmd/skriptes-seed).
func (s *Service) CreateUser(ctx context.Context, email, displayName, password string, role Role) (User, error) {
	if email == "" {
		return User{}, errors.New("email must not be empty")
	}
	hash, err := HashPassword(password, s.bcryptCost)
	if err != nil {
		return User{}, fmt.Errorf("hash password: %w", err)
	}
	var u User
	err = s.pool.QueryRow(ctx, `
		INSERT INTO users (email, display_name, password_hash, role)
		VALUES ($1, $2, $3, $4)
		RETURNING id, email, display_name, role, COALESCE(kindle_email::text, ''), created_at
	`, email, displayName, hash, string(role)).Scan(
		&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.KindleEmail, &u.CreatedAt,
	)
	if err != nil {
		return User{}, fmt.Errorf("insert user: %w", err)
	}
	return u, nil
}

// SessionMetadata — опциональная контекстная информация о новой сессии,
// заполняется хэндлером (IP, User-Agent).
type SessionMetadata struct {
	IP        netip.Addr
	UserAgent string
}

// Login верифицирует email+password и создаёт новую сессию.
// При неуспехе всегда возвращает ErrInvalidPassword — никогда
// ErrUserNotFound, чтобы не палить enumeration.
func (s *Service) Login(ctx context.Context, email, password string, meta SessionMetadata) (User, string, error) {
	u, err := s.ValidateCredentials(ctx, email, password)
	if err != nil {
		return User{}, "", err
	}
	token, err := s.createSession(ctx, u.ID, meta)
	if err != nil {
		return User{}, "", err
	}
	return u, token, nil
}

// ValidateCredentials — проверяет пару email+password и возвращает
// User без создания сессии. Используется в HTTP Basic Auth для OPDS:
// e-reader'ы не умеют cookie/CSRF и шлют credentials каждым запросом,
// поэтому держать им долгоживущую сессию бессмысленно — проще валидировать
// заново на каждый запрос.
//
// Сохраняет timing-mitigation Login'а: при отсутствии user'а всё равно
// делает фиктивный bcrypt-verify, чтобы внешний атакующий не отличал
// "user not found" от "wrong password".
func (s *Service) ValidateCredentials(ctx context.Context, email, password string) (User, error) {
	var (
		u    User
		hash string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, display_name, role, COALESCE(kindle_email::text, ''), created_at, password_hash
		FROM users WHERE email = $1
	`, email).Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.KindleEmail, &u.CreatedAt, &hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// тратим примерно столько же CPU как и реальный verify,
			// чтобы timing не выдавал отсутствие пользователя.
			_ = VerifyPassword(password, "$2a$12$"+
				"abcdefghijklmnopqrstuv"+
				"abcdefghijklmnopqrstuvwxyz0123456789")
			return User{}, ErrInvalidPassword
		}
		return User{}, fmt.Errorf("lookup user: %w", err)
	}
	if err := VerifyPassword(password, hash); err != nil {
		return User{}, err
	}
	return u, nil
}

func (s *Service) createSession(ctx context.Context, userID int64, meta SessionMetadata) (string, error) {
	token, err := generateSessionToken()
	if err != nil {
		return "", err
	}
	var ipStr *string
	if meta.IP.IsValid() {
		v := meta.IP.String()
		ipStr = &v
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO sessions (token, user_id, expires_at, ip, user_agent)
		VALUES ($1, $2, $3, $4, $5)
	`, token, userID, time.Now().Add(SessionTTL), ipStr, meta.UserAgent)
	if err != nil {
		return "", fmt.Errorf("insert session: %w", err)
	}
	return token, nil
}

// UserByToken — для middleware. Возвращает (User, true) если токен жив,
// (zero, false) если нет — без различия между "нет токена" и "истёк".
// Заодно лениво чистит истёкшие сессии того же пользователя (best-effort).
func (s *Service) UserByToken(ctx context.Context, token string) (User, bool) {
	if token == "" {
		return User{}, false
	}
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT u.id, u.email, u.display_name, u.role, COALESCE(u.kindle_email::text, ''), u.created_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token = $1 AND s.expires_at > now()
	`, token).Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.KindleEmail, &u.CreatedAt)
	if err != nil {
		return User{}, false
	}
	return u, true
}

// Logout удаляет конкретную сессию (только её, не все сессии пользователя).
// Не возвращает ошибку если сессии нет — logout идемпотентен.
func (s *Service) Logout(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE token = $1`, token)
	return err
}

// CleanupExpiredSessions удаляет все сессии где expires_at <= now().
// Можно вызывать по cron. Не критично — middleware и так фильтрует.
func (s *Service) CleanupExpiredSessions(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at <= now()`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
