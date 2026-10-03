package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/api"
	"github.com/skriptes/skriptes/backend/internal/auth"
	"github.com/skriptes/skriptes/backend/internal/email"
	"github.com/skriptes/skriptes/backend/internal/kindle"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestKindleTargets_Sender — /api/me/kindle-targets отдаёт адрес отправителя:
// его пользователь добавляет в «Утверждённые отправители» Amazon, поэтому он
// обязан совпадать с заголовком From писем (SKRIPTES_SMTP_FROM, иначе логин SMTP).
func TestKindleTargets_Sender(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	authSvc := auth.New(pool, 4)
	_, err := authSvc.CreateUser(ctx, "reader@example.com", "Reader", "readerpass1234", auth.RoleUser)
	require.NoError(t, err)

	cases := []struct {
		name   string
		sender *email.Sender
		want   string
	}{
		{"From задан", email.New(email.Config{Host: "smtp.example.com", Port: 587,
			User: "login@example.com", From: "books@example.com"}, nil), "books@example.com"},
		{"From пуст — логин SMTP", email.New(email.Config{Host: "smtp.example.com", Port: 587,
			User: "login@example.com"}, nil), "login@example.com"},
		{"SMTP не настроен", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := api.NewRouter(api.Deps{
				Auth:   api.AuthDeps{Service: authSvc, AllowedOrigins: []string{"https://test.local"}},
				Kindle: api.KindleDeps{Service: kindle.New(pool), Email: tc.sender},
			})
			srv := httptest.NewServer(router)
			defer srv.Close()

			cookie := loginAndGetCookie(t, srv.URL, "reader@example.com", "readerpass1234")
			resp := do(t, srv.URL+"/api/me/kindle-targets", http.MethodGet, cookie, nil, "")
			require.Equal(t, http.StatusOK, resp.StatusCode)
			var body struct {
				Items  []json.RawMessage `json:"items"`
				Sender string            `json:"sender"`
			}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
			require.Equal(t, tc.want, body.Sender)
		})
	}
}
