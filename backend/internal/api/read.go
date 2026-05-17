package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
)

// handleMarkRead — POST /api/books/{id}/read. Идемпотентна.
// Возвращает {"is_read": true}.
func handleMarkRead(d HistoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		bookID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || bookID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := d.Service.MarkRead(ctx, u.ID, bookID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"is_read": true})
	}
}

// handleUnmarkRead — DELETE /api/books/{id}/read. Идемпотентна.
func handleUnmarkRead(d HistoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		bookID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || bookID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := d.Service.UnmarkRead(ctx, u.ID, bookID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"is_read": false})
	}
}

// handleGetPosition — GET /api/books/{id}/position. Возвращает
// {"pos": "<epub-cfi>"} либо {"pos": ""} если позиция не сохранялась.
// Используется ридером на старте чтобы решить, открывать с начала
// или с сохранённой позиции.
func handleGetPosition(d HistoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		bookID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || bookID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		pos, err := d.Service.GetPosition(ctx, u.ID, bookID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"pos": pos})
	}
}

// handleSavePosition — PUT /api/books/{id}/position с body {"pos": "..."}.
// Ридер дёргает на каждом перевороте «страницы» (foliate-js event
// `relocate`); приходит дебаунсенно с фронта (раз в ~3 секунды),
// чтобы не молотить БД.
func handleSavePosition(d HistoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		bookID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || bookID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		// Лимит на размер тела — epub-cfi обычно <500 байт. Защита от
		// мусорного POST'а в десятки мегабайт.
		var body struct {
			Pos string `json:"pos"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		if err := dec.Decode(&body); err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "position too large"})
				return
			}
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := d.Service.SavePosition(ctx, u.ID, bookID, body.Pos); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"pos": body.Pos})
	}
}
