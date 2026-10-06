package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/skriptes/skriptes/backend/internal/books"
)

// presetParams — кто смотрит подборку и что ему скрыто.
func presetParams(r *http.Request, userID int64, content ContentDeps) books.PresetParams {
	p := books.PresetParams{UserID: userID, Now: time.Now()}
	if content.Resolver != nil {
		p.ExcludeGenres, p.ExcludeLangs, p.HideCompilations = content.Resolver.Exclusions(r.Context(), userID)
	}
	return p
}

// handleListPresets — GET /api/me/presets: готовые подборки (#389) со счётчиками.
func handleListPresets(d BooksDeps, content ContentDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		items, err := d.Service.Presets(ctx, presetParams(r, u.ID, content))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// handlePresetBooks — GET /api/me/presets/{key}: книги подборки.
func handlePresetBooks(d BooksDeps, hist HistoryDeps, content ContentDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		res, err := d.Service.PresetWorks(ctx, chi.URLParam(r, "key"), presetParams(r, u.ID, content))
		switch {
		case errors.Is(err, books.ErrUnknownPreset):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		hydrateUserListMeta(ctx, res.Items, u.ID, hist.Service)
		writeJSON(w, http.StatusOK, res)
	}
}
