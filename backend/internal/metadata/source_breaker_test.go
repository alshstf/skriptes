package metadata

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// breakerFixture — сервер с управляемым ответом и клиент через прерыватель с
// подменённым временем.
type breakerFixture struct {
	srv    *httptest.Server
	status atomic.Int32
	hits   atomic.Int32
	now    time.Time
	b      *sourceBreaker
	client *http.Client
}

func newBreakerFixture(t *testing.T) *breakerFixture {
	t.Helper()
	f := &breakerFixture{now: time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)}
	f.status.Store(http.StatusOK)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.hits.Add(1)
		w.WriteHeader(int(f.status.Load()))
	}))
	t.Cleanup(f.srv.Close)
	f.b = newSourceBreaker()
	f.b.now = func() time.Time { return f.now }
	f.client = &http.Client{Transport: &breakerTransport{b: f.b, base: http.DefaultTransport}}
	return f
}

func (f *breakerFixture) get(t *testing.T) error {
	t.Helper()
	resp, err := f.client.Get(f.srv.URL)
	if err == nil {
		_ = resp.Body.Close()
	}
	return err
}

func TestSourceBreaker_OpensAfterThresholdAndProbes(t *testing.T) {
	f := newBreakerFixture(t)
	f.status.Store(http.StatusServiceUnavailable)
	for range breakerThreshold {
		require.NoError(t, f.get(t), "5xx — ответ, не ошибка транспорта")
	}
	require.EqualValues(t, breakerThreshold, f.hits.Load())

	// Пауза: запрос не уходит в сеть, ошибка распознаётся через url.Error.
	err := f.get(t)
	require.ErrorIs(t, err, ErrSourcePaused)
	var pe *SourcePausedError
	require.True(t, errors.As(err, &pe))
	require.Equal(t, f.now.Add(breakerBaseBackoff), pe.Until)
	require.EqualValues(t, breakerThreshold, f.hits.Load())

	// Пауза кончилась, источник всё ещё лежит: один пробный запрос, пауза вдвое.
	f.now = f.now.Add(breakerBaseBackoff)
	require.NoError(t, f.get(t))
	require.EqualValues(t, breakerThreshold+1, f.hits.Load())
	require.ErrorIs(t, f.get(t), ErrSourcePaused)
	f.now = f.now.Add(breakerBaseBackoff)
	require.ErrorIs(t, f.get(t), ErrSourcePaused, "вторая пауза — вдвое длиннее")
	f.now = f.now.Add(breakerBaseBackoff)

	// Источник ожил: проба проходит, прерыватель закрыт.
	f.status.Store(http.StatusOK)
	require.NoError(t, f.get(t))
	require.NoError(t, f.get(t))
	require.EqualValues(t, breakerThreshold+3, f.hits.Load())
}

func TestSourceBreaker_BackoffCapped(t *testing.T) {
	f := newBreakerFixture(t)
	f.status.Store(http.StatusTooManyRequests)
	for range breakerThreshold {
		require.NoError(t, f.get(t))
	}
	for range 20 { // много провальных проб подряд
		f.now = f.now.Add(breakerMaxBackoff)
		require.NoError(t, f.get(t))
	}
	require.Equal(t, breakerMaxBackoff, f.b.hosts[f.host(t)].backoff)
}

func TestSourceBreaker_SuccessResetsFailures(t *testing.T) {
	f := newBreakerFixture(t)
	for round := range 3 {
		f.status.Store(http.StatusBadGateway)
		for range breakerThreshold - 1 {
			require.NoError(t, f.get(t))
		}
		f.status.Store(http.StatusNotFound) // 404 — честный ответ источника, не сбой
		require.NoError(t, f.get(t), "раунд %d", round)
	}
	require.NoError(t, f.get(t))
}

func TestSourceBreaker_OnlyOneProbe(t *testing.T) {
	f := newBreakerFixture(t)
	f.status.Store(http.StatusInternalServerError)
	for range breakerThreshold {
		require.NoError(t, f.get(t))
	}
	f.now = f.now.Add(breakerBaseBackoff)
	host := f.host(t)
	ok, probe, _ := f.b.allow(host)
	require.True(t, ok)
	require.True(t, probe)
	ok, _, _ = f.b.allow(host)
	require.False(t, ok, "пока проба в пути, остальные ждут")
	f.b.record(host, true, outcomeOK)
	ok, probe, _ = f.b.allow(host)
	require.True(t, ok)
	require.False(t, probe)
}

func TestSourceBreaker_CanceledRequestsDoNotCount(t *testing.T) {
	f := newBreakerFixture(t)
	block := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(block); slow.Close() })
	for range breakerThreshold + 2 {
		ctx, cancel := context.WithCancel(context.Background())
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, slow.URL, nil)
		require.NoError(t, err)
		go func() { time.Sleep(10 * time.Millisecond); cancel() }()
		_, err = f.client.Do(req)
		require.Error(t, err)
		require.NotErrorIs(t, err, ErrSourcePaused)
	}
}

// host — ключ прерывателя для тестового сервера (как в RoundTrip: URL.Host).
func (f *breakerFixture) host(t *testing.T) string {
	t.Helper()
	u, err := url.Parse(f.srv.URL)
	require.NoError(t, err)
	return u.Host
}

func TestSourceBreaker_TimeoutsCount(t *testing.T) {
	f := newBreakerFixture(t)
	block := make(chan struct{})
	hang := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(block); hang.Close() })
	client := &http.Client{Timeout: 20 * time.Millisecond, Transport: f.client.Transport}
	for range breakerThreshold {
		_, err := client.Get(hang.URL)
		require.Error(t, err)
		require.NotErrorIs(t, err, ErrSourcePaused)
	}
	_, err := client.Get(hang.URL)
	require.ErrorIs(t, err, ErrSourcePaused, "зависший хост (таймауты клиента) ставится на паузу")
}

// TestSourceBreaker_UpSnapshot — #471: метрика доступности — «на паузе сейчас»,
// а не итог последней пробы. Пауза кончилась без запросов — хост доступен;
// проба провалилась — снова на паузе.
func TestSourceBreaker_UpSnapshot(t *testing.T) {
	f := newBreakerFixture(t)
	host := mustHost(t, f.srv.URL)
	require.NoError(t, f.get(t))
	require.Equal(t, map[string]bool{host: true}, f.b.upSnapshot())

	f.status.Store(http.StatusServiceUnavailable)
	for range breakerThreshold {
		require.NoError(t, f.get(t))
	}
	require.False(t, f.b.upSnapshot()[host], "пауза идёт — недоступен")

	f.now = f.now.Add(breakerBaseBackoff)
	require.True(t, f.b.upSnapshot()[host], "пауза кончилась, запросов не было — доступен (раньше 0 держался сутками)")

	require.NoError(t, f.get(t)) // проба, источник всё ещё лежит
	require.False(t, f.b.upSnapshot()[host], "провал пробы — снова пауза")
}

func mustHost(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	require.NoError(t, err)
	return u.Host
}
