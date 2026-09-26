package metadata

import (
	"context"
	"testing"
)

// Рантайм-тумблер TMDB: выключен → ведём себя как без провайдера
// (RecheckPosterHoles — no-op ещё до похода в БД/сеть).
func TestSetTMDBPostersEnabled_GatesRecheck(t *testing.T) {
	e := &Enricher{tmdbPosters: NewTMDBPosterProvider("k")}
	e.tmdbEnabled.Store(true)
	if !e.tmdbPostersActive() {
		t.Fatal("сконфигурирован и включён — должен быть активен")
	}
	e.SetTMDBPostersEnabled(false)
	if e.tmdbPostersActive() {
		t.Fatal("выключен тумблером — не активен")
	}
	// pool == nil: если бы гейт не сработал первым, был бы nil-deref ниже по коду.
	checked, filled, err := e.RecheckPosterHoles(context.Background(), 10)
	if err != nil || checked != 0 || filled != 0 {
		t.Fatalf("выключенный TMDB: ожидаем no-op, got (%d,%d,%v)", checked, filled, err)
	}
	// Без провайдера вовсе — тоже no-op, независимо от тумблера.
	e2 := &Enricher{}
	e2.tmdbEnabled.Store(true)
	if e2.tmdbPostersActive() {
		t.Fatal("без провайдера не активен")
	}
}
