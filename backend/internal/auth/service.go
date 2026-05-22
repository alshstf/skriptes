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
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// isUniqueViolation — true если err это PG unique_violation (23505) и
// constraint содержит подстроку constraintHint. Используется чтобы
// отличать пользовательскую ошибку «email занят» от других DB-проблем.
//
// constraintHint — кусок имени constraint'а (например "users_email").
// PG creates default name like "users_email_key" — substring-check
// устойчив к мелкой смене именования.
func isUniqueViolation(err error, constraintHint string) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	if pgErr.Code != "23505" {
		return false
	}
	return strings.Contains(pgErr.ConstraintName, constraintHint)
}

// ErrUserNotFound возвращается при попытке получить несуществующего
// пользователя (например, по email во время регистрации).
// В Login НЕ ВОЗВРАЩАЕТСЯ — там всегда ErrInvalidPassword,
// чтобы атакующий не отличал "нет такого email" от "пароль не тот".
var ErrUserNotFound = errors.New("user not found")

// ErrLastAdmin — попытка удалить или деградировать (role admin → user)
// единственного оставшегося админа. Это бы залочило юзера из админ-UI;
// recovery остаётся только через CLI skriptes-seed. Защита: handler
// возвращает 409 Conflict, фронт показывает сообщение.
var ErrLastAdmin = errors.New("cannot remove or demote the last admin")

// ErrEmailTaken — попытка установить email который уже занят другим
// пользователем (UNIQUE-нарушение в users.email). В PR создания юзера
// то же самое; в PR update делаем явный pre-check чтобы дать
// предсказуемое сообщение об ошибке.
var ErrEmailTaken = errors.New("email already taken")

// ErrPasswordTooShort — пароль меньше MinPasswordLen. Validate'м в
// CreateUser / ResetPassword / ChangePassword. Минимум — компромисс:
// не строжим, чтобы не мешать семье; но не пустые / 1-символьные.
var ErrPasswordTooShort = errors.New("password too short")

// MinPasswordLen — минимальная длина пароля.
// 8 — общепринятый baseline; bcrypt cap = 72 байта.
const MinPasswordLen = 8

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

