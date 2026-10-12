package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/skriptes/skriptes/backend/internal/auth"
	"github.com/skriptes/skriptes/backend/internal/bio/timeline"
)

// BioDeps — био-таймлайн автора (план v3). Service == nil — ручки не монтируются.
type BioDeps struct {
	Service *timeline.Service
}

// handleAuthorBioTimeline — GET /api/authors/{id}/bio-timeline. Админу —
// скрытые вехи с флагом hidden (для «Скрытые (N) — вернуть»).
func handleAuthorBioTimeline(d BioDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
			return
		}
		u, _ := UserFromContext(r.Context())
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		tl, err := d.Service.Timeline(ctx, id, u.Role == auth.RoleAdmin)
		switch {
		case errors.Is(err, timeline.ErrNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, tl)
	}
}

// handleWorkLifeContext — GET /api/works/{id}/life-context: «в жизни автора в
// это время» для карточки книги.
func handleWorkLifeContext(d BioDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		lc, err := d.Service.LifeContextForWork(ctx, id)
		switch {
		case errors.Is(err, timeline.ErrNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		case lc == nil:
			writeJSON(w, http.StatusOK, timeline.LifeContext{})
			return
		}
		writeJSON(w, http.StatusOK, lc)
	}
}

// handleSetBioEventHidden — PATCH /api/admin/bio-timeline/{qid}/events/{key} {hidden}.
func handleSetBioEventHidden(d BioDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		var body struct {
			Hidden bool `json:"hidden"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad json"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		err := d.Service.SetHidden(ctx, chi.URLParam(r, "qid"), chi.URLParam(r, "key"), body.Hidden, u.ID)
		switch {
		case errors.Is(err, timeline.ErrNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"qid": chi.URLParam(r, "qid"), "key": chi.URLParam(r, "key"), "hidden": body.Hidden})
	}
}

// handleBioTimelineStats — GET /api/admin/bio-timeline/stats: покрытие датасета.
func handleBioTimelineStats(d BioDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		st, err := d.Service.Stats(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, st)
	}
}
