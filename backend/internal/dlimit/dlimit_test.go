package dlimit

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLimiter(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	l := New(2, 3, 10*time.Minute)
	l.now = func() time.Time { return now }

	r1, _, ok := l.Acquire(1)
	require.True(t, ok)
	r2, _, ok := l.Acquire(1)
	require.True(t, ok)
	_, wait, ok := l.Acquire(1)
	require.False(t, ok, "два одновременных — третьему ждать")
	require.Equal(t, 5*time.Second, wait)
	_, _, ok = l.Acquire(2)
	require.True(t, ok, "у другого пользователя свой лимит")

	r1()
	r1() // повторный release не освобождает лишнего
	now = now.Add(time.Minute)
	r3, _, ok := l.Acquire(1)
	require.True(t, ok)
	r2()
	r3()

	now = now.Add(time.Minute)
	_, wait, ok = l.Acquire(1)
	require.False(t, ok, "три за окно — четвёртое ждёт")
	require.Equal(t, 8*time.Minute, wait, "до выхода первого скачивания из окна")

	now = now.Add(8 * time.Minute)
	_, _, ok = l.Acquire(1)
	require.True(t, ok, "первое вышло из окна")
}
