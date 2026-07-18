package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/skriptes/skriptes/backend/internal/catalog"
)

// handleSetAuthorService — PUT /api/admin/authors/{id}/service. Ручная метка
// «служебный автор» (агрегат-псевдоавтор: скрыт из списка /authors) — в обе
// стороны. Пишет is_service_source='manual', чтобы эвристика
// ClassifyServiceAuthors решение не перетирала.
func handleSetAuthorService(d CatalogDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid author id"})
			return
		}
		var body struct {
			IsService bool `json:"is_service"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		if err := d.Service.SetAuthorService(r.Context(), id, body.IsService); err != nil {
			if errors.Is(err, catalog.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "author not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"is_service": body.IsService})
	}
}
