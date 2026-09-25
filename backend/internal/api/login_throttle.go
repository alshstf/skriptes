package api

import (
	"net/http"
	"strings"
	"sync"
	"time"
)

// loginThrottle — мягкий анти-брутфорс логина: фиксированное окно, считает ТОЛЬКО
// неудачные попытки (успешный логин лимит не тратит — легитимный юзер не упрётся).
// In-memory, периодическая чистка протухших окон. Используется по двум ключам
// (IP и email) — отдельными инстансами с разными лимитами.
type loginThrottle struct {
	mu     sync.Mutex
	hits   map[string]*hitWindow
	limit  int
	window time.Duration
}

type hitWindow struct {
	count int
	reset time.Time
}

func newLoginThrottle(limit int, window time.Duration) *loginThrottle {
	return &loginThrottle{hits: make(map[string]*hitWindow), limit: limit, window: window}
}

// over сообщает, что по key уже исчерпан лимит неудач в текущем окне (read-only —
// проверяем ДО попытки логина, чтобы не тратить bcrypt на заблокированный ключ).
// limit <= 0 → слой выключен (всегда false).
func (t *loginThrottle) over(key string) bool {
	if t == nil || t.limit <= 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	w := t.hits[key]
	return w != nil && time.Now().Before(w.reset) && w.count >= t.limit
}

// fail регистрирует неудачную попытку по key (новое окно либо инкремент текущего).
// limit <= 0 → no-op.
func (t *loginThrottle) fail(key string) {
	if t == nil || t.limit <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	if w := t.hits[key]; w != nil && now.Before(w.reset) {
		w.count++
		return
	}
	t.hits[key] = &hitWindow{count: 1, reset: now.Add(t.window)}
}

// cleanup удаляет протухшие окна (ограничивает рост карты).
func (t *loginThrottle) cleanup() {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	for k, w := range t.hits {
		if now.After(w.reset) {
			delete(t.hits, k)
		}
	}
}

func (t *loginThrottle) cleanupLoop() {
	tick := time.NewTicker(t.window)
	defer tick.Stop()
	for range tick.C {
		t.cleanup()
	}
}

// authThrottles — общие лимитеры неудачных входов по IP и email. Один экземпляр на
// роутер: форма логина и OPDS Basic-auth тратят ОДИН бюджет, иначе OPDS — обходной
// путь перебора пароля без лимита (каждая попытка — ещё и bcrypt на сервере).
type authThrottles struct {
	ip      *loginThrottle
	email   *loginThrottle
	trustCF bool
}

func newAuthThrottles(d AuthDeps) *authThrottles {
	t := &authThrottles{
		ip:      newLoginThrottle(d.LoginRateLimitIP, 5*time.Minute),
		email:   newLoginThrottle(d.LoginRateLimitEmail, 15*time.Minute),
		trustCF: d.TrustCFConnectingIP,
	}
	if d.LoginRateLimitIP > 0 {
		go t.ip.cleanupLoop()
	}
	if d.LoginRateLimitEmail > 0 {
		go t.email.cleanupLoop()
	}
	return t
}

// keys — ключи лимитеров для попытки входа с данным email.
func (t *authThrottles) keys(r *http.Request, email string) (ipKey, emailKey string) {
	return throttleIP(r, t.trustCF), strings.ToLower(strings.TrimSpace(email))
}

func (t *authThrottles) over(ipKey, emailKey string) bool {
	return t.ip.over(ipKey) || t.email.over(emailKey)
}

func (t *authThrottles) fail(ipKey, emailKey string) {
	t.ip.fail(ipKey)
	t.email.fail(emailKey)
}

// throttleIP — IP клиента для лимитера. CF-Connecting-IP берём ТОЛЬКО при
// trustCF (SKRIPTES_TRUST_CF_CONNECTING_IP): за Cloudflare его ставит край и клиент
// подделать не может, а без Cloudflare это обычный заголовок — клиент подставит
// любой и обойдёт лимит по IP. Иначе — RemoteAddr (chi.RealIP уже учёл
// X-Forwarded-For от reverse-proxy).
func throttleIP(r *http.Request, trustCF bool) string {
	if trustCF {
		if cf := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cf != "" {
			return cf
		}
	}
	return clientIP(r).String()
}
