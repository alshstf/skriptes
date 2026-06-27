package api

import (
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
