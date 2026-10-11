package metadata

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestDailyQuota — общая дневная квота хоста (#501): N запросов за UTC-сутки,
// счётчик в БД переживает рестарт (новый объект видит выбранное), новые сутки —
// с нуля, лимит 0 — без ограничения.
func TestDailyQuota(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	day := time.Date(2026, 10, 11, 10, 0, 0, 0, time.UTC)
	q := NewDailyQuota(pool, "gb.test", 3, quiet)
	q.now = func() time.Time { return day }
	for i := 0; i < 3; i++ {
		ok, _ := q.Take(ctx)
		require.True(t, ok, "запрос %d в пределах лимита", i+1)
	}
	ok, until := q.Take(ctx)
	require.False(t, ok, "4-й запрос — за лимитом")
	require.Equal(t, time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC), until, "до полуночи UTC")
	require.True(t, q.Exhausted(ctx))
	require.Equal(t, 3, q.Used(ctx))

	// Рестарт: новый объект с тем же хостом видит выбранную квоту.
	restarted := NewDailyQuota(pool, "gb.test", 3, quiet)
	restarted.now = func() time.Time { return day }
	require.True(t, restarted.Exhausted(ctx))
	ok, _ = restarted.Take(ctx)
	require.False(t, ok)

	// Лимит подняли из админки — ещё можно.
	restarted.SetLimit(5)
	ok, _ = restarted.Take(ctx)
	require.True(t, ok)
	require.Equal(t, 4, restarted.Used(ctx))

	// Новые сутки — счётчик с нуля.
	next := NewDailyQuota(pool, "gb.test", 3, quiet)
	next.now = func() time.Time { return day.Add(24 * time.Hour) }
	require.False(t, next.Exhausted(ctx))
	ok, _ = next.Take(ctx)
	require.True(t, ok)
	require.Equal(t, 1, next.Used(ctx))

	// Лимит 0 — без ограничения, в БД не ходит.
	unlimited := NewDailyQuota(nil, "gb.test", 0, quiet)
	for i := 0; i < 5; i++ {
		ok, _ := unlimited.Take(ctx)
		require.True(t, ok)
	}
}

// TestBreakerTransport_DailyQuota — квота в общем транспорте: выбранная — запрос
// не уходит, ошибка — ErrSourcePaused с Quota; прерыватель на паузу не встаёт
// (квота — не сбой источника), другие хосты не задеты.
func TestBreakerTransport_DailyQuota(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		_, _ = w.Write([]byte("{}"))
	}))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	require.NoError(t, err)

	SetHostDailyQuota(u.Host, NewDailyQuota(pool, u.Host, 2, quiet))
	t.Cleanup(func() { SetHostDailyQuota(u.Host, nil) })

	client := SourceHTTPClient(5 * time.Second)
	get := func() error {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
		return err
	}
	require.NoError(t, get())
	require.NoError(t, get())
	err = get()
	require.ErrorIs(t, err, ErrSourcePaused, "третий запрос — за квотой")
	var pe *SourcePausedError
	require.True(t, errors.As(err, &pe))
	require.True(t, pe.Quota)
	require.Equal(t, 2, hits, "запрос за квотой в сеть не ушёл")
	require.True(t, sources.upByHost()[u.Host], "квота — не пауза прерывателя")
}
