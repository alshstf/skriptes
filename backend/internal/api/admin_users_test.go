package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/api"
	"github.com/skriptes/skriptes/backend/internal/auth"
	"github.com/skriptes/skriptes/backend/internal/db"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestAdminEndpoints — end-to-end через HTTP:
//   - admin login → может делать /api/admin/users
//   - user (не admin) → 403 Forbidden
//   - unauthenticated → 401
//   - POST/PATCH/DELETE/reset-password — happy paths + last-admin защита
//   - /api/me + /api/me/password — self-сервис
func TestAdminEndpoints(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startAPIAdminPostgres(t, ctx)
	authSvc := auth.New(pool, 4)

	admin, err := authSvc.CreateUser(ctx, "root@example.com", "Root", "rootpass1234", auth.RoleAdmin)
	require.NoError(t, err)
	regular, err := authSvc.CreateUser(ctx, "user@example.com", "User", "userpass1234", auth.RoleUser)
	require.NoError(t, err)

	router := api.NewRouter(api.Deps{
		Auth: api.AuthDeps{Service: authSvc, AllowedOrigins: []string{"https://test.local"}},
	})
	srv := httptest.NewServer(router)
	defer srv.Close()

	adminCookie := loginAndGetCookie(t, srv.URL, admin.Email, "rootpass1234")
	userCookie := loginAndGetCookie(t, srv.URL, regular.Email, "userpass1234")

	// ── /api/admin/users без auth → 401 ───────────────────────────
	resp := do(t, srv.URL+"/api/admin/users", http.MethodGet, "", nil, "")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// ── /api/admin/users юзером (не админом) → 403 ───────────────
	resp = do(t, srv.URL+"/api/admin/users", http.MethodGet, userCookie, nil, "https://test.local")
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	// ── ListUsers admin'ом ───────────────────────────────────────
	resp = do(t, srv.URL+"/api/admin/users", http.MethodGet, adminCookie, nil, "https://test.local")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var listResp struct {
		Items []struct {
			ID    int64  `json:"id"`
			Email string `json:"email"`
			Role  string `json:"role"`
		} `json:"items"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&listResp))
	require.Len(t, listResp.Items, 2)

	// ── CreateUser admin'ом ─────────────────────────────────────
	resp = do(t, srv.URL+"/api/admin/users", http.MethodPost, adminCookie,
		map[string]any{
			"email":        "new@example.com",
			"display_name": "New",
			"password":     "newpass1234",
			"role":         "user",
		}, "https://test.local")
	require.Equal(t, http.StatusCreated, resp.StatusCode)
	var created struct {
		ID    int64  `json:"id"`
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	require.Equal(t, "new@example.com", created.Email)
	require.Equal(t, "user", created.Role)

	// CreateUser: 409 на дубликат email
	resp = do(t, srv.URL+"/api/admin/users", http.MethodPost, adminCookie,
		map[string]any{"email": "new@example.com", "password": "anotherpass1", "role": "user"},
		"https://test.local")
	require.Equal(t, http.StatusConflict, resp.StatusCode)

	// CreateUser: 400 на короткий пароль
	resp = do(t, srv.URL+"/api/admin/users", http.MethodPost, adminCookie,
		map[string]any{"email": "x@example.com", "password": "short", "role": "user"},
		"https://test.local")
	require.Equal(t, http.StatusBadRequest, resp.StatusCode)

	// ── UpdateUser admin'ом ─────────────────────────────────────
	resp = do(t, srv.URL+"/api/admin/users/"+itoa(created.ID), http.MethodPatch, adminCookie,
		map[string]any{"display_name": "Renamed"}, "https://test.local")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var updated struct {
		DisplayName string `json:"display_name"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&updated))
	require.Equal(t, "Renamed", updated.DisplayName)

	// ── Last-admin защита: PATCH role=user на единственного admin'а ──
	resp = do(t, srv.URL+"/api/admin/users/"+itoa(admin.ID), http.MethodPatch, adminCookie,
		map[string]any{"role": "user"}, "https://test.local")
	require.Equal(t, http.StatusConflict, resp.StatusCode)

	// ── ResetPassword: успех + новые сессии работают ─────────────
	resp = do(t, srv.URL+"/api/admin/users/"+itoa(created.ID)+"/password", http.MethodPatch, adminCookie,
		map[string]any{"new_password": "newpass2025"}, "https://test.local")
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	// Старый пароль больше не залогинит
	resp = doRaw(t, srv.URL+"/api/auth/login", http.MethodPost, "",
		map[string]any{"email": "new@example.com", "password": "newpass1234"},
		"https://test.local")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	// Новый — да
	resp = doRaw(t, srv.URL+"/api/auth/login", http.MethodPost, "",
		map[string]any{"email": "new@example.com", "password": "newpass2025"},
		"https://test.local")
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// ── DeleteUser ──────────────────────────────────────────────
	resp = do(t, srv.URL+"/api/admin/users/"+itoa(created.ID), http.MethodDelete, adminCookie, nil, "https://test.local")
	require.Equal(t, http.StatusNoContent, resp.StatusCode)

	// Delete несуществующего → 404
	resp = do(t, srv.URL+"/api/admin/users/99999", http.MethodDelete, adminCookie, nil, "https://test.local")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)

	// Delete самого себя → 409
	resp = do(t, srv.URL+"/api/admin/users/"+itoa(admin.ID), http.MethodDelete, adminCookie, nil, "https://test.local")
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	body := readBody(t, resp)
	require.Contains(t, body, "cannot delete self")

	// ── Self-сервис: /api/me PATCH ──────────────────────────────
	resp = do(t, srv.URL+"/api/me", http.MethodPatch, userCookie,
		map[string]any{"display_name": "Self Renamed"}, "https://test.local")
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var meResp struct {
		User struct {
			DisplayName string `json:"display_name"`
		} `json:"user"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&meResp))
	require.Equal(t, "Self Renamed", meResp.User.DisplayName)

	// /api/me/password — wrong current → 403
	resp = do(t, srv.URL+"/api/me/password", http.MethodPatch, userCookie,
		map[string]any{"current_password": "wrong", "new_password": "anyvalidpass"},
		"https://test.local")
	require.Equal(t, http.StatusForbidden, resp.StatusCode)

	// /api/me/password — happy path: текущая сессия сохраняется
	resp = do(t, srv.URL+"/api/me/password", http.MethodPatch, userCookie,
		map[string]any{"current_password": "userpass1234", "new_password": "userpass2025"},
		"https://test.local")
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	// Старый пароль не работает
	resp = doRaw(t, srv.URL+"/api/auth/login", http.MethodPost, "",
		map[string]any{"email": "user@example.com", "password": "userpass1234"},
		"https://test.local")
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	// А текущая сессия (userCookie) ВСЁ ЕЩЁ работает — keepSessionToken
	resp = do(t, srv.URL+"/api/auth/me", http.MethodGet, userCookie, nil, "https://test.local")
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// ── helpers ──────────────────────────────────────────────────────

func loginAndGetCookie(t *testing.T, base, email, password string) string {
	t.Helper()
	resp := doRaw(t, base+"/api/auth/login", http.MethodPost, "",
		map[string]any{"email": email, "password": password}, "https://test.local")
	require.Equalf(t, http.StatusOK, resp.StatusCode, "login %s failed", email)
	// Cookie вернулся в Set-Cookie; берём первый.
	for _, c := range resp.Cookies() {
		if c.Name == "skriptes_session" {
			return c.Value
		}
	}
	t.Fatalf("no skriptes_session cookie in login response")
	return ""
}

func do(t *testing.T, url, method, cookie string, body any, origin string) *http.Response {
	t.Helper()
	resp := doRaw(t, url, method, cookie, body, origin)
	return resp
}

func doRaw(t *testing.T, url, method, cookie string, body any, origin string) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		require.NoError(t, err)
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequest(method, url, reader)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: "skriptes_session", Value: cookie})
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, _ := io.ReadAll(resp.Body)
	return strings.TrimSpace(string(b))
}

func itoa(n int64) string { return fmtInt(n) }

func fmtInt(n int64) string {
	if n == 0 {
		return "0"
	}
	out := make([]byte, 0, 20)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		out = append([]byte{byte('0' + n%10)}, out...)
		n /= 10
	}
	if neg {
		out = append([]byte{'-'}, out...)
	}
	return string(out)
}

func startAPIAdminPostgres(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	pgC, err := postgres.Run(ctx,
		"postgres:17-alpine",
		postgres.WithDatabase("skriptes_test"),
		postgres.WithUsername("skriptes"),
		postgres.WithPassword("skriptes"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pgC.Terminate(context.Background()) })
	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, db.Migrate(dsn))
	pool, err := db.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}
