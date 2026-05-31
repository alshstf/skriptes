package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/skriptes/skriptes/backend/internal/metadata"
	"github.com/skriptes/skriptes/backend/internal/settings"
)

// YearReindexer пересинхронизирует Meili-поле year из books.written_year
// (реализуется *importer.Importer). Нужно, т.к. год наполняется обогащением
// ПОСЛЕ импорта, а фильтр/сортировка/фасет «Год» на /books работают по Meili.
type YearReindexer interface {
	ResyncYears(ctx context.Context) (int, error)
}

// yearEnrichmentResponse — конфиг дозаполнения года + состояние воркера +
// покрытие written_year по источникам (для админ-страницы).
type yearEnrichmentResponse struct {
	settings.YearEnrichmentConfig
	metadata.YearBackfillStatus
	Coverage metadata.YearCoverage `json:"coverage"`
}

func toYearBackfillConfig(c settings.YearEnrichmentConfig) metadata.YearBackfillConfig {
	return metadata.YearBackfillConfig{
		OpenLibrary:       c.OpenLibrary,
		Wikidata:          c.Wikidata,
		OpenLibraryRPM:    c.OpenLibraryRPM,
		WikidataRPM:       c.WikidataRPM,
		NotFoundRetryDays: c.NotFoundRetryDays,
		ErrorRetryHours:   c.ErrorRetryHours,
	}
}

func yearEnrichmentState(ctx context.Context, d SettingsDeps, cfg settings.YearEnrichmentConfig) yearEnrichmentResponse {
	resp := yearEnrichmentResponse{YearEnrichmentConfig: cfg}
	if d.YearBackfill != nil {
		resp.YearBackfillStatus = d.YearBackfill.Status()
		if cov, err := d.YearBackfill.Coverage(ctx); err == nil {
			resp.Coverage = cov
		}
	}
	return resp
}

// handleGetYearEnrichment — GET /api/admin/year-enrichment. Конфиг +
// состояние воркера + покрытие.
func handleGetYearEnrichment(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		cfg, err := d.Store.YearEnrichment(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read settings failed"})
			return
		}
		writeJSON(w, http.StatusOK, yearEnrichmentState(ctx, d, cfg))
	}
}

// handleUpdateYearEnrichment — PUT /api/admin/year-enrichment. Сохраняет
// конфиг и применяет в рантайме: лимиты/источники/TTL (SetConfig) +
// вкл/выкл фонового воркера (SetEnabled) — без рестарта.
func handleUpdateYearEnrichment(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var cfg settings.YearEnrichmentConfig
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&cfg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		if cfg.OpenLibraryRPM < 0 || cfg.WikidataRPM < 0 || cfg.NotFoundRetryDays < 0 || cfg.ErrorRetryHours < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "values must be non-negative"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := d.Store.SetYearEnrichment(ctx, cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save settings failed"})
			return
		}
		if d.YearBackfill != nil {
			d.YearBackfill.SetConfig(toYearBackfillConfig(cfg))
			d.YearBackfill.SetEnabled(cfg.Enabled)
		}
		writeJSON(w, http.StatusOK, yearEnrichmentState(ctx, d, cfg))
	}
}

// handleYearBackfillNow — POST /api/admin/year-enrichment/run. Разовый
// проход дозаполнения (кнопка «Запустить сейчас»), в фоне.
func handleYearBackfillNow(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.YearBackfill == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "year enrichment disabled"})
			return
		}
		d.YearBackfill.RunOnce()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
	}
}

// handleYearBackfillStop — POST /api/admin/year-enrichment/stop. Отменяет
// идущий разовый проход (между батчами).
func handleYearBackfillStop(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.YearBackfill == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "year enrichment disabled"})
			return
		}
		d.YearBackfill.StopOnce()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "stopping"})
	}
}

// handleResyncYears — POST /api/admin/year-enrichment/reindex. Пере-проставляет
// Meili-поле year из written_year для всех книг (после обогащения). Синхронно
// (batch partial-update), щедрый таймаут.
func handleResyncYears(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Reindex == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "reindex unavailable"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()
		n, err := d.Reindex.ResyncYears(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "resync failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"synced": n})
	}
}
