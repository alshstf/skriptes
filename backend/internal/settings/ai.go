package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
)

const aiKey = "ai"

// MaxAIBudgetUSD — верхняя граница месячного потолка в настройках: защита от
// опечатки в поле (лишний ноль), не рекомендация.
const MaxAIBudgetUSD = 1000

// AIConfig — рантайм-настройки ИИ-функций (админка → «Общее» → «ИИ»). Сам ключ
// модели — только в env (SKRIPTES_LLM_API_KEY); без него эти настройки ни на
// что не влияют.
type AIConfig struct {
	// MonthlyBudgetUSD — потолок расходов инстанса за календарный месяц.
	// Исчерпан — ИИ-функции скрыты до следующего месяца. nil — дефолт из env
	// (SKRIPTES_LLM_MONTHLY_BUDGET_USD).
	MonthlyBudgetUSD *float64 `json:"monthly_budget_usd"`
	// Disabled — выключить все ИИ-функции, не убирая ключ.
	Disabled bool `json:"disabled"`
	// DisabledUsers — пользователи, которым ИИ-функции выключены.
	DisabledUsers []int64 `json:"disabled_users"`
}

func (c *AIConfig) normalize() {
	if c.MonthlyBudgetUSD != nil {
		b := min(max(*c.MonthlyBudgetUSD, 0), MaxAIBudgetUSD)
		c.MonthlyBudgetUSD = &b
	}
	slices.Sort(c.DisabledUsers)
	c.DisabledUsers = slices.Compact(c.DisabledUsers)
	if c.DisabledUsers == nil {
		c.DisabledUsers = []int64{}
	}
}

// AI читает настройки ИИ-функций; нет записи — всё по умолчанию.
func (s *Store) AI(ctx context.Context) (AIConfig, error) {
	var cfg AIConfig
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, aiKey).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		cfg.normalize()
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read ai settings: %w", err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return AIConfig{DisabledUsers: []int64{}}, fmt.Errorf("decode ai settings: %w", err)
	}
	cfg.normalize()
	return cfg, nil
}

// SetAI сохраняет настройки ИИ-функций (upsert).
func (s *Store) SetAI(ctx context.Context, cfg AIConfig) error {
	cfg.normalize()
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode ai settings: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, aiKey, raw)
	if err != nil {
		return fmt.Errorf("save ai settings: %w", err)
	}
	return nil
}
