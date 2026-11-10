package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/skriptes/skriptes/backend/internal/llm"
	"github.com/skriptes/skriptes/backend/internal/settings"
)

// AIDeps — ИИ-функции (только при заданном SKRIPTES_LLM_API_KEY); Service ==
// nil → ручки не монтируются.
type AIDeps struct {
	Service *llm.Service
	Store   *settings.Store
}

// handleAIStatus — GET /api/ai/status: доступны ли ИИ-функции этому
// пользователю сейчас (ключ, выключатели, потолок, квота). Фронт по ответу
// показывает или прячет кнопки.
func handleAIStatus(d AIDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		writeJSON(w, http.StatusOK, d.Service.Status(ctx, u.ID))
	}
}

type adminAIResp struct {
	llm.Summary
	Disabled      bool    `json:"disabled"`
	DisabledUsers []int64 `json:"disabled_users"`
	MaxBudgetUSD  float64 `json:"max_budget_usd"`
}

func adminAI(ctx context.Context, d AIDeps) (adminAIResp, error) {
	sum, err := d.Service.Summary(ctx)
	if err != nil {
		return adminAIResp{}, err
	}
	cfg, err := d.Store.AI(ctx)
	if err != nil {
		return adminAIResp{}, err
	}
	return adminAIResp{Summary: sum, Disabled: cfg.Disabled, DisabledUsers: cfg.DisabledUsers, MaxBudgetUSD: settings.MaxAIBudgetUSD}, nil
}

// handleGetAdminAI — GET /api/admin/ai: настройки ИИ и расход за месяц.
func handleGetAdminAI(d AIDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		out, err := adminAI(ctx, d)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read ai settings failed"})
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// handleUpdateAdminAI — PUT /api/admin/ai: месячный потолок и выключатели.
// Применяется сразу (настройки читаются на каждое действие). Ключ и модели —
// только в env.
func handleUpdateAdminAI(d AIDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var cfg settings.AIConfig
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&cfg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := d.Store.SetAI(ctx, cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save ai settings failed"})
			return
		}
		out, err := adminAI(ctx, d)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read ai settings failed"})
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}
