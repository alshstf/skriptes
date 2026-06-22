package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/skriptes/skriptes/backend/internal/metadata"
	"github.com/skriptes/skriptes/backend/internal/settings"
)

// externalRatingResponse — конфиг дозаполнения внешнего рейтинга + состояние
// воркера + покрытие (для админ-страницы «Фоновые операции»).
type externalRatingResponse struct {
	settings.ExternalRatingConfig
	metadata.ExternalRatingBackfillStatus
	Coverage metadata.ExternalRatingCoverage `json:"coverage"`
}

func toExternalRatingBackfillConfig(c settings.ExternalRatingConfig) metadata.ExternalRatingBackfillConfig {
	return metadata.ExternalRatingBackfillConfig{
		GoogleBooks:       c.GoogleBooks,
		OpenLibrary:       c.OpenLibrary,
		WholeCollection:   c.WholeCollection,
		GoogleBooksRPM:    c.GoogleBooksRPM,
		OpenLibraryRPM:    c.OpenLibraryRPM,
		NotFoundRetryDays: c.NotFoundRetryDays,
		ErrorRetryHours:   c.ErrorRetryHours,
	}
}

func externalRatingState(ctx context.Context, d SettingsDeps, cfg settings.ExternalRatingConfig) externalRatingResponse {
	resp := externalRatingResponse{ExternalRatingConfig: cfg}
	if d.ExternalRating != nil {
		resp.ExternalRatingBackfillStatus = d.ExternalRating.Status()
		if cov, err := d.ExternalRating.Coverage(ctx); err == nil {
			resp.Coverage = cov
		}
	}
	return resp
}

// handleGetExternalRating — GET /api/admin/external-rating. Конфиг + состояние
// воркера + покрытие.
func handleGetExternalRating(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		cfg, err := d.Store.ExternalRating(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read settings failed"})
			return
		}
		writeJSON(w, http.StatusOK, externalRatingState(ctx, d, cfg))
	}
}

// handleUpdateExternalRating — PUT /api/admin/external-rating. Сохраняет конфиг
// и применяет в рантайме: источники/режим/лимиты/TTL (SetConfig) + вкл/выкл
// фонового воркера (SetEnabled) — без рестарта.
func handleUpdateExternalRating(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var cfg settings.ExternalRatingConfig
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&cfg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		if cfg.GoogleBooksRPM < 0 || cfg.OpenLibraryRPM < 0 || cfg.NotFoundRetryDays < 0 || cfg.ErrorRetryHours < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "values must be non-negative"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := d.Store.SetExternalRating(ctx, cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save settings failed"})
			return
		}
		if d.ExternalRating != nil {
			d.ExternalRating.SetConfig(toExternalRatingBackfillConfig(cfg))
			d.ExternalRating.SetEnabled(cfg.Enabled)
		}
		writeJSON(w, http.StatusOK, externalRatingState(ctx, d, cfg))
	}
}

// handleExternalRatingNow — POST /api/admin/external-rating/run. Разовый проход
// дозаполнения (кнопка «Прогнать разово»), в фоне.
func handleExternalRatingNow(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.ExternalRating == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "external rating enrichment disabled"})
			return
		}
		d.ExternalRating.RunOnce()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
	}
}

// handleExternalRatingStop — POST /api/admin/external-rating/stop. Отменяет
// идущий разовый проход (между батчами).
func handleExternalRatingStop(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.ExternalRating == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "external rating enrichment disabled"})
			return
		}
		d.ExternalRating.StopOnce()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "stopping"})
	}
}
