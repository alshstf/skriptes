package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/skriptes/skriptes/backend/internal/books"
)

// BooksDeps — зависимости для эндпоинтов /api/books*.
// Service может быть nil — тогда эндпоинты не монтируются.
type BooksDeps struct {
	Service *books.Service
}

func handleListBooks(d BooksDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		params := books.ListParams{
			Query:  q.Get("q"),
			Limit:  parseIntOr(q.Get("limit"), 20),
			Offset: parseIntOr(q.Get("offset"), 0),
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		res, err := d.Service.List(ctx, params)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "search failed"})
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

func handleGetBook(d BooksDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		b, err := d.Service.Get(ctx, id)
		if err != nil {
			if errors.Is(err, books.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "book not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, b)
	}
}

func parseIntOr(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}
