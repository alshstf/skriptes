package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
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
			Query:    q.Get("q"),
			Limit:    parseIntOr(q.Get("limit"), 20),
			Offset:   parseIntOr(q.Get("offset"), 0),
			Genres:   splitCSV(q.Get("genres")),
			Lang:     q.Get("lang"),
			YearFrom: parseIntOr(q.Get("year_from"), 0),
			YearTo:   parseIntOr(q.Get("year_to"), 0),
			SeriesID: parseInt64Or(q.Get("series_id"), 0),
			AuthorID: parseInt64Or(q.Get("author_id"), 0),
			Sort:     q.Get("sort"),
			Facets:   splitCSV(q.Get("facets")),
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

func parseInt64Or(s string, def int64) int64 {
	if s == "" {
		return def
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// splitCSV — разбивает значение query-параметра вида "a,b,c" в []string,
// пропуская пустые элементы. Пустая или отсутствующая строка → nil.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
