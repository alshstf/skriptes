package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/skriptes/skriptes/backend/internal/auth"
)

// adminUserResponse — то что отдаём наружу. Не User напрямую, потому
// что хочется убрать legacy kindle_email (см. Phase 3 — оно теперь в
// отдельной таблице kindle_targets) и контролировать имена ключей.
type adminUserResponse struct {
	ID          int64  `json:"id"`
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Role        string `json:"role"`
	CreatedAt   string `json:"created_at"`
}

func toAdminUserResponse(u auth.User) adminUserResponse {
	return adminUserResponse{
		ID:          u.ID,
		Email:       u.Email,
		DisplayName: u.DisplayName,
		Role:        string(u.Role),
		CreatedAt:   u.CreatedAt.Format(time.RFC3339),
	}
}

// handleListUsers — GET /api/admin/users. Возвращает массив всех
// пользователей, отсортированных по дате создания.
func handleListUsers(d AuthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		users, err := d.Service.ListUsers(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		out := make([]adminUserResponse, 0, len(users))
		for _, u := range users {
			out = append(out, toAdminUserResponse(u))
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": out})
	}
}

// createUserReq — body POST /api/admin/users.
//
// Role опциональна; пустая → auth.RoleUser (безопасный default —
// admin'ом надо стать осознанно). Email и password обязательны.
// display_name опционален: если пустой — handler подставит часть email до '@'.
type createUserReq struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
	Role        string `json:"role"`
}

func handleCreateUser(d AuthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createUserReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		req.Email = strings.TrimSpace(strings.ToLower(req.Email))
		if req.Email == "" || !strings.Contains(req.Email, "@") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid email"})
			return
		}
		role := auth.Role(req.Role)
		if role == "" {
			role = auth.RoleUser
		}
		if role != auth.RoleAdmin && role != auth.RoleUser {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid role"})
			return
		}
		displayName := strings.TrimSpace(req.DisplayName)
		if displayName == "" {
			// strings.Cut вернёт префикс до '@' (если есть; мы выше
			// validate'нули). gocritic'у Cut нравится больше Index'а.
			displayName, _, _ = strings.Cut(req.Email, "@")
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		u, err := d.Service.CreateUser(ctx, req.Email, displayName, req.Password, role)
		switch {
		case errors.Is(err, auth.ErrPasswordTooShort):
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "password too short",
			})
			return
		case errors.Is(err, auth.ErrEmailTaken):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "email already taken"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create failed"})
			return
		}
		writeJSON(w, http.StatusCreated, toAdminUserResponse(u))
	}
}

// updateUserReq — body PATCH /api/admin/users/{id}. Все поля опциональны;
// пустая строка = «не менять». Это важно для частичных обновлений:
// фронт может прислать только {display_name: "..."}, не таская весь объект.
type updateUserReq struct {
	Email       string `json:"email,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
	Role        string `json:"role,omitempty"`
}

func handleUpdateUser(d AuthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := parseUserID(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		var req updateUserReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		req.Email = strings.TrimSpace(strings.ToLower(req.Email))
		if req.Email != "" && !strings.Contains(req.Email, "@") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid email"})
			return
		}
		role := auth.Role(req.Role)
		if req.Role != "" && role != auth.RoleAdmin && role != auth.RoleUser {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid role"})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		u, err := d.Service.UpdateUser(ctx, id, req.Email, strings.TrimSpace(req.DisplayName), role)
		switch {
		case errors.Is(err, auth.ErrUserNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		case errors.Is(err, auth.ErrLastAdmin):
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "cannot demote the last admin",
			})
			return
		case errors.Is(err, auth.ErrEmailTaken):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "email already taken"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
			return
		}
		writeJSON(w, http.StatusOK, toAdminUserResponse(u))
	}
}

// resetPasswordReq — body PATCH /api/admin/users/{id}/password.
type resetPasswordReq struct {
	NewPassword string `json:"new_password"`
}

func handleResetPassword(d AuthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := parseUserID(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		var req resetPasswordReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		err = d.Service.ResetPassword(ctx, id, req.NewPassword)
		switch {
		case errors.Is(err, auth.ErrUserNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		case errors.Is(err, auth.ErrPasswordTooShort):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "password too short"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "reset failed"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleDeleteUser(d AuthDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := parseUserID(r)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		// Защита: удалить самого себя через admin-API запрещено.
		// Иначе админ может случайно деактивировать собственный аккаунт.
		// Полностью удалить себя — только через CLI (skriptes-seed
		// либо прямой DELETE в БД).
		if u, ok := UserFromContext(r.Context()); ok && u.ID == id {
			writeJSON(w, http.StatusConflict, map[string]string{"error": "cannot delete self"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		err = d.Service.DeleteUser(ctx, id)
		switch {
		case errors.Is(err, auth.ErrUserNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
			return
		case errors.Is(err, auth.ErrLastAdmin):
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "cannot delete the last admin",
			})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// parseUserID — общий хелпер для admin-роутов, выдёргивает {id}.
func parseUserID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid user id")
	}
	return id, nil
}
