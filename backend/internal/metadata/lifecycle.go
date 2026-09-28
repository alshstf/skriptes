package metadata

import (
	"context"
	"sync"
	"time"
)

// Фоновые работы процесса — воркеры обогащения и группировки, их разовые
// проходы, ленивое обогащение из HTTP-ручек, разовые шаги старта и импорт —
// живут от общего контекста и учитываются здесь. При остановке main зовёт
// Shutdown ДО закрытия пула PG: иначе воркеры, не зная об остановке, писали в
// закрытый пул и сыпали WARN «closed pool» на каждом деплое (#270).
type lifecycle struct {
	ctx    context.Context
	cancel context.CancelFunc

	mu      sync.Mutex
	stopped bool
	wg      sync.WaitGroup
}

func newLifecycle() *lifecycle {
	ctx, cancel := context.WithCancel(context.Background())
	return &lifecycle{ctx: ctx, cancel: cancel}
}

// spawn запускает f в горутине, которую дождётся shutdown. После shutdown
// ничего не запускает (false).
func (l *lifecycle) spawn(f func()) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stopped {
		return false
	}
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		f()
	}()
	return true
}

func (l *lifecycle) shutdown(timeout time.Duration) bool {
	l.mu.Lock()
	l.stopped = true
	l.mu.Unlock()
	l.cancel()
	done := make(chan struct{})
	go func() {
		l.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

var background = newLifecycle()

// workersCtx — контекст фоновых работ пакета; его отменяет Shutdown.
var workersCtx = background.ctx

// spawn — фоновая горутина пакета, которую дождётся Shutdown (после него — не
// запускается).
func spawn(f func()) { background.spawn(f) }

// Go запускает фоновую работу с контекстом, который отменит Shutdown.
func Go(f func(ctx context.Context)) {
	background.spawn(func() { f(background.ctx) })
}

// Stopping — идёт остановка процесса (Shutdown уже вызван).
func Stopping() bool { return background.ctx.Err() != nil }

// Shutdown отменяет все фоновые работы и ждёт их не дольше timeout.
// false — кто-то не успел (main всё равно закроет пул: процесс завершается).
func Shutdown(timeout time.Duration) bool { return background.shutdown(timeout) }
