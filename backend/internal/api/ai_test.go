package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skriptes/skriptes/backend/internal/auth"
	"github.com/skriptes/skriptes/backend/internal/llm"
	"github.com/skriptes/skriptes/backend/internal/settings"
	"github.com/skriptes/skriptes/backend/internal/testpg"
)

// Настройки ИИ в админке: потолок правится без перезапуска и сразу действует
// на статус пользователя; без ключа статус — «нет ключа».
func TestAdminAI(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	store := settings.New(pool)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	withUser := func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userCtxKey{}, auth.User{ID: 7})))
		})
	}
	status := func(d AIDeps) llm.Status {
		rec := httptest.NewRecorder()
		withUser(handleAIStatus(d)).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, http.StatusOK, rec.Code)
		var st llm.Status
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &st))
		return st
	}

	off := AIDeps{Service: llm.New(llm.Config{}, pool, store, nil, quiet), Store: store}
	require.Equal(t, llm.Status{Reason: llm.ReasonNoKey}, status(off))

	d := AIDeps{Service: llm.New(llm.Config{APIKey: "k", Model: "claude-sonnet-5-5", FastModel: "claude-haiku-5-5",
		DailyPerUser: 20, DefaultBudgetUSD: 10}, pool, store, nil, quiet), Store: store}
	require.Equal(t, llm.Status{Available: true, DailyLeft: 20}, status(d))

	get := func() adminAIResp {
		rec := httptest.NewRecorder()
		handleGetAdminAI(d)(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var out adminAIResp
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		return out
	}
	put := func(body string) adminAIResp {
		rec := httptest.NewRecorder()
		handleUpdateAdminAI(d)(rec, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body)))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		var out adminAIResp
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
		return out
	}

	out := get()
	require.True(t, out.Configured)
	require.InDelta(t, 10, out.BudgetUSD, 1e-9, "пока админ не задал свой — потолок из env")
	require.Equal(t, "claude-sonnet-5-5", out.Model)
	require.Empty(t, out.ByFeature)

	out = put(`{"monthly_budget_usd": 3.5, "disabled_users": [9, 7, 7]}`)
	require.InDelta(t, 3.5, out.BudgetUSD, 1e-9)
	require.Equal(t, []int64{7, 9}, out.DisabledUsers)
	require.Equal(t, llm.ReasonUserDisabled, status(d).Reason, "действует сразу, без перезапуска")

	out = put(`{"monthly_budget_usd": 99999, "disabled": true}`)
	require.InDelta(t, settings.MaxAIBudgetUSD, out.BudgetUSD, 1e-9, "опечатка с лишними нулями обрезается")
	require.True(t, out.Disabled)
	require.Equal(t, llm.ReasonDisabled, status(d).Reason)

	out = put(`{}`)
	require.InDelta(t, 10, out.BudgetUSD, 1e-9, "без своего значения — снова потолок из env")
	require.True(t, status(d).Available)

	rec := httptest.NewRecorder()
	handleUpdateAdminAI(d)(rec, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{`)))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}
