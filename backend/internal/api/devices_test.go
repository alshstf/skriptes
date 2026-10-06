package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/api"
	"github.com/skriptes/skriptes/backend/internal/auth"
	"github.com/skriptes/skriptes/backend/internal/opds"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestDevicePasswords — пароли устройств (#389, B2): создаются в профиле
// (пароль — один раз), входят в OPDS вместо основного; основной пароль — только
// из доверенных сетей; отозванный не входит; подбор упирается в общий лимит.
func TestDevicePasswords(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	svc := auth.New(pool, 4)
	const email, password = "reader@example.com", "correct horse battery"
	_, err := svc.CreateUser(ctx, email, "Reader", password, auth.RoleUser)
	require.NoError(t, err)
	_, err = svc.CreateUser(ctx, "other@example.com", "Other", password, auth.RoleUser)
	require.NoError(t, err)

	newServer := func(nets []netip.Prefix, limit int) *httptest.Server {
		srv := httptest.NewServer(api.NewRouter(api.Deps{
			Version: "test", DB: pool,
			Auth: api.AuthDeps{Service: svc, MainPasswordNets: nets, LoginRateLimitIP: limit, LoginRateLimitEmail: 100},
			OPDS: api.OPDSDeps{Handler: opds.NewHandler(opds.Config{}, opds.Deps{})},
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	opdsStatus := func(srv *httptest.Server, user, pass string) int {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/opds/", nil)
		req.SetBasicAuth(user, pass)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	// Основной пароль разрешён только из 192.168.0.0/24 — тест ходит с 127.0.0.1.
	srv := newServer([]netip.Prefix{netip.MustParsePrefix("192.168.0.0/24")}, 0)

	// Пароль устройства — из профиля (сессия).
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second}
	resp := postJSON(t, client, srv.URL+"/api/auth/login", map[string]string{"email": email, "password": password})
	require.Equal(t, http.StatusOK, resp.StatusCode, "веб-вход основным паролем не ограничен сетью")
	_ = resp.Body.Close()
	resp = postJSON(t, client, srv.URL+"/api/me/devices", map[string]string{"name": "KOReader"})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var created struct {
		Device   auth.Device `json:"device"`
		Password string      `json:"password"`
		Login    string      `json:"login"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	_ = resp.Body.Close()
	require.Len(t, created.Password, 16)
	require.Equal(t, email, created.Login)
	require.Equal(t, "KOReader", created.Device.Name)

	require.Equal(t, http.StatusUnauthorized, opdsStatus(srv, email, password), "основной пароль снаружи — нет")
	require.Equal(t, http.StatusOK, opdsStatus(srv, email, created.Password), "пароль устройства — да")
	spaced := strings.ToUpper(created.Password[:4] + " " + created.Password[4:8] + "-" + created.Password[8:])
	require.Equal(t, http.StatusOK, opdsStatus(srv, email, spaced), "группы и регистр не мешают")
	require.Equal(t, http.StatusUnauthorized, opdsStatus(srv, "other@example.com", created.Password), "чужой пароль устройства")

	devices, err := svc.ListDevices(ctx, mustUserID(t, ctx, svc, email))
	require.NoError(t, err)
	require.Len(t, devices, 1)
	require.NotNil(t, devices[0].LastUsedAt, "видно последнее использование")

	// Из доверенной сети основной пароль работает, как раньше.
	lan := newServer([]netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}, 0)
	require.Equal(t, http.StatusOK, opdsStatus(lan, email, password))

	// Отзыв.
	req, _ := http.NewRequestWithContext(ctx, http.MethodDelete, srv.URL+"/api/me/devices/"+itoa(created.Device.ID), nil)
	resp, err = client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.Equal(t, http.StatusUnauthorized, opdsStatus(srv, email, created.Password), "отозванный не входит")

	// Подбор: три неудачи с адреса — дальше 429 даже с верным паролем устройства.
	_, fresh, err := svc.CreateDevice(ctx, mustUserID(t, ctx, svc, email), "Readest")
	require.NoError(t, err)
	limited := newServer(nil, 3)
	for i := 0; i < 3; i++ {
		require.Equal(t, http.StatusUnauthorized, opdsStatus(limited, email, "aaaabbbbccccdddd"))
	}
	require.Equal(t, http.StatusTooManyRequests, opdsStatus(limited, email, fresh))
}

func mustUserID(t *testing.T, ctx context.Context, svc *auth.Service, email string) int64 {
	t.Helper()
	u, err := svc.ValidateCredentials(ctx, email, "correct horse battery")
	require.NoError(t, err)
	return u.ID
}
