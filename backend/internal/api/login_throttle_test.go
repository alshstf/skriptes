package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/require"
)

// TestLoginThrottle — считает неудачи, блокирует по достижении лимита, изолирует
// ключи, сбрасывается по истечении окна.
func TestLoginThrottle(t *testing.T) {
	tr := newLoginThrottle(3, 40*time.Millisecond)
	const k = "1.2.3.4"

	require.False(t, tr.over(k)) // чисто
	tr.fail(k)
	tr.fail(k)
	require.False(t, tr.over(k)) // 2 < 3 — ещё пускаем
	tr.fail(k)
	require.True(t, tr.over(k))        // 3 >= 3 — блок
	require.False(t, tr.over("other")) // другой ключ не задет

	time.Sleep(55 * time.Millisecond)
	require.False(t, tr.over(k)) // окно протухло → снова пускаем
	tr.cleanup()                 // не паникует на пустом/протухшем
}

// TestThrottleIP_CFHeader — CF-Connecting-IP учитывается ТОЛЬКО при trustCF: без
// Cloudflare этот заголовок ставит сам клиент, и доверие к нему = обход лимита по IP.
func TestThrottleIP_CFHeader(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	r.RemoteAddr = "203.0.113.7:51234"
	r.Header.Set("CF-Connecting-IP", "198.51.100.1")

	require.Equal(t, "203.0.113.7", throttleIP(r, false)) // заголовок игнорируется
	require.Equal(t, "198.51.100.1", throttleIP(r, true)) // за Cloudflare — берём его

	r.Header.Del("CF-Connecting-IP")
	require.Equal(t, "203.0.113.7", throttleIP(r, true)) // нет заголовка → RemoteAddr
}

// TestClientIP_BehindProxy — за нашим прокси IP клиента = правое значение XFF
// (его выставляет Caddy); True-Client-IP/X-Real-IP и левые значения XFF от клиента
// игнорируются (раньше chi.RealIP им верил — GO-2026-5774/5775/5777); без XFF —
// адрес соединения.
func TestClientIP_BehindProxy(t *testing.T) {
	var got string
	h := middleware.ClientIPFromXFF()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = clientIP(r).String()
	}))

	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	r.RemoteAddr = "172.18.0.5:40000" // Caddy в docker-сети
	r.Header.Set("True-Client-IP", "6.6.6.6")
	r.Header.Set("X-Real-IP", "7.7.7.7")
	r.Header.Set("X-Forwarded-For", "9.9.9.9, 203.0.113.7") // 9.9.9.9 — подделка клиента
	h.ServeHTTP(httptest.NewRecorder(), r)
	require.Equal(t, "203.0.113.7", got)

	r = httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	r.RemoteAddr = "192.0.2.10:5555"
	h.ServeHTTP(httptest.NewRecorder(), r)
	require.Equal(t, "192.0.2.10", got, "без XFF — адрес соединения")
}

// TestLoginThrottle_Disabled — limit<=0 полностью выключает слой; nil-safe.
func TestLoginThrottle_Disabled(t *testing.T) {
	tr := newLoginThrottle(0, time.Minute)
	const k = "1.2.3.4"
	for i := 0; i < 100; i++ {
		tr.fail(k)
	}
	require.False(t, tr.over(k)) // лимит 0 → никогда не блокирует

	var nilTr *loginThrottle
	require.False(t, nilTr.over(k))
	require.NotPanics(t, func() { nilTr.fail(k) })
}
