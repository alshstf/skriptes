package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/skriptes/skriptes/backend/internal/auth"
	"github.com/skriptes/skriptes/backend/internal/authorevents"
)

// AuthorEventsDeps — читающий сервис био-таймлайна.
type AuthorEventsDeps struct {
	Service *authorevents.Service
}

// handleListAuthorEvents — GET /api/authors/{id}/events. Эндпоинт сам служит
// lazy-триггером (зеркало /adaptations): status=pending → детачнутый
// EnsureAuthorEvents (renown-гейт и single-shot — внутри Ensure, здесь без
// дополнительных проверок). Фронт поллит до done.
func handleListAuthorEvents(d AuthorEventsDeps, meta MetadataDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		isAdmin := false
		if u, ok := auth.UserFromContext(r.Context()); ok && u.Role == auth.RoleAdmin {
			isAdmin = true
		}

		res, err := d.Service.List(ctx, id, isAdmin)
		if err != nil {
			if errors.Is(err, authorevents.ErrAuthorNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "author not found"})
				return
			}
			slog.Error("list author events failed", "author_id", id, "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}

		if res.EnrichmentStatus == "pending" {
			triggerAuthorEventsAsync(meta, id)
		}
		writeJSON(w, http.StatusOK, res)
	}
}

// triggerAuthorEventsAsync — fire-and-forget EnsureAuthorEvents с детачнутым
// контекстом (SPARQL медленный — 90с бюджет, зеркало adaptations-триггера).
func triggerAuthorEventsAsync(meta MetadataDeps, authorID int64) {
	if meta.Service == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		meta.Service.EnsureAuthorEvents(ctx, authorID)
	}()
}
