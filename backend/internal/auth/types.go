package auth

import "time"

// Role — ролевая модель. admin может всё, user — только чтение/чтение книг.
// Жёстко зашитые значения (а не свободная строка) — конфайнятся CHECK
// constraint'ом в users.role.
type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

// User — публичный DTO пользователя. Никогда не содержит password_hash.
// Возвращается из Service.Login и из middleware.UserFromContext.
type User struct {
	ID          int64     `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Role        Role      `json:"role"`
	KindleEmail string    `json:"kindle_email,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// Session описывает активную сессию пользователя в БД.
type Session struct {
	Token     string    // непрозрачный 256-битный token
	UserID    int64     // FK на users.id
	ExpiresAt time.Time // через сколько считать сессию истёкшей
}
