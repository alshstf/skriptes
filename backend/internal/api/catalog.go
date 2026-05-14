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

// authorResponse / seriesResponse — обёртки над catalog-DTO с
// user-specific is_favorite. Как и bookResponse, держим в api-слое
// чтобы не тащить user-концепт в catalog.
type authorResponse struct {
	catalog.Author
	IsFavorite bool `json:"is_favorite"`
}

type seriesResponse struct {
	catalog.Series
	IsFavorite bool `json:"is_favorite"`
}

func handleGetAuthor(d CatalogDeps, hist HistoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// userID нужен сервису для ReadCount; и параллельно для is_favorite.
		var userID int64
		if u, ok := UserFromContext(r.Context()); ok {
			userID = u.ID
		}

		a, err := d.Service.GetAuthor(ctx, id, userID)
		if err != nil {
			if errors.Is(err, catalog.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "author not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		var isFav bool
		if userID > 0 && hist.Service != nil {
			if v, err := hist.Service.IsFavoriteAuthor(ctx, userID, id); err == nil {
				isFav = v
			}
		}
		writeJSON(w, http.StatusOK, authorResponse{Author: a, IsFavorite: isFav})
	}
}

func handleGetSeries(d CatalogDeps, hist HistoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		var userID int64
		if u, ok := UserFromContext(r.Context()); ok {
			userID = u.ID
		}

		s, err := d.Service.GetSeries(ctx, id, userID)
		if err != nil {
			if errors.Is(err, catalog.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "series not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		var isFav bool
		if userID > 0 && hist.Service != nil {
			if v, err := hist.Service.IsFavoriteSeries(ctx, userID, id); err == nil {
				isFav = v
			}
		}
		writeJSON(w, http.StatusOK, seriesResponse{Series: s, IsFavorite: isFav})
	}
}
