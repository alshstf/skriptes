package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/skriptes/skriptes/backend/internal/metadata"
	"github.com/skriptes/skriptes/backend/internal/settings"
)

// srcLangEnrichmentResponse — конфиг дозаполнения языка оригинала + состояние
// воркера + покрытие src_lang (для админ-страницы). Зеркало year-enrichment.
type srcLangEnrichmentResponse struct {
	settings.SrcLangEnrichmentConfig
	metadata.SrcLangBackfillStatus
	Coverage metadata.SrcLangCoverage `json:"coverage"`
}

func toSrcLangBackfillConfig(c settings.SrcLangEnrichmentConfig) metadata.SrcLangBackfillConfig {
	return metadata.SrcLangBackfillConfig{
		Wikidata:          c.Wikidata,
		WholeCollection:   c.WholeCollection,
		WikidataRPM:       c.WikidataRPM,
		NotFoundRetryDays: c.NotFoundRetryDays,
		ErrorRetryHours:   c.ErrorRetryHours,
	}
}

func srcLangEnrichmentState(ctx context.Context, d SettingsDeps, cfg settings.SrcLangEnrichmentConfig) srcLangEnrichmentResponse {
	resp := srcLangEnrichmentResponse{SrcLangEnrichmentConfig: cfg}
	if d.SrcLangBackfill != nil {
		resp.SrcLangBackfillStatus = d.SrcLangBackfill.Status()
		if cov, err := d.SrcLangBackfill.Coverage(ctx); err == nil {
			resp.Coverage = cov
		}
	}
	return resp
}

// handleGetSrcLangEnrichment — GET /api/admin/src-lang-enrichment.
func handleGetSrcLangEnrichment(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		cfg, err := d.Store.SrcLangEnrichment(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read settings failed"})
			return
		}
		writeJSON(w, http.StatusOK, srcLangEnrichmentState(ctx, d, cfg))
	}
}

// handleUpdateSrcLangEnrichment — PUT /api/admin/src-lang-enrichment. Сохраняет
// конфиг и применяет в рантайме (SetConfig + SetEnabled) без рестарта.
func handleUpdateSrcLangEnrichment(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var cfg settings.SrcLangEnrichmentConfig
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&cfg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		if cfg.WikidataRPM < 0 || cfg.NotFoundRetryDays < 0 || cfg.ErrorRetryHours < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "values must be non-negative"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := d.Store.SetSrcLangEnrichment(ctx, cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save settings failed"})
			return
		}
		if d.SrcLangBackfill != nil {
			d.SrcLangBackfill.SetConfig(toSrcLangBackfillConfig(cfg))
			d.SrcLangBackfill.SetEnabled(cfg.Enabled)
		}
		writeJSON(w, http.StatusOK, srcLangEnrichmentState(ctx, d, cfg))
	}
}

// handleSrcLangBackfillNow — POST /api/admin/src-lang-enrichment/run.
func handleSrcLangBackfillNow(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.SrcLangBackfill == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "src_lang enrichment disabled"})
			return
		}
		d.SrcLangBackfill.RunOnce()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
	}
}

// handleSrcLangBackfillStop — POST /api/admin/src-lang-enrichment/stop.
func handleSrcLangBackfillStop(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.SrcLangBackfill == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "src_lang enrichment disabled"})
			return
		}
		d.SrcLangBackfill.StopOnce()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "stopping"})
	}
}

// handleResetSrcLangLookups — POST /api/admin/src-lang-enrichment/reset-failed.
func handleResetSrcLangLookups(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.SrcLangBackfill == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "src_lang enrichment disabled"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		n, err := d.SrcLangBackfill.ResetFailedLookups(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reset failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int64{"reset": n})
	}
}
