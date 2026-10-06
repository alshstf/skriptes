package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/skriptes/skriptes/backend/internal/history"
)

// Закладки и выделения с заметками веб-ридера (#389, B6).

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	return id, err == nil && id > 0
}

// handleBookAnnotations — GET /api/books/{id}/annotations.
func handleBookAnnotations(d HistoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		id, ok := pathID(r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		items, err := d.Service.BookAnnotations(ctx, u.ID, id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// handleWorkAnnotations — GET /api/works/{id}/annotations: все издания работы.
func handleWorkAnnotations(d HistoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		id, ok := pathID(r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		items, err := d.Service.WorkAnnotations(ctx, u.ID, id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// handleSaveAnnotation — POST /api/books/{id}/annotations.
func handleSaveAnnotation(d HistoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		id, ok := pathID(r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		var a history.Annotation
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&a); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		saved, err := d.Service.SaveAnnotation(ctx, u.ID, id, a)
		switch {
		case errors.Is(err, history.ErrInvalidAnnotation):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid annotation"})
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
		default:
			writeJSON(w, http.StatusOK, saved)
		}
	}
}

// handleUpdateAnnotation — PATCH /api/annotations/{id} {note}.
func handleUpdateAnnotation(d HistoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		id, ok := pathID(r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		var req struct {
			Note string `json:"note"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		a, err := d.Service.UpdateAnnotationNote(ctx, u.ID, id, req.Note)
		switch {
		case errors.Is(err, history.ErrAnnotationNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
		default:
			writeJSON(w, http.StatusOK, a)
		}
	}
}

// handleDeleteAnnotation — DELETE /api/annotations/{id}.
func handleDeleteAnnotation(d HistoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		id, ok := pathID(r)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		switch err := d.Service.DeleteAnnotation(ctx, u.ID, id); {
		case errors.Is(err, history.ErrAnnotationNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}
}
