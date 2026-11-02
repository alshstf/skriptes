package api

import (
	"net/http"
	"time"

	"github.com/skriptes/skriptes/backend/internal/libcheck"
)

// LibraryDeps — проверка тома с книгами (#632); Check == nil → ручка не монтируется.
type LibraryDeps struct {
	Check *libcheck.Checker
}

type libraryHealthResp struct {
	Checked   bool       `json:"checked"` // false — первой проверки ещё не было
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	Archives  int        `json:"archives"`
	Missing   int        `json:"missing"`
	Sample    []string   `json:"sample"` // первые недоступные архивы
}

// handleLibraryHealth — GET /api/admin/library: итог последней проверки тома —
// плашка в админке, когда архивы каталога не видны на диске.
func handleLibraryHealth(d LibraryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		res, at := d.Check.Last()
		out := libraryHealthResp{Archives: res.Archives, Missing: len(res.Missing), Sample: res.Sample()}
		if !at.IsZero() {
			out.Checked, out.CheckedAt = true, &at
		}
		if out.Sample == nil {
			out.Sample = []string{}
		}
		writeJSON(w, http.StatusOK, out)
	}
}
