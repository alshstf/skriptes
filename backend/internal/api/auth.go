package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/skriptes/skriptes/backend/internal/auth"
)

// Имя cookie для сессии. HttpOnly + SameSite=Lax + (опц.) Secure.
const sessionCookieName = "skriptes_session"

// AuthDeps — зависимости auth-handlers и middleware.
type AuthDeps struct {
	Service        *auth.Service
	CookieSecure   bool   // false для пюре-HTTP dev, true в проде / за TLS
	CookieDomain   string // пустая строка = текущий host
	AllowedOrigins []string
}

// userCtxKey — ключ для хранения текущего пользователя в request context.
// Тип — приватный, чтобы исключить коллизии.
type userCtxKey struct{}

// UserFromContext извлекает текущего пользователя из контекста запроса.
// Возвращает (zero, false) если запрос неаутентифицирован.
func UserFromContext(ctx context.Context) (auth.User, bool) {
	u, ok := ctx.Value(userCtxKey{}).(auth.User)
	return u, ok
}

// loginRequest — JSON-тело POST /api/auth/login.
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// userResponse — обёртка вокруг auth.User для всех auth-эндпоинтов
// (login и me возвращают одинаковую структуру).
type userResponse struct {
	User auth.User `json:"user"`
}

func handleLogin(d AuthDeps) http.HandlerFunc {
	// Анти-брутфорс (считаем только неудачи): по IP — 10/5мин (одна точка долбит),
	// по email — 20/15мин, щедрее (анти-IP-ротация на один аккаунт, но не запирает
	// легитимного юзера). Первичный гейт для публикации — Cloudflare Access; это
	// defense-in-depth + второй слой к CF edge rate-limit (см. деплой-гайд).
	ipThrottle := newLoginThrottle(10, 5*time.Minute)
	emailThrottle := newLoginThrottle(20, 15*time.Minute)
	go ipThrottle.cleanupLoop()
	go emailThrottle.cleanupLoop()
	return func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if req.Email == "" || req.Password == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "email and password required"})
			return
		}
		ipKey := throttleIP(r)
		emailKey := strings.ToLower(strings.TrimSpace(req.Email))
		if ipThrottle.over(ipKey) || emailThrottle.over(emailKey) {
			w.Header().Set("Retry-After", "300")
			writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many attempts, try again later"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		meta := auth.SessionMetadata{IP: clientIP(r), UserAgent: r.UserAgent()}
		user, token, err := d.Service.Login(ctx, req.Email, req.Password, meta)
		if err != nil {
			if errors.Is(err, auth.ErrInvalidPassword) {
				ipThrottle.fail(ipKey)
				emailThrottle.fail(emailKey)
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid email or password"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "login failed"})
			return
		}
		setSessionCookie(w, d, token, time.Now().Add(auth.SessionTTL))
		writeJSON(w, http.StatusOK, userResponse{User: user})
	}
}

func handleLogout(d AuthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookieName)
		if err == nil && c.Value != "" {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			_ = d.Service.Logout(ctx, c.Value)
		}
		clearSessionCookie(w, d)
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleMe возвращает текущего пользователя. Аутентификация обеспечивается
// requireAuth-middleware в router.go — здесь просто читаем из контекста.
func handleMe(_ AuthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		writeJSON(w, http.StatusOK, userResponse{User: u})
	}
}

func setSessionCookie(w http.ResponseWriter, d AuthDeps, token string, expiresAt time.Time) {
	// Secure флаг управляется конфигом: true в проде / за TLS, false в чистом
	// HTTP dev. gosec G124 — false positive здесь.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is config-driven
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		Domain:   d.CookieDomain,
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   d.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(w http.ResponseWriter, d AuthDeps) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // Secure is config-driven
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		Domain:   d.CookieDomain,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   d.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
}

// clientIP делает best-effort извлечение IP клиента из запроса.
// chi.RealIP уже обрабатывает X-Forwarded-For в RemoteAddr — здесь просто
// парсим RemoteAddr в netip.Addr.
func clientIP(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, _ := netip.ParseAddr(host)
	return addr
}
