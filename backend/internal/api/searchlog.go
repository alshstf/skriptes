package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/skriptes/skriptes/backend/internal/recsys"
)

// Лог поисковых запросов (план recsys/01, задача 1.4). Запись — в
// handleListBooks (books.go); здесь — клик из выдачи, история пользователя,
// удаление и агрегат ненайденного для админа. Лог выключен
// (SKRIPTES_SEARCH_LOG_DAYS=0) — ручки отвечают пусто/204.

// RecsDeps — зависимости ленты рекомендаций и лога поиска.
type RecsDeps struct {
	SearchLog *recsys.SearchLog
}

// handleSearchClick — POST /api/me/search-log/{id}/click {work_id}.
func handleSearchClick(d RecsDeps) http.HandlerFunc {
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
		var body struct {
			WorkID int64 `json:"work_id"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil || body.WorkID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid work_id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := d.SearchLog.Click(ctx, u.ID, id, body.WorkID); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleListSearchLog — GET /api/me/search-log?limit=&offset=
func handleListSearchLog(d RecsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		resp := map[string]any{"items": []recsys.Entry{}, "total": 0, "retention_days": d.SearchLog.RetentionDays}
		if !d.SearchLog.Enabled() {
			writeJSON(w, http.StatusOK, resp)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		items, total, err := d.SearchLog.List(ctx, u.ID,
			parseIntOr(r.URL.Query().Get("limit"), 50), parseIntOr(r.URL.Query().Get("offset"), 0))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		resp["items"], resp["total"] = items, total
		writeJSON(w, http.StatusOK, resp)
	}
}

// handleDeleteSearchLog — DELETE /api/me/search-log (вся история) и
// DELETE /api/me/search-log/{id} (одна строка).
func handleDeleteSearchLog(d RecsDeps, one bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		var err error
		if one {
			id, perr := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
			if perr != nil || id <= 0 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
				return
			}
			err = d.SearchLog.Delete(ctx, u.ID, id)
		} else {
			err = d.SearchLog.DeleteAll(ctx, u.ID)
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleSearchMisses — GET /api/admin/search-log/misses?days=90&limit=50:
// что ищут и не находят (агрегат без пользователей) — для пополнения библиотеки.
func handleSearchMisses(d RecsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		days := parseIntOr(r.URL.Query().Get("days"), 90)
		resp := map[string]any{"items": []recsys.Miss{}, "days": days, "enabled": d.SearchLog.Enabled()}
		if !d.SearchLog.Enabled() {
			writeJSON(w, http.StatusOK, resp)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		items, err := d.SearchLog.Misses(ctx, days, parseIntOr(r.URL.Query().Get("limit"), 50))
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		resp["items"] = items
		writeJSON(w, http.StatusOK, resp)
	}
}

// searchFilters — непустые фильтры списка для лога (без личных данных).
func searchFilters(q map[string][]string) map[string]any {
	out := map[string]any{}
	for _, k := range []string{"genres", "lang", "src_lang", "kind", "year_from", "year_to", "series_id", "author_id", "sort", "unread", "awards", "has_award"} {
		if v := q[k]; len(v) > 0 && v[0] != "" && v[0] != "0" {
			out[k] = v[0]
		}
	}
	return out
}
