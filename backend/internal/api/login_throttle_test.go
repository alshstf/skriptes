package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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
