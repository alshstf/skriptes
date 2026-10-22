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
	"github.com/skriptes/skriptes/backend/internal/recsys"
)

// Явная обратная связь (план recsys/03): причины низкой оценки (3.1), «не
// предлагать» автора/цикл (3.2), полка «Хочу прочитать» (3.3). Всё — по
// пользователю; каталог и поиск не меняются.

// handleSetRatingReasons — PUT /api/works/{id}/rating-reasons {reasons: [...]}:
// заменить набор причин низкой оценки (пустой — убрать). Неизвестная — 400.
func handleSetRatingReasons(d HistoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		workID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || workID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		var req struct {
			Reasons []recsys.RatingReason `json:"reasons"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := d.Service.SetRatingReasons(ctx, u.ID, workID, req.Reasons); err != nil {
			switch {
			case errors.Is(err, history.ErrInvalidRatingReason):
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown reason"})
			case isForeignKeyViolation(err):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "work not found"})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
			}
			return
		}
		saved, err := d.Service.RatingReasons(ctx, u.ID, workID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"reasons": saved})
	}
}

// blockTarget — автор или цикл для ручек «не предлагать».
type blockTarget struct {
	block   func(ctx context.Context, s *history.Service, userID, id int64) error
	unblock func(ctx context.Context, s *history.Service, userID, id int64) error
}

var (
	blockAuthorTarget = blockTarget{
		block:   func(ctx context.Context, s *history.Service, u, id int64) error { return s.BlockAuthor(ctx, u, id) },
		unblock: func(ctx context.Context, s *history.Service, u, id int64) error { return s.UnblockAuthor(ctx, u, id) },
	}
	blockSeriesTarget = blockTarget{
		block:   func(ctx context.Context, s *history.Service, u, id int64) error { return s.BlockSeries(ctx, u, id) },
		unblock: func(ctx context.Context, s *history.Service, u, id int64) error { return s.UnblockSeries(ctx, u, id) },
	}
)

// handleBlock — POST (block=true) / DELETE (block=false) /api/authors/{id}/block и
// /api/series/{id}/block. Идемпотентны; ответ — {is_blocked}.
func handleBlock(d HistoryDeps, t blockTarget, block bool) http.HandlerFunc {
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
		fn := t.unblock
		if block {
			fn = t.block
		}
		if err := fn(ctx, d.Service, u.ID, id); err != nil {
			if isForeignKeyViolation(err) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"is_blocked": block})
	}
}

// handleListBlocklist — GET /api/me/blocklist: {authors: [...], series: [...]}.
func handleListBlocklist(d HistoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		list, err := d.Service.ListBlocklist(ctx, u.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, list)
	}
}

// handleToRead — POST (add=true) / DELETE (add=false) /api/books/{id}/to-read:
// полка «Хочу прочитать» (служебная, kind='to_read'); ответ — {is_to_read}.
func handleToRead(d HistoryDeps, add bool) http.HandlerFunc {
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
		fn := d.Service.RemoveToRead
		if add {
			fn = d.Service.AddToRead
		}
		if err := fn(ctx, u.ID, bookID); err != nil {
			if isForeignKeyViolation(err) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "book not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"is_to_read": add})
	}
}
