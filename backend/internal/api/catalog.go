package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/skriptes/skriptes/backend/internal/catalog"
)

// CatalogDeps — зависимости /api/authors/:id и /api/series/:id.
type CatalogDeps struct {
	Service *catalog.Service
}

func handleGetAuthor(d CatalogDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		a, err := d.Service.GetAuthor(ctx, id)
		if err != nil {
			if errors.Is(err, catalog.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "author not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, a)
	}
}

func handleGetSeries(d CatalogDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		s, err := d.Service.GetSeries(ctx, id)
		if err != nil {
			if errors.Is(err, catalog.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "series not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, s)
	}
}
