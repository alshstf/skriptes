package importer

import (
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPopularityTracker_MarkDedup — MarkBook накапливает уникальные book_id и
// игнорирует невалидные (0/отрицательные); dirty-set дедуплицирует.
func TestPopularityTracker_MarkDedup(t *testing.T) {
	tr := NewPopularityTracker(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	tr.MarkBook(5)
	tr.MarkBook(5) // дубль
	tr.MarkBook(7)
	tr.MarkBook(0)  // игнор
	tr.MarkBook(-1) // игнор

	tr.mu.Lock()
	n := len(tr.dirty)
	tr.mu.Unlock()
	require.Equal(t, 2, n) // только 5 и 7

	// nil-трекер безопасен.
	var nilTr *PopularityTracker
	require.NotPanics(t, func() { nilTr.MarkBook(1) })
}
