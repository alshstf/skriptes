package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/skriptes/skriptes/backend/internal/settings"
)

// handleGetEnrichmentGates — GET /api/admin/enrichment-gates. Текущие
// «выключатели» lazy-обогащения по типам (режим «Выкл» в админке).
func handleGetEnrichmentGates(r *settings.EnrichmentGateResolver) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, r.Gates())
	}
}

// handleUpdateEnrichmentGates — PUT /api/admin/enrichment-gates. Сохраняет
// «выключатели» и живо обновляет кэш (применяется без рестарта). «Выкл» не
// стирает уже сохранённые данные — лишь перестаёт инициировать новые lazy-фетчи.
func handleUpdateEnrichmentGates(r *settings.EnrichmentGateResolver) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		var cfg settings.EnrichmentGates
		if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 1024)).Decode(&cfg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		ctx, cancel := context.WithTimeout(req.Context(), 5*time.Second)
		defer cancel()
		if err := r.SetGates(ctx, cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save enrichment gates failed"})
			return
		}
		writeJSON(w, http.StatusOK, r.Gates())
	}
}
