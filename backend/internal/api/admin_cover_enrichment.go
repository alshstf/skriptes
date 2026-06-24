package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/skriptes/skriptes/backend/internal/metadata"
	"github.com/skriptes/skriptes/backend/internal/settings"
)

// coverEnrichmentResponse — конфиг дозаполнения обложек + состояние воркера +
// покрытие cover_path (для админ-страницы).
type coverEnrichmentResponse struct {
	settings.CoverEnrichmentConfig
	metadata.CoverBackfillStatus
	Coverage metadata.CoverCoverage `json:"coverage"`
}

func toCoverBackfillConfig(c settings.CoverEnrichmentConfig) metadata.CoverBackfillConfig {
	return metadata.CoverBackfillConfig{
		OpenLibrary:       c.OpenLibrary,
		GoogleBooks:       c.GoogleBooks,
		WholeCollection:   c.WholeCollection,
		OpenLibraryRPM:    c.OpenLibraryRPM,
		GoogleBooksRPM:    c.GoogleBooksRPM,
		NotFoundRetryDays: c.NotFoundRetryDays,
		ErrorRetryHours:   c.ErrorRetryHours,
	}
}

func coverEnrichmentState(ctx context.Context, d SettingsDeps, cfg settings.CoverEnrichmentConfig) coverEnrichmentResponse {
	resp := coverEnrichmentResponse{CoverEnrichmentConfig: cfg}
	if d.CoverBackfill != nil {
		resp.CoverBackfillStatus = d.CoverBackfill.Status()
		if cov, err := d.CoverBackfill.Coverage(ctx); err == nil {
			resp.Coverage = cov
		}
	}
	return resp
}

// handleGetCoverEnrichment — GET /api/admin/cover-enrichment. Конфиг +
// состояние воркера + покрытие.
func handleGetCoverEnrichment(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		cfg, err := d.Store.CoverEnrichment(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read settings failed"})
			return
		}
		writeJSON(w, http.StatusOK, coverEnrichmentState(ctx, d, cfg))
	}
}

// handleUpdateCoverEnrichment — PUT /api/admin/cover-enrichment. Сохраняет
// конфиг и применяет в рантайме: источники/режим/лимиты/TTL (SetConfig) +
// вкл/выкл фонового воркера (SetEnabled) — без рестарта.
func handleUpdateCoverEnrichment(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var cfg settings.CoverEnrichmentConfig
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&cfg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		if cfg.OpenLibraryRPM < 0 || cfg.GoogleBooksRPM < 0 || cfg.NotFoundRetryDays < 0 || cfg.ErrorRetryHours < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "values must be non-negative"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := d.Store.SetCoverEnrichment(ctx, cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save settings failed"})
			return
		}
		if d.CoverBackfill != nil {
			d.CoverBackfill.SetConfig(toCoverBackfillConfig(cfg))
			d.CoverBackfill.SetEnabled(cfg.Enabled)
		}
		writeJSON(w, http.StatusOK, coverEnrichmentState(ctx, d, cfg))
	}
}

// handleCoverBackfillNow — POST /api/admin/cover-enrichment/run. Разовый
// проход дозаполнения (кнопка «Прогнать разово»), в фоне.
func handleCoverBackfillNow(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.CoverBackfill == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "cover enrichment disabled"})
			return
		}
		d.CoverBackfill.RunOnce()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
	}
}

// handleCoverBackfillStop — POST /api/admin/cover-enrichment/stop. Отменяет
// идущий разовый проход (между батчами).
func handleCoverBackfillStop(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.CoverBackfill == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "cover enrichment disabled"})
			return
		}
		d.CoverBackfill.StopOnce()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "stopping"})
	}
}

// handleResetCoverLookups — POST /api/admin/cover-enrichment/reset-failed. Сброс
// неудачных попыток (not_found/error) → книги перепроверятся на следующем проходе.
func handleResetCoverLookups(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.CoverBackfill == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "cover enrichment disabled"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		n, err := d.CoverBackfill.ResetFailedLookups(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reset failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int64{"reset": n})
	}
}
