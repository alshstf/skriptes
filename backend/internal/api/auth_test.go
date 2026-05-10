package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/api"
	"github.com/skriptes/skriptes/backend/internal/auth"
	"github.com/skriptes/skriptes/backend/internal/db"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestAuth_FullFlow поднимает реальный postgres + httptest сервер с
// настоящим router'ом и проверяет: bad password → 401, login → 200 + cookie,
// /me с cookie → 200, logout → 204, /me после logout → 401.
func TestAuth_FullFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

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

	// bcryptCost=4 — быстрее тесты, неприемлемо в проде.
	svc := auth.New(pool, 4)
	_, err = svc.CreateUser(ctx, "alice@example.com", "Alice", "correct horse", auth.RoleAdmin)
	require.NoError(t, err)

	router := api.NewRouter(api.Deps{
		Version: "test",
		DB:      pool,
		Auth: api.AuthDeps{
			Service:      svc,
			CookieSecure: false, // httptest — чистый HTTP
		},
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}

	// 1) /api/auth/me без cookie → 401
	{
		resp, err := client.Get(srv.URL + "/api/auth/me")
		require.NoError(t, err)
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		_ = resp.Body.Close()
	}

	// 2) Login с неверным паролем → 401
	{
		resp := postJSON(t, client, srv.URL+"/api/auth/login", map[string]string{
			"email": "alice@example.com", "password": "wrong",
		})
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		_ = resp.Body.Close()
	}

	// 3) Login с верным паролем → 200, cookie проставлен.
	{
		resp := postJSON(t, client, srv.URL+"/api/auth/login", map[string]string{
			"email": "alice@example.com", "password": "correct horse",
		})
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var body struct {
			User auth.User `json:"user"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		_ = resp.Body.Close()
		require.Equal(t, "alice@example.com", body.User.Email)
		require.Equal(t, auth.RoleAdmin, body.User.Role)
		// Сессия в БД действительно создана.
		var cnt int
		require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id = $1`, body.User.ID).Scan(&cnt))
		require.Equal(t, 1, cnt)
	}

	// 4) /api/auth/me с cookie → 200 и тот же пользователь.
	{
		resp, err := client.Get(srv.URL + "/api/auth/me")
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var body struct {
			User auth.User `json:"user"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		_ = resp.Body.Close()
		require.Equal(t, "alice@example.com", body.User.Email)
	}

	// 5) Logout → 204; cookie очищается; в БД сессии больше нет.
	{
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/logout", nil)
		resp, err := client.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusNoContent, resp.StatusCode)
		_ = resp.Body.Close()

		var cnt int
		require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM sessions`).Scan(&cnt))
		require.Equal(t, 0, cnt, "logout должен удалить запись из sessions")
	}

	// 6) /api/auth/me после logout → 401
	{
		resp, err := client.Get(srv.URL + "/api/auth/me")
		require.NoError(t, err)
		require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		_ = resp.Body.Close()
	}
}

// TestOriginCheck — мутирующие запросы с чужого Origin блокируются 403.
func TestOriginCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

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

	svc := auth.New(pool, 4)
	router := api.NewRouter(api.Deps{
		Version: "test",
		DB:      pool,
		Auth: api.AuthDeps{
			Service:        svc,
			CookieSecure:   false,
			AllowedOrigins: []string{"https://skriptes.localhost"},
		},
	})
	srv := httptest.NewServer(router)
	t.Cleanup(srv.Close)

	// POST с чужого Origin → 403, не дойдёт даже до login-handler.
	body, _ := json.Marshal(map[string]string{"email": "x", "password": "y"})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example.com")
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
	_ = resp.Body.Close()

	// POST с корректного Origin → проходит middleware (получит 400 за пустой email или 401, но не 403).
	req, _ = http.NewRequest(http.MethodPost, srv.URL+"/api/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://skriptes.localhost")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.NotEqual(t, http.StatusForbidden, resp.StatusCode,
		"корректный Origin не должен блокироваться")
	_ = resp.Body.Close()
}

func postJSON(t *testing.T, c *http.Client, url string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	require.NoError(t, err)
	return resp
}

// глушим unused-import предупреждения вспомогательных пакетов:
var (
	_ = io.EOF
	_ = strings.Builder{}
)
