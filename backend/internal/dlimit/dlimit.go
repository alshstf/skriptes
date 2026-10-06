// Package dlimit — лимит скачиваний на пользователя (#389, B1): одновременных
// скачиваний (конвертация fb2 → epub/kepub держит процессор) и числа за окно
// (выкачка библиотеки целиком через OPDS снаружи). Общий для OPDS и веб-скачивания.
package dlimit

import (
	"sync"
	"time"
)

// Значения по умолчанию: семья читает по книге, серия — десятки книг подряд;
// сотни за минуты — уже выкачка.
const (
	DefaultConcurrent = 2
	DefaultPerWindow  = 60
	DefaultWindow     = 10 * time.Minute
)

// Limiter — состояние лимитов в памяти процесса.
type Limiter struct {
	mu         sync.Mutex
	concurrent int
	perWindow  int
	window     time.Duration
	active     map[int64]int
	recent     map[int64][]time.Time
	now        func() time.Time
}

// New — лимитер: не больше concurrent одновременно и perWindow за window.
func New(concurrent, perWindow int, window time.Duration) *Limiter {
	return &Limiter{
		concurrent: concurrent, perWindow: perWindow, window: window,
		active: map[int64]int{}, recent: map[int64][]time.Time{}, now: time.Now,
	}
}

// NewDefault — лимитер со значениями по умолчанию.
func NewDefault() *Limiter { return New(DefaultConcurrent, DefaultPerWindow, DefaultWindow) }

// Acquire занимает место под скачивание. ok=false — лимит исчерпан, retryAfter —
// через сколько повторить. Иначе release обязателен по окончании отдачи.
func (l *Limiter) Acquire(userID int64) (release func(), retryAfter time.Duration, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	cut := now.Add(-l.window)
	times := l.recent[userID]
	i := 0
	for i < len(times) && !times[i].After(cut) {
		i++
	}
	times = times[i:]
	if len(times) >= l.perWindow {
		l.recent[userID] = times
		return nil, times[0].Add(l.window).Sub(now), false
	}
	if l.active[userID] >= l.concurrent {
		l.recent[userID] = times
		return nil, 5 * time.Second, false
	}
	l.recent[userID] = append(times, now)
	l.active[userID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if l.active[userID]--; l.active[userID] <= 0 {
				delete(l.active, userID)
			}
		})
	}, 0, true
}
