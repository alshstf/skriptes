package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"time"

	"github.com/go-chi/chi/v5/middleware"
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
	// Анти-брутфорс логина: лимит неудач на окно (IP — 5мин, email — 15мин).
	// 0 = слой выключен (см. config SKRIPTES_LOGIN_RATELIMIT_*).
	LoginRateLimitIP    int
	LoginRateLimitEmail int
	// TrustCFConnectingIP — брать IP клиента для лимита из CF-Connecting-IP. Только
	// если к бэкенду ходят исключительно через Cloudflare (SKRIPTES_TRUST_CF_CONNECTING_IP).
	TrustCFConnectingIP bool
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

// handleLogin — POST /api/auth/login. Анти-брутфорс (считаем только неудачи): по IP и
// по email, лимиты из конфига (0 = слой выключен — для инстансов за своим WAF / в
// доверенной LAN). По умолч. IP 10/5мин (одна точка долбит), email 20/15мин
// (анти-IP-ротация на аккаунт, но не запирает легитимного). th общий с OPDS Basic-auth.
func handleLogin(d AuthDeps, th *authThrottles) http.HandlerFunc {
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
		ipKey, emailKey := th.keys(r, req.Email)
		if th.over(ipKey, emailKey) {
			slog.Warn("login throttled", "via", "form", "ip", ipKey, "email", emailKey)
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
				// Для алертов (Loki/Telegram) и возможного fail2ban/CrowdSec: без этой
				// строки подбор пароля в публичном инстансе не виден вовсе.
				slog.Warn("login failed", "via", "form", "ip", ipKey, "email", emailKey)
				th.fail(ipKey, emailKey)
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

// clientIP — IP клиента: то, что положил middleware.ClientIPFromXFF (правое
// значение XFF от нашего прокси), иначе — адрес TCP-соединения (прямой вызов,
// dev). r.RemoteAddr middleware не трогает — там всегда адрес прокси.
func clientIP(r *http.Request) netip.Addr {
	if ip := middleware.GetClientIPAddr(r.Context()); ip.IsValid() {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, _ := netip.ParseAddr(host)
	return addr
}
