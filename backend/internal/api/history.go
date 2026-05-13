package api

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/skriptes/skriptes/backend/internal/history"
)

// HistoryDeps — зависимости для favorites/recent эндпоинтов.
// Service может быть nil — тогда эндпоинты не монтируются (полезно
// для unit-тестов простых ручек без БД).
type HistoryDeps struct {
	Service *history.Service
}

// recordViewAsync — отвязанный от запроса fire-and-forget INSERT в views.
// Используется из handleGetBook: пользователь открыл карточку — пишем.
//
// Контекст НЕ наследуем от r.Context() — иначе если клиент закроет
// соединение раньше чем DB ответит, запись потеряется. Делаем
// собственный с маленьким deadline.
func recordViewAsync(svc *history.Service, userID, bookID int64) {
	if svc == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := svc.RecordView(ctx, userID, bookID); err != nil {
			slog.Warn("record view failed", "user_id", userID, "book_id", bookID, "err", err)
		}
	}()
}

// recordReadAsync — аналог для скачивания книги.
func recordReadAsync(svc *history.Service, userID, bookID int64) {
	if svc == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := svc.RecordRead(ctx, userID, bookID); err != nil {
			slog.Warn("record read failed", "user_id", userID, "book_id", bookID, "err", err)
		}
	}()
}

// favoriteToggle — POST/DELETE /api/books/{id}/favorite.
// Идемпотентны: повторный POST не падает, повторный DELETE тоже.
func handleAddFavorite(d HistoryDeps) http.HandlerFunc {
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
		if err := d.Service.AddFavorite(ctx, u.ID, bookID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"is_favorite": true})
	}
}

func handleRemoveFavorite(d HistoryDeps) http.HandlerFunc {
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
		if err := d.Service.RemoveFavorite(ctx, u.ID, bookID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"is_favorite": false})
	}
}

// handleListFavorites — GET /api/me/favorites?limit=&offset=
func handleListFavorites(d HistoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		limit := parseIntOr(r.URL.Query().Get("limit"), 50)
		offset := parseIntOr(r.URL.Query().Get("offset"), 0)
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		items, err := d.Service.ListFavorites(ctx, u.ID, limit, offset)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		total, err := d.Service.FavoritesCount(ctx, u.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, history.FavoritesListResponse{
			Items:  items,
			Total:  total,
			Limit:  limit,
			Offset: offset,
		})
	}
}

// handleRecentViews — GET /api/me/recent?limit=
func handleRecentViews(d HistoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		limit := parseIntOr(r.URL.Query().Get("limit"), 20)
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		items, err := d.Service.RecentViews(ctx, u.ID, limit)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}
