package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/skriptes/skriptes/backend/internal/awards"
	"github.com/skriptes/skriptes/backend/internal/books"
)

// AwardsDeps — премии (#389, A2).
type AwardsDeps struct {
	Service *awards.Service
}

// handleListAwards — GET /api/awards: премии белого списка с числом лауреатов.
func handleListAwards(d AwardsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		items, err := d.Service.List(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// awardWin — лауреат с карточкой книги, если она есть в каталоге и не скрыта.
type awardWin struct {
	awards.Win
	Item *books.ListItem `json:"item,omitempty"`
}

// handleAwardWins — GET /api/awards/{key}: лауреаты премии по годам. Книги,
// скрытые от пользователя (жанры, языки), не показываются совсем — ни
// карточкой, ни строкой.
func handleAwardWins(d AwardsDeps, b BooksDeps, hist HistoryDeps, content ContentDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		award, wins, err := d.Service.Wins(ctx, chi.URLParam(r, "key"))
		switch {
		case errors.Is(err, awards.ErrUnknownAward):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		var ids []int64
		for _, x := range wins {
			if x.WorkID != nil {
				ids = append(ids, *x.WorkID)
			}
		}
		visible := map[int64]*books.ListItem{}
		if len(ids) > 0 {
			items, err := b.Service.VisibleWorks(ctx, ids, presetParams(r, u.ID, content))
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
				return
			}
			hydrateUserListMeta(ctx, items, u.ID, hist.Service)
			for i := range items {
				visible[items[i].ID] = &items[i]
			}
		}
		out := make([]awardWin, 0, len(wins))
		for _, x := range wins {
			aw := awardWin{Win: x}
			if x.WorkID != nil {
				it, ok := visible[*x.WorkID]
				if !ok {
					continue
				}
				aw.Item = it
			}
			out = append(out, aw)
		}
		writeJSON(w, http.StatusOK, map[string]any{"award": award, "wins": out})
	}
}

// handleWorkAwards — GET /api/works/{id}/awards: премии книги (плашки карточки).
func handleWorkAwards(d AwardsDeps) http.HandlerFunc {
	return badgesHandler(d.Service.WorkAwards)
}

// handleAuthorAwards — GET /api/authors/{id}/awards: премии, врученные автору.
func handleAuthorAwards(d AwardsDeps) http.HandlerFunc {
	return badgesHandler(d.Service.AuthorAwards)
}

func badgesHandler(get func(context.Context, int64) ([]awards.Badge, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		items, err := get(ctx, id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}