// CreateUser создаёт пользователя. Используется для seed (skriptes-seed CLI)
// и для admin CRUD через UI (PATCH /api/admin/users).
//
// Валидация: email непустой, password ≥ MinPasswordLen символов. Возвращает
// ErrEmailTaken если UNIQUE-нарушение по users.email.
func (s *Service) CreateUser(ctx context.Context, email, displayName, password string, role Role) (User, error) {
	if email == "" {
		return User{}, errors.New("email must not be empty")
	}
	if len(password) < MinPasswordLen {
		return User{}, ErrPasswordTooShort
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
		if isUniqueViolation(err, "users_email") {
			return User{}, ErrEmailTaken
		}
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

// ── Admin user management ──────────────────────────────────────────

// ListUsers возвращает всех пользователей (без password_hash) для admin-UI.
// Сортировка по created_at asc — стабильный порядок, первый созданный
// (обычно изначальный admin) сверху.
func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, email, display_name, role, COALESCE(kindle_email::text, ''), created_at
		FROM users
		ORDER BY created_at, id
	`)
	if err != nil {
		return nil, fmt.Errorf("query users: %w", err)
	}
	defer rows.Close()
	out := make([]User, 0)
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.KindleEmail, &u.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// GetUser — отдельный SELECT по id, нужен handler'у POST /api/admin/users/{id}
// чтобы вернуть 404 для несуществующего id отдельным кодом.
func (s *Service) GetUser(ctx context.Context, id int64) (User, error) {
	var u User
	err := s.pool.QueryRow(ctx, `
		SELECT id, email, display_name, role, COALESCE(kindle_email::text, ''), created_at
		FROM users WHERE id = $1
	`, id).Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.KindleEmail, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrUserNotFound
		}
		return User{}, fmt.Errorf("get user: %w", err)
	}
	return u, nil
}

// UpdateUser — admin-эндпоинт. Меняет email / display_name / role в любых
// комбинациях (пустые значения = «не менять»).
//
// Защита от потери доступа: при попытке снять role=admin с единственного
// оставшегося админа возвращает ErrLastAdmin. ErrEmailTaken если email
// конфликтует с другим пользователем.
//
// Не верифицирует пароль вызывающего (это делает middleware requireAdmin).
func (s *Service) UpdateUser(ctx context.Context, id int64, email, displayName string, role Role) (User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Локaем строку чтобы countAdmins был консистентным относительно update'а.
	var current User
	err = tx.QueryRow(ctx, `
		SELECT id, email, display_name, role, COALESCE(kindle_email::text, ''), created_at
		FROM users WHERE id = $1 FOR UPDATE
	`, id).Scan(&current.ID, &current.Email, &current.DisplayName, &current.Role, &current.KindleEmail, &current.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrUserNotFound
		}
		return User{}, fmt.Errorf("lookup user: %w", err)
	}

	// Last-admin защита. Если был admin и становится user — проверить
	// что есть ещё хотя бы один admin.
	if string(current.Role) == string(RoleAdmin) && role != "" && string(role) != string(RoleAdmin) {
		n, err := countAdminsTx(ctx, tx)
		if err != nil {
			return User{}, err
		}
		if n <= 1 {
			return User{}, ErrLastAdmin
		}
	}

	newEmail := current.Email
	if email != "" && email != current.Email {
		newEmail = email
	}
	newDisplayName := current.DisplayName
	if displayName != "" {
		newDisplayName = displayName
	}
	newRole := current.Role
	if role != "" {
		newRole = role
	}

	var u User
	err = tx.QueryRow(ctx, `
		UPDATE users SET email = $1, display_name = $2, role = $3, updated_at = now()
		WHERE id = $4
		RETURNING id, email, display_name, role, COALESCE(kindle_email::text, ''), created_at
	`, newEmail, newDisplayName, string(newRole), id).Scan(
		&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.KindleEmail, &u.CreatedAt,
	)
	if err != nil {
		if isUniqueViolation(err, "users_email") {
			return User{}, ErrEmailTaken
		}
		return User{}, fmt.Errorf("update user: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, fmt.Errorf("commit: %w", err)
	}
	return u, nil
}

// UpdateMe — self-эндпоинт. Юзер меняет своё display_name (и/или email).
// Role менять нельзя — это admin-only. Безопасный subset UpdateUser.
func (s *Service) UpdateMe(ctx context.Context, id int64, email, displayName string) (User, error) {
	return s.UpdateUser(ctx, id, email, displayName, "")
}

// ResetPassword — admin-путь. Без верификации текущего пароля.
// Инвалидирует ВСЕ сессии этого юзера: если пароль был
// скомпрометирован, активные сессии злоумышленника тоже убиваем.
func (s *Service) ResetPassword(ctx context.Context, id int64, newPassword string) error {
	return s.setPassword(ctx, id, newPassword, "")
}

// ChangePassword — self-путь. Юзер сам меняет свой пароль с
// верификацией текущего; keepSessionToken — токен текущей сессии (если есть),
// который нужно СОХРАНИТЬ; все остальные сессии этого юзера revoke'ются.
//
// Если currentPassword не совпадает — возвращает ErrInvalidPassword.
func (s *Service) ChangePassword(ctx context.Context, id int64, currentPassword, newPassword, keepSessionToken string) error {
	// Сначала достать текущий hash и verify.
	var hash string
	err := s.pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, id).Scan(&hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUserNotFound
		}
		return fmt.Errorf("lookup user: %w", err)
	}
	if err := VerifyPassword(currentPassword, hash); err != nil {
		return err
	}
	return s.setPassword(ctx, id, newPassword, keepSessionToken)
}

// setPassword — внутренняя реализация. Хэширует пароль, обновляет
// users.password_hash, удаляет все сессии юзера КРОМЕ keepSessionToken
// (если непустой). Транзакционно.
func (s *Service) setPassword(ctx context.Context, id int64, newPassword, keepSessionToken string) error {
	if len(newPassword) < MinPasswordLen {
		return ErrPasswordTooShort
	}
	newHash, err := HashPassword(newPassword, s.bcryptCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx,
		`UPDATE users SET password_hash = $1, updated_at = now() WHERE id = $2`,
		newHash, id,
	)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrUserNotFound
	}

	// Удаляем все сессии этого юзера. Если keepSessionToken задан —
	// исключаем именно её.
	if keepSessionToken != "" {
		_, err = tx.Exec(ctx,
			`DELETE FROM sessions WHERE user_id = $1 AND token <> $2`,
			id, keepSessionToken,
		)
	} else {
		_, err = tx.Exec(ctx, `DELETE FROM sessions WHERE user_id = $1`, id)
	}
	if err != nil {
		return fmt.Errorf("revoke sessions: %w", err)
	}
	return tx.Commit(ctx)
}

// DeleteUser — admin-путь. Hard-delete; FK CASCADE чистит sessions,
// favorites, reads, kindle_targets автоматически (см. 0001_init.up.sql).
//
// Last-admin защита: возвращает ErrLastAdmin если удаляем единственного
// admin'а.
func (s *Service) DeleteUser(ctx context.Context, id int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var role string
	err = tx.QueryRow(ctx,
		`SELECT role FROM users WHERE id = $1 FOR UPDATE`, id,
	).Scan(&role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrUserNotFound
		}
		return fmt.Errorf("lookup user: %w", err)
	}
	if role == string(RoleAdmin) {
		n, err := countAdminsTx(ctx, tx)
		if err != nil {
			return err
		}
		if n <= 1 {
			return ErrLastAdmin
		}
	}
	_, err = tx.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	return tx.Commit(ctx)
}

// countAdminsTx — внутри транзакции считает админов. Используется
// last-admin защитой UpdateUser/DeleteUser.
func countAdminsTx(ctx context.Context, tx pgx.Tx) (int, error) {
	var n int
	err := tx.QueryRow(ctx,
		`SELECT COUNT(*) FROM users WHERE role = 'admin'`,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count admins: %w", err)
	}
	return n, nil
}
