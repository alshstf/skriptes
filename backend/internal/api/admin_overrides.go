package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/skriptes/skriptes/backend/internal/metadata"
)

// handleSetOverride — POST /api/admin/overrides. Ручная правка поля книги/работы
// (только админ; гейт requireAdmin на группе). Body:
// {"target_kind":"book","target_id":123,"field":"edition_year","value":{"v":2018}}.
func handleSetOverride(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Overrides == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "overrides disabled"})
			return
		}
		var body struct {
			TargetKind string          `json:"target_kind"`
			TargetID   int64           `json:"target_id"`
			Field      string          `json:"field"`
			Value      json.RawMessage `json:"value"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&body); err != nil ||
			body.TargetID == 0 || body.Field == "" || len(body.Value) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "target_id, field, value required"})
			return
		}
		var setBy int64
		if u, ok := UserFromContext(r.Context()); ok {
			setBy = u.ID
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		if err := d.Overrides.SetOverride(ctx, body.TargetKind, body.TargetID, body.Field, body.Value, setBy); err != nil {
			writeOverrideErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// handleRevertOverride — DELETE /api/admin/overrides. Body:
// {"target_kind":"book","target_id":123,"field":"edition_year"}.
func handleRevertOverride(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Overrides == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "overrides disabled"})
			return
		}
		var body struct {
			TargetKind string `json:"target_kind"`
			TargetID   int64  `json:"target_id"`
			Field      string `json:"field"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&body); err != nil ||
			body.TargetID == 0 || body.Field == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "target_id, field required"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		if err := d.Overrides.RevertOverride(ctx, body.TargetKind, body.TargetID, body.Field); err != nil {
			writeOverrideErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// handleRevertAllOverrides — POST /api/admin/overrides/revert-all. Body: {"book_id":123}.
func handleRevertAllOverrides(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Overrides == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "overrides disabled"})
			return
		}
		var body struct {
			BookID int64 `json:"book_id"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16*1024)).Decode(&body); err != nil || body.BookID == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "book_id required"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		if err := d.Overrides.RevertAllForBook(ctx, body.BookID); err != nil {
			writeOverrideErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// handleListOverrides — GET /api/admin/overrides?work_id=N. Какие поля работы и
// её изданий оверрайднуты — для индикаторов «изменено»/откат на карточке (админ).
// Ответ: {"work":["title"],"book":{"123":["edition_year"]}}.
func handleListOverrides(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Overrides == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "overrides disabled"})
			return
		}
		workID, err := strconv.ParseInt(r.URL.Query().Get("work_id"), 10, 64)
		if err != nil || workID == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "work_id required"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		perBook, workFields, err := d.Overrides.OverridesForWork(ctx, workID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list failed"})
			return
		}
		if workFields == nil {
			workFields = []string{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"work": workFields, "book": perBook})
	}
}

func writeOverrideErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, metadata.ErrUnknownOverrideField):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown field"})
	case errors.Is(err, metadata.ErrOverrideTargetNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "target not found"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "override failed"})
	}
}
