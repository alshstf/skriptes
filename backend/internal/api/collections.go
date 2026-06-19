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

// CollectionsDeps — зависимости эндпоинтов личных полок.
// Service может быть nil — тогда эндпоинты не монтируются (как HistoryDeps).
type CollectionsDeps struct {
	Service *collections.Service
}

// collectionNameReq — тело создания/переименования полки.
type collectionNameReq struct {
	Name string `json:"name"`
}

// handleListCollections — GET /api/me/collections. Полки текущего юзера
// с числом живых книг.
func handleListCollections(d CollectionsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		items, err := d.Service.ListCollections(ctx, u.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// handleCreateCollection — POST /api/me/collections.
func handleCreateCollection(d CollectionsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		var req collectionNameReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		c, err := d.Service.CreateCollection(ctx, u.ID, req.Name)
		switch {
		case errors.Is(err, collections.ErrEmptyName):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
			return
		}
		writeJSON(w, http.StatusOK, c)
	}
}

// handleRenameCollection — PATCH /api/me/collections/{id}.
func handleRenameCollection(d CollectionsDeps) http.HandlerFunc {
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
		var req collectionNameReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		err = d.Service.RenameCollection(ctx, u.ID, id, req.Name)
		switch {
		case errors.Is(err, collections.ErrEmptyName):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "name required"})
			return
		case errors.Is(err, collections.ErrNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "collection not found"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// handleDeleteCollection — DELETE /api/me/collections/{id}.
func handleDeleteCollection(d CollectionsDeps) http.HandlerFunc {
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
		err = d.Service.DeleteCollection(ctx, u.ID, id)
		switch {
		case errors.Is(err, collections.ErrNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "collection not found"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// handleListCollectionBooks — GET /api/me/collections/{id} — книги полки.
func handleListCollectionBooks(d CollectionsDeps) http.HandlerFunc {
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
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		books, err := d.Service.ListCollectionBooks(ctx, u.ID, id)
		switch {
		case errors.Is(err, collections.ErrNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "collection not found"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": books})
	}
}

// handleAddBookToCollection — POST /api/me/collections/{id}/books/{bookId}.
func handleAddBookToCollection(d CollectionsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		collID, bookID, ok := collectionBookParams(w, r)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		err := d.Service.AddBookToCollection(ctx, u.ID, collID, bookID)
		switch {
		case errors.Is(err, collections.ErrNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "collection not found"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// handleRemoveBookFromCollection — DELETE /api/me/collections/{id}/books/{bookId}.
func handleRemoveBookFromCollection(d CollectionsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		collID, bookID, ok := collectionBookParams(w, r)
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		err := d.Service.RemoveBookFromCollection(ctx, u.ID, collID, bookID)
		switch {
		case errors.Is(err, collections.ErrNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "collection not found"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}
}

// collectionBookParams — парсит {id} и {bookId} из URL, валидирует > 0.
// На ошибке сам пишет 400 и возвращает ok=false.
func collectionBookParams(w http.ResponseWriter, r *http.Request) (collID, bookID int64, ok bool) {
	var err error
	collID, err = strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || collID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
		return 0, 0, false
	}
	bookID, err = strconv.ParseInt(chi.URLParam(r, "bookId"), 10, 64)
	if err != nil || bookID <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid book id"})
		return 0, 0, false
	}
	return collID, bookID, true
}
