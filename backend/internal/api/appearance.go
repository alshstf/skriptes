package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/skriptes/skriptes/backend/internal/settings"
)

// handleGetMeAppearance — GET /api/me/appearance. Персональные настройки
// внешнего вида (стиль жанровых меток и т.п.). Нет оверрайда → дефолт.
func handleGetMeAppearance(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		cfg, err := d.Store.UserAppearance(ctx, u.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read appearance failed"})
			return
		}
		writeJSON(w, http.StatusOK, cfg)
	}
}

// handleUpdateMeAppearance — PUT /api/me/appearance. Сохраняет персональные
// настройки внешнего вида.
func handleUpdateMeAppearance(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		var cfg settings.AppearanceConfig
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&cfg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := d.Store.SetUserAppearance(ctx, u.ID, cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save appearance failed"})
			return
		}
		saved, err := d.Store.UserAppearance(ctx, u.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read appearance failed"})
			return
		}
		writeJSON(w, http.StatusOK, saved)
	}
}
