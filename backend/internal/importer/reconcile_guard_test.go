package importer

import "testing"

// Сверка не должна стирать поиск из-за сбоя базы или массового расхождения.
func TestGuardRemoval(t *testing.T) {
	for _, c := range []struct {
		extra, indexed, live int
		ok                   bool
	}{
		{0, 100, 0, true},    // нечего удалять
		{5, 100, 95, true},   // обычный мусор
		{5, 100, 0, false},   // PG пуст — сбой, не мусор
		{60, 100, 40, false}, // больше половины индекса — подозрительно
	} {
		err := guardRemoval("works", c.extra, c.indexed, c.live)
		if (err == nil) != c.ok {
			t.Errorf("guardRemoval(%d,%d,%d) err=%v, want ok=%v", c.extra, c.indexed, c.live, err, c.ok)
		}
	}
}
