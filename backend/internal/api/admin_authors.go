package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/skriptes/skriptes/backend/internal/catalog"
	"github.com/skriptes/skriptes/backend/internal/metadata"
)

// handleSetAuthorService — PUT /api/admin/authors/{id}/service. Ручная метка
// «служебный автор» (агрегат-псевдоавтор: скрыт из списка /authors) — в обе
// стороны. Пишет is_service_source='manual', чтобы эвристика
// ClassifyServiceAuthors решение не перетирала.
func handleSetAuthorService(d CatalogDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid author id"})
			return
		}
		var body struct {
			IsService bool `json:"is_service"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		if err := d.Service.SetAuthorService(r.Context(), id, body.IsService); err != nil {
			if errors.Is(err, catalog.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "author not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"is_service": body.IsService})
	}
}

// handleAuthorDuplicates — GET /api/admin/authors/{id}/duplicates: возможные
// дубли автора (#308) для подсказки на его карточке.
func handleAuthorDuplicates(d CatalogDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid author id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		items, err := d.Service.AuthorDuplicates(ctx, id)
		if err != nil {
			slog.Default().Warn("author duplicates failed", "author_id", id, "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// handleDuplicatePairs — GET /api/admin/authors/duplicates?limit=&offset=: пары
// возможных дублей от самых известных (страница админки «Дубли авторов»).
func handleDuplicatePairs(d CatalogDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		pairs, total, err := d.Service.DuplicatePairs(ctx, limit, offset)
		if err != nil {
			slog.Default().Warn("duplicate pairs failed", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": pairs, "total": total})
	}
}

// handleMergeAuthors — POST /api/admin/authors/{id}/merge {target_id}: автор id
// сливается в target_id (#308, metadata.MergeAuthors): работы, подписки, био при
// пустом у цели; слияние помнится для следующих импортов.
func handleMergeAuthors(s SettingsDeps, c CatalogDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Overrides == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "overrides disabled"})
			return
		}
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid author id"})
			return
		}
		var body struct {
			TargetID int64 `json:"target_id"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil || body.TargetID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "target_id required"})
			return
		}
		var setBy int64
		if u, ok := UserFromContext(r.Context()); ok {
			setBy = u.ID
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		works, err := s.Overrides.MergeAuthors(ctx, id, body.TargetID, setBy)
		switch {
		case errors.Is(err, metadata.ErrMergeSameAuthor):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "нельзя слить автора сам в себя"})
			return
		case errors.Is(err, metadata.ErrMergeAuthorNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "author not found"})
			return
		case err != nil:
			slog.Default().Warn("merge authors failed", "source_id", id, "target_id", body.TargetID, "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "merge failed"})
			return
		}
		// Число книг и рейтинг авторов (сортировки /authors) — сразу, не через полчаса.
		if c.Service != nil {
			metadata.Go(func(ctx context.Context) {
				if _, err := c.Service.RecomputeAuthorStats(ctx); err != nil {
					slog.Default().Warn("author stats after merge failed", "err", err)
				}
			})
		}
		writeJSON(w, http.StatusOK, map[string]any{"target_id": body.TargetID, "works": len(works)})
	}
}
