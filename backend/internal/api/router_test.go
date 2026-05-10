package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHealthz(t *testing.T) {
	r := NewRouter(Deps{Version: "test"})
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, "ok", body["status"])
}

func TestReadyzWithoutDB(t *testing.T) {
	// Без подключённого пула /readyz всё равно отвечает 200, но с маркером db=disabled.
	// Полный интеграционный тест с реальной БД — в internal/db/migrate_test.go (testcontainers).
	r := NewRouter(Deps{Version: "test"})
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/readyz")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, "disabled", body["db"])
}

func TestVersion(t *testing.T) {
	r := NewRouter(Deps{Version: "v1.2.3"})
	srv := httptest.NewServer(r)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/version")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body map[string]string
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	require.Equal(t, "v1.2.3", body["version"])
}
