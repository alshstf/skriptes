package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/skriptes/skriptes/backend/internal/metadata"
	"github.com/skriptes/skriptes/backend/internal/settings"
)

// renownResponse — конфиг дозаполнения известности + состояние воркера +
// покрытие (для админ-страницы «Фоновые операции»). Зеркало externalRatingResponse.
type renownResponse struct {
	settings.RenownConfig
	metadata.RenownBackfillStatus
	Coverage metadata.RenownCoverage `json:"coverage"`
}

func toRenownBackfillConfig(c settings.RenownConfig) metadata.RenownBackfillConfig {
	return metadata.RenownBackfillConfig{
		Fantlab:           c.Fantlab,
		OpenLibrary:       c.OpenLibrary,
		Wikidata:          c.Wikidata,
		WholeCollection:   c.WholeCollection,
		FantlabRPM:        c.FantlabRPM,
		OpenLibraryRPM:    c.OpenLibraryRPM,
		WikidataRPM:       c.WikidataRPM,
		FoundRefreshDays:  c.FoundRefreshDays,
		NotFoundRetryDays: c.NotFoundRetryDays,
		ErrorRetryHours:   c.ErrorRetryHours,
	}
}

func renownState(ctx context.Context, d SettingsDeps, cfg settings.RenownConfig) renownResponse {
	resp := renownResponse{RenownConfig: cfg}
	if d.Renown != nil {
		resp.RenownBackfillStatus = d.Renown.Status()
		if cov, err := d.Renown.Coverage(ctx); err == nil {
			resp.Coverage = cov
		}
	}
	return resp
}

// handleGetRenown — GET /api/admin/renown. Конфиг + состояние воркера + покрытие.
func handleGetRenown(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		cfg, err := d.Store.Renown(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read settings failed"})
			return
		}
		writeJSON(w, http.StatusOK, renownState(ctx, d, cfg))
	}
}

// handleUpdateRenown — PUT /api/admin/renown. Сохраняет конфиг и применяет в
// рантайме: источники/охват/лимиты/TTL (SetConfig) + вкл/выкл фонового воркера
// (SetEnabled) — без рестарта.
func handleUpdateRenown(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var cfg settings.RenownConfig
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&cfg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		if cfg.FantlabRPM < 0 || cfg.OpenLibraryRPM < 0 || cfg.WikidataRPM < 0 ||
			cfg.FoundRefreshDays < 0 || cfg.NotFoundRetryDays < 0 || cfg.ErrorRetryHours < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "values must be non-negative"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := d.Store.SetRenown(ctx, cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save settings failed"})
			return
		}
		if d.Renown != nil {
			d.Renown.SetConfig(toRenownBackfillConfig(cfg))
			d.Renown.SetEnabled(cfg.Enabled)
		}
		writeJSON(w, http.StatusOK, renownState(ctx, d, cfg))
	}
}

// handleRenownNow — POST /api/admin/renown/run. Разовый проход (кнопка
// «Прогнать разово»), в фоне.
func handleRenownNow(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Renown == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "renown enrichment disabled"})
			return
		}
		d.Renown.RunOnce()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
	}
}

// handleRenownStop — POST /api/admin/renown/stop. Отменяет идущий разовый
// проход (между батчами).
func handleRenownStop(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Renown == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "renown enrichment disabled"})
			return
		}
		d.Renown.StopOnce()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "stopping"})
	}
}

// handleResetRenownLookups — POST /api/admin/renown/reset-failed. Сброс
// неудачных попыток (not_found/error) → работы перепроверятся на следующем проходе.
func handleResetRenownLookups(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Renown == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "renown enrichment disabled"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		n, err := d.Renown.ResetFailedLookups(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reset failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int64{"reset": n})
	}
}
