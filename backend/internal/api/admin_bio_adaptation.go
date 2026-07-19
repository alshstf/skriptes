package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/skriptes/skriptes/backend/internal/metadata"
	"github.com/skriptes/skriptes/backend/internal/settings"
)

// bioAdaptationResponse — конфиг + состояние обоих воркеров + покрытие.
// Поля статусов плоские (НЕ embed двух Status-структов: у обоих поля
// Running/Mode → коллизия промотированных имён, encoding/json выкинул бы их).
type bioAdaptationResponse struct {
	settings.BioAdaptationConfig
	BiosRunning        bool                        `json:"bios_running"`
	BiosMode           string                      `json:"bios_mode"`
	AdaptationsRunning bool                        `json:"adaptations_running"`
	AdaptationsMode    string                      `json:"adaptations_mode"`
	BioCoverage        metadata.AuthorCoverage     `json:"bio_coverage"`
	AdaptationCoverage metadata.AdaptationCoverage `json:"adaptation_coverage"`
}

func bioAdaptationState(ctx context.Context, d SettingsDeps, cfg settings.BioAdaptationConfig) bioAdaptationResponse {
	resp := bioAdaptationResponse{BioAdaptationConfig: cfg}
	if d.AuthorBackfill != nil {
		st := d.AuthorBackfill.Status()
		resp.BiosRunning, resp.BiosMode = st.Running, st.Mode
		if cov, err := d.AuthorBackfill.Coverage(ctx); err == nil {
			resp.BioCoverage = cov
		}
	}
	if d.AdaptationBackfill != nil {
		st := d.AdaptationBackfill.Status()
		resp.AdaptationsRunning, resp.AdaptationsMode = st.Running, st.Mode
		if cov, err := d.AdaptationBackfill.Coverage(ctx); err == nil {
			resp.AdaptationCoverage = cov
		}
	}
	return resp
}

// handleGetBioAdaptation — GET /api/admin/bio-adaptation-enrichment.
func handleGetBioAdaptation(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		cfg, err := d.Store.BioAdaptation(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read settings failed"})
			return
		}
		writeJSON(w, http.StatusOK, bioAdaptationState(ctx, d, cfg))
	}
}

// handleUpdateBioAdaptation — PUT /api/admin/bio-adaptation-enrichment.
// Сохраняет конфиг и применяет в рантайме оба воркера (rpm + вкл/выкл).
func handleUpdateBioAdaptation(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var cfg settings.BioAdaptationConfig
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&cfg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		if cfg.BiosRPM < 0 || cfg.AdaptationsRPM < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "values must be non-negative"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := d.Store.SetBioAdaptation(ctx, cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save settings failed"})
			return
		}
		if d.AuthorBackfill != nil {
			d.AuthorBackfill.SetConfig(cfg.BiosRPM)
			d.AuthorBackfill.SetEnabled(cfg.Bios)
		}
		if d.AdaptationBackfill != nil {
			d.AdaptationBackfill.SetConfig(cfg.AdaptationsRPM)
			d.AdaptationBackfill.SetEnabled(cfg.Adaptations)
		}
		// Per-source тумблер TMDB-постеров — применяется в рантайме без рестарта.
		if d.Metadata != nil {
			d.Metadata.SetTMDBPostersEnabled(cfg.TMDBPosters)
		}
		writeJSON(w, http.StatusOK, bioAdaptationState(ctx, d, cfg))
	}
}

// handleBioBackfillNow — POST .../bios/run. Разовый проход воркера биографий.
func handleBioBackfillNow(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.AuthorBackfill == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "bio enrichment disabled"})
			return
		}
		d.AuthorBackfill.RunOnce()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
	}
}

// handleBioBackfillStop — POST .../bios/stop.
func handleBioBackfillStop(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.AuthorBackfill == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "bio enrichment disabled"})
			return
		}
		d.AuthorBackfill.StopOnce()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "stopping"})
	}
}

// handleAdaptationBackfillNow — POST .../adaptations/run.
func handleAdaptationBackfillNow(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.AdaptationBackfill == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "adaptation enrichment disabled"})
			return
		}
		d.AdaptationBackfill.RunOnce()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
	}
}

// handleAdaptationBackfillStop — POST .../adaptations/stop.
func handleAdaptationBackfillStop(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.AdaptationBackfill == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "adaptation enrichment disabled"})
			return
		}
		d.AdaptationBackfill.StopOnce()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "stopping"})
	}
}
