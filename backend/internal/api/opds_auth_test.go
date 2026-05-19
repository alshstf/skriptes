package api_test

import (
	"context"
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
	"github.com/skriptes/skriptes/backend/internal/opds"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestOPDS_BasicAuth — проверяет, что:
//
//  1. /opds/ без credentials → 401 + WWW-Authenticate
//  2. /opds/ с НЕвалидными credentials → 401
//  3. /opds/ с валидными credentials → 200 + Atom XML с правильным
//     Content-Type
//  4. /opds/opensearch.xml тоже за авторизацией (e-reader получает
//     ссылку из root feed и follow-ит её, должно работать с тем же realm)
//
// books/catalog/converter в этом тесте не нужны: Root и OpenSearchDescription
// не читают данные, только формируют статичный navigation feed. Если
// когда-то понадобятся data-handler'ы — поднимем дополнительный meili.
func TestOPDS_BasicAuth(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := startOPDSAuthPostgres(t, ctx)
	authSvc := auth.New(pool, 0)

	const (
		email    = "opds@example.com"
		password = "test-password-1234"
	)
	_, err := authSvc.CreateUser(ctx, email, "OPDS Tester", password, auth.RoleUser)
	require.NoError(t, err)

	// Конструируем router точно так же как production-main, минус
	// books/catalog/converter — для Root они не нужны.
	router := api.NewRouter(api.Deps{
		Auth: api.AuthDeps{Service: authSvc},
		OPDS: api.OPDSDeps{Handler: opds.NewHandler(opds.Config{}, opds.Deps{})},
	})
	srv := httptest.NewServer(router)
	defer srv.Close()

	// 1) без auth → 401 + WWW-Authenticate
	resp, err := http.Get(srv.URL + "/opds/")
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)
	require.Contains(t, resp.Header.Get("WWW-Authenticate"), `Basic realm="skriptes OPDS"`)

	// 2) неверный пароль → 401
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/opds/", nil)
	req.SetBasicAuth(email, "wrong-password")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// 3) неверный email → 401 (timing-mitigation: ValidateCredentials
	// делает фиктивный bcrypt-verify, но снаружи это всё равно 401).
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/opds/", nil)
	req.SetBasicAuth("nobody@example.com", password)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	// 4) валидные credentials → 200 + Atom XML
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/opds/", nil)
	req.SetBasicAuth(email, password)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.Equalf(t, http.StatusOK, resp.StatusCode, "body: %s", body)
	require.Contains(t, resp.Header.Get("Content-Type"), opds.MIMEFeedNavigation)
	bodyStr := string(body)
	require.True(t, strings.HasPrefix(bodyStr, "<?xml"), "must be xml")
	require.Contains(t, bodyStr, "urn:skriptes:opds:root")
	require.Contains(t, bodyStr, "urn:skriptes:opds:recent")
	require.Contains(t, bodyStr, "urn:skriptes:opds:authors")
	require.Contains(t, bodyStr, "urn:skriptes:opds:series")
	require.Contains(t, bodyStr, "urn:skriptes:opds:genres")
	// Search-link должен присутствовать в root feed'е.
	require.Contains(t, bodyStr, `/opds/opensearch.xml`)

	// 5) opensearch.xml тоже за auth, но при валидных creds возвращается
	// корректный OpenSearch document.
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/opds/opensearch.xml", nil)
	req.SetBasicAuth(email, password)
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Contains(t, resp.Header.Get("Content-Type"), opds.MIMEOpenSearch)
	require.Contains(t, string(body), "OpenSearchDescription")
	require.Contains(t, string(body), "/opds/search?q={searchTerms}")
}

func startOPDSAuthPostgres(t *testing.T, ctx context.Context) *pgxpool.Pool {
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
