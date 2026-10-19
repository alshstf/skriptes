package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/skriptes/skriptes/backend/internal/auth"
	"github.com/skriptes/skriptes/backend/internal/hiding"
)

// HidingDeps — скрытие авторов и работ на инстансе (админ, миграция 0064).
// Sync — пересчёт флагов instance_hidden и синхронизация Meili
// (importer.SyncInstanceHidden); nil — ручки не монтируются.
type HidingDeps struct {
	Store *hiding.Store
	Sync  func(ctx context.Context) (books, works int, err error)
}

type hideBody struct {
	Note string `json:"note"`
}

// handleSetHidden — POST (скрыть) / DELETE (вернуть)
// /api/admin/{authors|works}/{id}/hide. Отвечает числом изданий и работ, у
// которых поменялась видимость.
func handleSetHidden(d HidingDeps, kind hiding.Kind, hide bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
			return
		}
		// Пересчёт с ресинком Meili — секунды; таймаут с запасом.
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		if hide {
			var body hideBody
			if r.ContentLength > 0 {
				if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
					writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad body"})
					return
				}
			}
			var by int64
			if u, ok := UserFromContext(r.Context()); ok {
				by = u.ID
			}
			err = d.Store.Hide(ctx, kind, id, by, body.Note)
		} else {
			err = d.Store.Unhide(ctx, kind, id)
		}
		if errors.Is(err, hiding.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
			return
		}
		books, works, err := d.Sync(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "sync failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"hidden": hide, "books_changed": books, "works_changed": works})
	}
}

// handleListHidden — GET /api/admin/hidden: всё явно скрытое.
func handleListHidden(d HidingDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		items, err := d.Store.List(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list failed"})
			return
		}
		if items == nil {
			items = []hiding.Item{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// handleHidePreview — GET /api/admin/{authors|works}/{id}/hide-preview: сколько
// изданий и работ скроет скрытие (текст подтверждения в UI).
func handleHidePreview(d HidingDeps, kind hiding.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		books, works, err := d.Store.Preview(ctx, kind, id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "preview failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"books": books, "works": works})
	}
}

// requireNotHidden — 404 на маршрутах «по id», если объект скрыт на инстансе
// (владелец: скрытое совсем недоступно — карточки, скачивание, чтение, Kindle,
// OPDS). Админ проходит: ему нужна карточка с плашкой и «Вернуть». Store == nil
// — пропуск; ошибка запроса — пропуск (как bookGate по жанрам: не роняем чтение
// из-за сбоя проверки).
func requireNotHidden(d HidingDeps, kind hiding.Kind) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if d.Store != nil {
				if u, ok := UserFromContext(r.Context()); !ok || u.Role != auth.RoleAdmin {
					if id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64); err == nil && id > 0 {
						ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
						hidden, qErr := d.Store.Hidden(ctx, kind, id)
						cancel()
						if qErr == nil && hidden {
							http.NotFound(w, r)
							return
						}
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// handleHiddenStatus — GET /api/admin/{authors|works|books}/{id}/hidden-status:
// плашка «Скрыто на инстансе» на карточке — скрыт сам или из-за автора/работы.
func handleHiddenStatus(d HidingDeps, kind hiding.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		st, err := d.Store.StatusOf(ctx, kind, id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "status failed"})
			return
		}
		writeJSON(w, http.StatusOK, st)
	}
}
