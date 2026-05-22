package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/skriptes/skriptes/backend/internal/auth"
)

// updateMeReq — body PATCH /api/me. Поля опциональны, пустая строка =
// «не менять». Юзер не может менять свою role (это admin-only — см.
// handleUpdateUser).
type updateMeReq struct {
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

// handleUpdateMe — PATCH /api/me. Self-сервис: юзер меняет своё
// display_name / email.
//
// Возвращает обновлённый объект (тот же формат что в /api/auth/me),
// чтобы фронт мог сразу подменить локально без отдельного GET.
func handleUpdateMe(d AuthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		var req updateMeReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		req.Email = strings.TrimSpace(strings.ToLower(req.Email))
		if req.Email != "" && !strings.Contains(req.Email, "@") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid email"})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		updated, err := d.Service.UpdateMe(ctx, u.ID, req.Email, strings.TrimSpace(req.DisplayName))
		switch {
		case errors.Is(err, auth.ErrUserNotFound):
			// Странный кейс: юзер был залогинен но удалён между запросами.
			// Возвращаем 401 чтобы фронт перенаправил на login.
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "user gone"})
			return
		case errors.Is(err, auth.ErrEmailTaken):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "email already taken"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
			return
		}
		// Тот же shape что у /auth/me — meHandler — чтобы фронт мог
		// заменить значение в react-query кэше без дополнительного reshape.
		writeJSON(w, http.StatusOK, map[string]any{
			"user": map[string]any{
				"id":           updated.ID,
				"email":        updated.Email,
				"display_name": updated.DisplayName,
				"role":         string(updated.Role),
				"created_at":   updated.CreatedAt.Format(time.RFC3339),
			},
		})
	}
}

// changeMyPasswordReq — body PATCH /api/me/password.
type changeMyPasswordReq struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// handleChangeMyPassword — PATCH /api/me/password. Юзер сам меняет
// свой пароль с верификацией текущего. Все остальные сессии юзера
// (другие устройства / браузеры) ревоукаются — текущая сохраняется.
func handleChangeMyPassword(d AuthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		var req changeMyPasswordReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		// Текущая сессия — токен из cookie, чтобы её НЕ инвалидировать
		// при смене пароля (юзер не должен выкинуться при изменении из-под себя).
		var keepToken string
		if c, err := r.Cookie(sessionCookieName); err == nil {
			keepToken = c.Value
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		err := d.Service.ChangePassword(ctx, u.ID, req.CurrentPassword, req.NewPassword, keepToken)
		switch {
		case errors.Is(err, auth.ErrUserNotFound):
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "user gone"})
			return
		case errors.Is(err, auth.ErrInvalidPassword):
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "current password is wrong"})
			return
		case errors.Is(err, auth.ErrPasswordTooShort):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password too short"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "change failed"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
