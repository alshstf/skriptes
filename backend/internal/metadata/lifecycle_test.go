package metadata

import (
	"testing"
	"time"
)

// TestLifecycle_ShutdownWaitsAndRefuses — #270: shutdown отменяет контекст,
// дожидается фоновых работ (пул закрывается только после) и новых не запускает.
func TestLifecycle_ShutdownWaitsAndRefuses(t *testing.T) {
	l := newLifecycle()
	finished := make(chan struct{})
	if !l.spawn(func() {
		<-l.ctx.Done()
		time.Sleep(50 * time.Millisecond) // дописывает после отмены
		close(finished)
	}) {
		t.Fatal("spawn до shutdown должен запускать")
	}
	if !l.shutdown(time.Second) {
		t.Fatal("shutdown не дождался работы")
	}
	select {
	case <-finished:
	default:
		t.Fatal("shutdown вернулся раньше, чем работа закончилась")
	}
	if l.spawn(func() {}) {
		t.Fatal("после shutdown новые работы не запускаются")
	}

	stuck := newLifecycle()
	stuck.spawn(func() { time.Sleep(time.Second) })
	if stuck.shutdown(20 * time.Millisecond) {
		t.Fatal("зависшая работа — shutdown по тайм-ауту возвращает false")
	}
}
