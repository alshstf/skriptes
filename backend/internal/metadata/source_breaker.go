package metadata

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/skriptes/skriptes/backend/internal/metrics"
)

// Прерыватель для внешних источников (#299). Когда источник недоступен надолго
// (на DMZ провайдер подменял DNS TMDB на 127.0.0.1), воркеры каждые 30 минут
// заново гнали по нему те же книги: сотни провальных вызовов за цикл, строка в
// логе и запись «error» в учёт на каждую. Теперь после breakerThreshold сбоев
// подряд хост ставится на паузу: запросы к нему сразу возвращают ErrSourcePaused,
// не уходя в сеть; пауза растёт вдвое при каждом новом провале до
// breakerMaxBackoff, после паузы проходит один пробный запрос. Состояние — по
// хосту (api.themoviedb.org, query.wikidata.org, ru.wikipedia.org, …): так один
// транспорт обслуживает всех провайдеров, и пауза одного источника не трогает
// остальные.

// ErrSourcePaused — источник на паузе после серии сбоев: запрос в сеть не ушёл.
// Для воркеров это не «не найдено» и не ошибка книги: книгу пропускают молча,
// без записи в учёт попыток, — её возьмут, когда источник вернётся.
var ErrSourcePaused = errors.New("external source paused after repeated failures")

const (
	breakerThreshold   = 5
	breakerBaseBackoff = 5 * time.Minute
	breakerMaxBackoff  = 6 * time.Hour
)

// SourcePausedError несёт хост и время, до которого он на паузе.
type SourcePausedError struct {
	Host  string
	Until time.Time
}

func (e *SourcePausedError) Error() string {
	return fmt.Sprintf("%s paused until %s after repeated failures", e.Host, e.Until.UTC().Format(time.RFC3339))
}

func (e *SourcePausedError) Is(target error) bool { return target == ErrSourcePaused }

type hostState struct {
	failures int           // сбоев подряд, пока хост не на паузе
	open     bool          // на паузе
	until    time.Time     // конец паузы
	backoff  time.Duration // текущая длина паузы
	probing  bool          // пробный запрос после паузы уже в пути
}

type sourceBreaker struct {
	mu    sync.Mutex
	hosts map[string]*hostState
	now   func() time.Time
}

func newSourceBreaker() *sourceBreaker {
	return &sourceBreaker{hosts: map[string]*hostState{}, now: time.Now}
}

// allow решает, пускать ли запрос к хосту; probe — это пробный запрос после паузы.
func (b *sourceBreaker) allow(host string) (ok, probe bool, until time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.state(host)
	if !st.open {
		return true, false, time.Time{}
	}
	if b.now().Before(st.until) || st.probing {
		return false, false, st.until
	}
	st.probing = true
	return true, true, time.Time{}
}

// outcome — итог запроса для прерывателя.
type outcome int

const (
	outcomeOK      outcome = iota
	outcomeFailed          // сеть, 429, 5xx
	outcomeIgnored         // запрос отменили сами (остановка, тайм-аут вызывающего) — не говорит о хосте
)

func (b *sourceBreaker) record(host string, probe bool, res outcome) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.state(host)
	if probe {
		st.probing = false
	}
	switch res {
	case outcomeIgnored:
		return
	case outcomeOK:
		if st.open {
			slog.Info("external source available again", "host", host)
		}
		st.failures, st.open, st.backoff = 0, false, 0
		return
	}
	if st.open {
		if !probe {
			return // ответ на запрос, ушедший до паузы
		}
		st.backoff = min(st.backoff*2, breakerMaxBackoff)
		st.until = b.now().Add(st.backoff)
		slog.Warn("external source still unavailable, pausing longer", "host", host, "pause", st.backoff)
		return
	}
	st.failures++
	if st.failures < breakerThreshold {
		return
	}
	st.open, st.backoff = true, breakerBaseBackoff
	st.until = b.now().Add(st.backoff)
	metrics.ExternalSourcePauses.WithLabelValues(host).Inc()
	slog.Warn("external source unavailable, pausing requests", "host", host, "failures", st.failures, "pause", st.backoff)
}

func (b *sourceBreaker) state(host string) *hostState {
	st, ok := b.hosts[host]
	if !ok {
		st = &hostState{}
		b.hosts[host] = st
	}
	return st
}

// upByHost — для skriptes_external_source_up: хост → «не на паузе сейчас». Пауза,
// срок которой вышел, уже не пауза, даже если пробный запрос ещё не уходил (#471).
func (b *sourceBreaker) upByHost() map[string]bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	up := make(map[string]bool, len(b.hosts))
	for host, st := range b.hosts {
		up[host] = !st.open || !now.Before(st.until)
	}
	return up
}

// breakerTransport — http.RoundTripper с прерывателем по хосту запроса.
type breakerTransport struct {
	b    *sourceBreaker
	base http.RoundTripper
}

func (t *breakerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	host := r.URL.Host // с портом, если он есть в адресе: у источников его нет, а тестовые серверы не мешают друг другу
	ok, probe, until := t.b.allow(host)
	if !ok {
		return nil, &SourcePausedError{Host: host, Until: until}
	}
	resp, err := t.base.RoundTrip(r)
	t.b.record(host, probe, classifyRoundTrip(r.Context(), resp, err))
	return resp, err
}

func classifyRoundTrip(ctx context.Context, resp *http.Response, err error) outcome {
	switch {
	// Отмена (остановка процесса, клиент ушёл со страницы) ничего не говорит об
	// источнике. Истёкший срок — говорит: таймаут http.Client тоже приходит сюда
	// через контекст запроса, а зависший хост — главный случай недоступности.
	case err != nil && errors.Is(ctx.Err(), context.Canceled):
		return outcomeIgnored
	case err != nil:
		return outcomeFailed
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return outcomeFailed
	}
	return outcomeOK
}

// sources — общий прерыватель процесса: все клиенты обогащения делят состояние
// хостов, поэтому пауза TMDB видна и воркеру экранизаций, и ленивому пути с карточки.
var (
	sources         = newSourceBreaker()
	sourceTransport = &breakerTransport{b: sources, base: http.DefaultTransport}
)

func init() { metrics.SetExternalSources(sources.upByHost) }

// SourceHTTPClient — http.Client для внешних источников обогащения: с таймаутом
// и прерывателем по хосту (#299).
func SourceHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: sourceTransport}
}
