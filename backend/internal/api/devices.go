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
)

// Пароли устройств (#389, B2) — для OPDS и синхронизации читалок.

// handleListDevices — GET /api/me/devices.
func handleListDevices(d AuthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		items, err := d.Service.ListDevices(ctx, u.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// handleCreateDevice — POST /api/me/devices {name}: пароль в ответе — один раз.
func handleCreateDevice(d AuthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		var req struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		dev, password, err := d.Service.CreateDevice(ctx, u.ID, req.Name)
		switch {
		case errors.Is(err, auth.ErrTooManyDevices):
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "паролей устройств уже " + strconv.Itoa(auth.MaxDevicesPerUser) + " — отзовите ненужные"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"device": dev, "password": password, "login": u.Email})
	}
}

// handleDeleteDevice — DELETE /api/me/devices/{id}: отозвать пароль.
func handleDeleteDevice(d AuthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		switch err := d.Service.DeleteDevice(ctx, u.ID, id); {
		case errors.Is(err, auth.ErrDeviceNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}
}
