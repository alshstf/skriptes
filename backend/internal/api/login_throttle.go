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
func (t *loginThrottle) over(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	w := t.hits[key]
	return w != nil && time.Now().Before(w.reset) && w.count >= t.limit
}

// fail регистрирует неудачную попытку по key (новое окно либо инкремент текущего).
func (t *loginThrottle) fail(key string) {
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

// throttleIP — IP клиента для лимитера. За Cloudflare берём CF-Connecting-IP (его
// клиент через CF подделать не может); иначе — RemoteAddr (chi.RealIP уже учёл
// X-Forwarded-For). Публичный инстанс ходит ТОЛЬКО через Cloudflare-туннель, прямого
// доступа к origin нет — поэтому CF-Connecting-IP здесь доверенный.
func throttleIP(r *http.Request) string {
	if cf := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); cf != "" {
		return cf
	}
	return clientIP(r).String()
}
