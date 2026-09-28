package importer

import "testing"

// #305: после восстановления базы на пустой Meili индекс книг пуст — нужен
// полный импорт; единичные пропуски и пустая база — нет.
func TestReconcileResult_BooksNeedReimport(t *testing.T) {
	for _, tc := range []struct {
		missing, live int
		want          bool
	}{
		{missing: 469000, live: 469000, want: true},
		{missing: 50000, live: 469000, want: true},
		{missing: 3, live: 469000, want: false},
		{missing: 0, live: 0, want: false},
	} {
		r := ReconcileResult{BooksMissing: tc.missing, BooksLive: tc.live}
		if got := r.BooksNeedReimport(); got != tc.want {
			t.Errorf("missing=%d live=%d: got %v, want %v", tc.missing, tc.live, got, tc.want)
		}
	}
}
