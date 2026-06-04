package api

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/skriptes/skriptes/backend/internal/adaptations"
	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/metadata"
	"github.com/skriptes/skriptes/backend/internal/settings"
)

// AdaptationsDeps — зависимости для /api/books/{id}/adaptations.
// Service может быть nil — тогда роут не монтируется.
type AdaptationsDeps struct {
	Service *adaptations.Service
}

// handleListAdaptations — GET /api/books/{id}/adaptations.
// Помимо чтения из БД триггерит lazy enrichment (если ещё не пробовали)
// тем же паттерном, что cover/annotation — fire-and-forget goroutine с
// detached контекстом.
func handleListAdaptations(d AdaptationsDeps, books BooksDeps, meta MetadataDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		res, err := d.Service.List(ctx, id)
		if err != nil {
			if errors.Is(err, adaptations.ErrBookNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "book not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}

		// Если enrichment ещё не запускался, дергаем его. Это позволяет
		// эндпоинту самостоятельно служить триггером (фронт может поллить
		// /adaptations без отдельного запроса на /books/{id}).
		if res.EnrichmentStatus == "pending" && books.Service != nil {
			triggerAdaptationsEnrichmentAsync(meta, books.Service, id)
		}

		writeJSON(w, http.StatusOK, res)
	}
}

// adaptationEnrichWanted — нужно ли инициировать ленивое обогащение
// экранизаций: только гейт «Выкл» (в отличие от книг/авторов у триггера нет
// проверки полей — статус pending уже проверен в хендлере). Чистая функция для
// симметрии с bookEnrichTargets/authorEnrichWanted и юнит-теста гейта.
func adaptationEnrichWanted(g settings.EnrichmentGates) bool {
	return !g.AdaptationDisabled
}

// triggerAdaptationsEnrichmentAsync — отдельный триггер (не объединён с
// triggerBookEnrichmentAsync для cover/annotation), потому что:
//
//   - семантически разные операции (cover/annotation быстры, adaptations
//     требует SPARQL-запросов, иногда таймаут до 10-15s);
//   - cover/annotation триггерятся на каждом GET /books/{id}, а
//     adaptations — только на явный запрос /adaptations, чтобы не
//     генерить SPARQL-нагрузку на каждую открытую карточку.
func triggerAdaptationsEnrichmentAsync(d MetadataDeps, svc *books.Service, bookID int64) {
	if d.Service == nil || svc == nil {
		return
	}
	// «Выкл» (gate) для экранизаций — не инициируем новый lazy-фетч.
	// Gates() nil-safe: nil-resolver → ничего не выключено.
	if !adaptationEnrichWanted(d.Gates.Gates()) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		b, err := svc.Get(ctx, bookID)
		if err != nil {
			return
		}
		authors := make([]string, 0, len(b.Authors))
		for _, a := range b.Authors {
			authors = append(authors, a.FullName)
		}
		q := metadata.BookQuery{
			ID:          b.ID,
			Title:       b.Title,
			Authors:     authors,
			Lang:        b.Lang,
			ArchivePath: filepath.Join(d.BooksRoot, b.Archive),
			FB2Name:     b.FileName + "." + b.Ext,
		}
		d.Service.EnsureAdaptations(ctx, q)
	}()
}
