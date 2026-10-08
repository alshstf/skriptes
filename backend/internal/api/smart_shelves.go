package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/skriptes/skriptes/backend/internal/collections"
)

// Умные полки (#389): сохранённые фильтры /books. Состав считает фронт тем же
// GET /api/books (с unread=1 для «Только непрочитанные»).

type smartShelfReq struct {
	Name    *string                   `json:"name"`
	Filters *collections.SmartFilters `json:"filters"`
}

// smartShelfError — ошибка сервиса → HTTP-ответ; false — ошибки не было.
func smartShelfError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, collections.ErrEmptyName):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
	case errors.Is(err, collections.ErrBadFilters):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid filters"})
	case errors.Is(err, collections.ErrTooMany):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "too many smart shelves"})
	case errors.Is(err, collections.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
	}
	return true
}

// handleListSmartShelves — GET /api/me/smart-shelves.
func handleListSmartShelves(d CollectionsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		items, err := d.Service.ListSmartShelves(ctx, u.ID)
		if smartShelfError(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// handleCreateSmartShelf — POST /api/me/smart-shelves {name, filters}.
func handleCreateSmartShelf(d CollectionsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		var req smartShelfReq
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil ||
			req.Name == nil || req.Filters == nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		sh, err := d.Service.CreateSmartShelf(ctx, u.ID, *req.Name, *req.Filters)
		if smartShelfError(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, sh)
	}
}

// handleUpdateSmartShelf — PATCH /api/me/smart-shelves/{id} {name?, filters?}.
func handleUpdateSmartShelf(d CollectionsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		var req smartShelfReq
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		sh, err := d.Service.UpdateSmartShelf(ctx, u.ID, id, req.Name, req.Filters)
		if smartShelfError(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, sh)
	}
}

// handleDeleteSmartShelf — DELETE /api/me/smart-shelves/{id}.
func handleDeleteSmartShelf(d CollectionsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if smartShelfError(w, d.Service.DeleteSmartShelf(ctx, u.ID, id)) {
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
