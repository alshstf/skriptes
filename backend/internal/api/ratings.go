package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/skriptes/skriptes/backend/internal/history"
	"github.com/skriptes/skriptes/backend/internal/settings"
)

// Пользовательские оценки книг (work-level). Ключ маршрута — work_id (карточка
// и /books/{id}, и /works/{id} несут work_id). Отдельно от books.rating (LIBRATE).

type ratingReq struct {
	Rating int `json:"rating"`
}

// handleSetRating — PUT /api/works/{id}/rating: поставить/изменить оценку (1–5).
func handleSetRating(d HistoryDeps) http.HandlerFunc {
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
		var req ratingReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := d.Service.SetRating(ctx, u.ID, workID, req.Rating); err != nil {
			switch {
			case errors.Is(err, history.ErrInvalidRating):
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rating must be between 1 and 5"})
			case isForeignKeyViolation(err):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "work not found"})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
			}
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"rating": req.Rating})
	}
}

// handleRemoveRating — DELETE /api/works/{id}/rating: снять оценку. Идемпотентна.
func handleRemoveRating(d HistoryDeps) http.HandlerFunc {
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
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := d.Service.RemoveRating(ctx, u.ID, workID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"removed": true})
	}
}

// isForeignKeyViolation — true если err это PG foreign_key_violation (23503),
// т.е. оценку ставят несуществующей работе.
func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}

// ── Отложенные запросы оценки («Оцените прочитанное») ──────────────

// handleRateablePrompts — GET /api/me/rating-prompts/feed?limit=
// Книги, которые юзер вероятно прочитал и ещё не оценил (для блока Главной).
// Если запросы оценки выключены в профиле — отдаём пустой список.
func handleRateablePrompts(hist HistoryDeps, set SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		cfg, err := set.Store.UserRatingPromptConfig(ctx, u.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read settings failed"})
			return
		}
		if !cfg.Enabled {
			writeJSON(w, http.StatusOK, map[string]any{"items": []history.RateableItem{}})
			return
		}
		limit := parseIntOr(r.URL.Query().Get("limit"), 12)
		items, err := hist.Service.RateableWorks(ctx, u.ID, cfg.DelayDays, limit)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// handleDismissRatingPrompt — POST /api/works/{id}/rating-prompt/dismiss.
// «Не буду оценивать»: скрыть, пока не появится явный сигнал прочтения.
func handleDismissRatingPrompt(hist HistoryDeps) http.HandlerFunc {
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
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := hist.Service.DismissRatingPrompt(ctx, u.ID, workID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"dismissed": true})
	}
}

// handleSnoozeRatingPrompt — POST /api/works/{id}/rating-prompt/snooze.
// «Ещё не прочитал»: скрыть на delay (из настроек профиля), потом спросить снова.
func handleSnoozeRatingPrompt(hist HistoryDeps, set SettingsDeps) http.HandlerFunc {
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
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		cfg, err := set.Store.UserRatingPromptConfig(ctx, u.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read settings failed"})
			return
		}
		if err := hist.Service.SnoozeRatingPrompt(ctx, u.ID, workID, cfg.DelayDays); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"snoozed": true})
	}
}

// handleGetMeRatingPrompts — GET /api/me/rating-prompts. Настройки запросов оценки.
func handleGetMeRatingPrompts(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		cfg, err := d.Store.UserRatingPromptConfig(ctx, u.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read settings failed"})
			return
		}
		writeJSON(w, http.StatusOK, cfg)
	}
}

// handleUpdateMeRatingPrompts — PUT /api/me/rating-prompts.
func handleUpdateMeRatingPrompts(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		var cfg settings.RatingPromptConfig
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&cfg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := d.Store.SetUserRatingPromptConfig(ctx, u.ID, cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
			return
		}
		saved, err := d.Store.UserRatingPromptConfig(ctx, u.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read settings failed"})
			return
		}
		writeJSON(w, http.StatusOK, saved)
	}
}
