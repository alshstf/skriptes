// Package llm — единственное место, откуда skriptes в рантайме ходит к
// языковой модели (Anthropic API).
//
// Правило проекта: модель в рантайме — только по ключу владельца
// (SKRIPTES_LLM_API_KEY; пусто — ИИ-функций нет вовсе) и только через этот
// пакет. Здесь же всё, что должно быть общим для любой ИИ-функции: суточная
// квота пользователя, месячный потолок расходов инстанса, учёт токенов и
// стоимости (таблица llm_usage), метрики, разбор отказов модели.
// План — ~/projects/plans/skriptes/ai-roadmap.md, этап 0.
package llm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/skriptes/skriptes/backend/internal/metrics"
	"github.com/skriptes/skriptes/backend/internal/settings"
)

// Config — настройки из env.
type Config struct {
	APIKey    string
	BaseURL   string // пусто — api.anthropic.com; для тестов и прокси
	Model     string // ответы читателю, суждения
	FastModel string // дешёвые операции: перевод, толкование, пересказы
	// DailyPerUser — действий пользователя в сутки (не вызовов модели: одно
	// действие может стоить нескольких вызовов).
	DailyPerUser int
	// DefaultBudgetUSD — месячный потолок, пока админ не задал свой.
	DefaultBudgetUSD float64
}

// Причины, по которым ИИ-функции сейчас недоступны пользователю.
const (
	ReasonNoKey        = "no_key"        // ключ не задан — функций нет
	ReasonDisabled     = "disabled"      // выключено админом целиком
	ReasonUserDisabled = "user_disabled" // выключено этому пользователю
	ReasonBudget       = "budget"        // месячный потолок расходов исчерпан
	ReasonQuota        = "quota"         // суточная квота пользователя исчерпана
)

// Unavailable — ИИ-функция недоступна; Reason — одна из констант Reason*.
type Unavailable struct{ Reason string }

func (e *Unavailable) Error() string { return "llm unavailable: " + e.Reason }

// Ошибки вызова модели.
var (
	// ErrRefused — модель отказалась отвечать (классификаторы безопасности).
	ErrRefused = errors.New("llm: model refused")
	// ErrUpstream — сбой провайдера или сети: можно повторить позже.
	ErrUpstream = errors.New("llm: upstream error")
	// ErrAuth — ключ не подходит (401/403): чинит владелец.
	ErrAuth = errors.New("llm: api key rejected")
)

// SettingsStore — рантайм-настройки ИИ (settings.Store).
type SettingsStore interface {
	AI(ctx context.Context) (settings.AIConfig, error)
}

// Service — вызовы модели с квотой, потолком и учётом. Нулевое значение и nil
// безопасны: Configured() == false.
type Service struct {
	cfg      Config
	pool     *pgxpool.Pool
	settings SettingsStore
	client   anthropic.Client
	logger   *slog.Logger
}

// New создаёт сервис. httpClient — транспорт (в приложении — с прерывателем
// внешних источников); nil — http.DefaultClient.
func New(cfg Config, pool *pgxpool.Pool, store SettingsStore, httpClient *http.Client, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Service{cfg: cfg, pool: pool, settings: store, logger: logger}
	if cfg.APIKey == "" {
		return s
	}
	opts := []option.RequestOption{option.WithAPIKey(cfg.APIKey)}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	if httpClient != nil {
		opts = append(opts, option.WithHTTPClient(httpClient))
	}
	s.client = anthropic.NewClient(opts...)
	return s
}

// Configured — ключ задан.
func (s *Service) Configured() bool { return s != nil && s.cfg.APIKey != "" }

// Models — модели из конфига: основная и быстрая.
func (s *Service) Models() (model, fast string) { return s.cfg.Model, s.cfg.FastModel }

// Status — доступны ли ИИ-функции пользователю прямо сейчас.
type Status struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	// DailyLeft — сколько действий осталось сегодня (при Available).
	DailyLeft int `json:"daily_left"`
}

// Status проверяет ключ, выключатели, потолок и квоту. Ошибка чтения базы —
// «недоступно» (не тратим деньги вслепую).
func (s *Service) Status(ctx context.Context, userID int64) Status {
	if !s.Configured() {
		return Status{Reason: ReasonNoKey}
	}
	cfg, err := s.settings.AI(ctx)
	if err != nil {
		s.logger.Warn("llm: read settings failed", "err", err)
		return Status{Reason: ReasonDisabled}
	}
	switch {
	case cfg.Disabled:
		return Status{Reason: ReasonDisabled}
	case slices.Contains(cfg.DisabledUsers, userID):
		return Status{Reason: ReasonUserDisabled}
	}
	spent, err := s.spentThisMonth(ctx)
	if err != nil {
		s.logger.Warn("llm: read spend failed", "err", err)
		return Status{Reason: ReasonBudget}
	}
	if spent >= usdToMicro(s.budget(cfg)) {
		return Status{Reason: ReasonBudget}
	}
	used, err := s.actionsToday(ctx, userID)
	if err != nil {
		s.logger.Warn("llm: read quota failed", "err", err)
		return Status{Reason: ReasonQuota}
	}
	if left := s.cfg.DailyPerUser - used; left > 0 {
		return Status{Available: true, DailyLeft: left}
	}
	return Status{Reason: ReasonQuota}
}

// Begin — проверка перед действием пользователя: nil — можно; *Unavailable — нет.
func (s *Service) Begin(ctx context.Context, userID int64) error {
	if st := s.Status(ctx, userID); !st.Available {
		return &Unavailable{Reason: st.Reason}
	}
	return nil
}

func (s *Service) budget(cfg settings.AIConfig) float64 {
	if cfg.MonthlyBudgetUSD != nil {
		return *cfg.MonthlyBudgetUSD
	}
	return s.cfg.DefaultBudgetUSD
}

func (s *Service) spentThisMonth(ctx context.Context) (int64, error) {
	var micro int64
	err := s.pool.QueryRow(ctx,
		`SELECT COALESCE(sum(cost_microusd), 0) FROM llm_usage WHERE at >= date_trunc('month', now())`).Scan(&micro)
	return micro, err
}

func (s *Service) actionsToday(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM llm_usage WHERE user_id = $1 AND counted AND at >= date_trunc('day', now())`, userID).Scan(&n)
	return n, err
}

// FeatureSpend — расход по одной функции за текущий месяц.
type FeatureSpend struct {
	Feature  string  `json:"feature"`
	Requests int     `json:"requests"`
	Actions  int     `json:"actions"`
	USD      float64 `json:"usd"`
}

// Summary — сводка для админки.
type Summary struct {
	Configured   bool           `json:"configured"`
	Model        string         `json:"model"`
	FastModel    string         `json:"fast_model"`
	DailyPerUser int            `json:"daily_per_user"`
	BudgetUSD    float64        `json:"budget_usd"`
	SpentUSD     float64        `json:"spent_usd"`
	ByFeature    []FeatureSpend `json:"by_feature"`
}

// Summary — потолок, расход за месяц и разбивка по функциям.
func (s *Service) Summary(ctx context.Context) (Summary, error) {
	out := Summary{ByFeature: []FeatureSpend{}}
	if s == nil {
		return out, nil
	}
	out.Configured = s.Configured()
	out.Model, out.FastModel, out.DailyPerUser = s.cfg.Model, s.cfg.FastModel, s.cfg.DailyPerUser
	cfg, err := s.settings.AI(ctx)
	if err != nil {
		return out, err
	}
	out.BudgetUSD = s.budget(cfg)
	rows, err := s.pool.Query(ctx, `
		SELECT feature, count(*), count(*) FILTER (WHERE counted), COALESCE(sum(cost_microusd), 0)
		FROM llm_usage WHERE at >= date_trunc('month', now())
		GROUP BY feature ORDER BY 4 DESC, feature`)
	if err != nil {
		return out, fmt.Errorf("llm usage by feature: %w", err)
	}
	defer rows.Close()
	var total int64
	for rows.Next() {
		var f FeatureSpend
		var micro int64
		if err := rows.Scan(&f.Feature, &f.Requests, &f.Actions, &micro); err != nil {
			return out, err
		}
		f.USD = microToUSD(micro)
		total += micro
		out.ByFeature = append(out.ByFeature, f)
	}
	out.SpentUSD = microToUSD(total)
	if b := out.BudgetUSD; b > 0 {
		metrics.SetLLMBudgetUsed(out.SpentUSD / b)
	}
	return out, rows.Err()
}

// usageRow — строка учёта одного вызова.
type usageRow struct {
	userID  int64
	feature string
	model   string
	stage   int
	counted bool
	outcome string
	usage   Usage
}

func (s *Service) record(ctx context.Context, r usageRow) int64 {
	cost := Cost(r.model, r.usage)
	metrics.LLMRequest(r.feature, r.model, r.outcome, r.usage.Input, r.usage.Output, r.usage.CacheRead, r.usage.CacheWrite, microToUSD(cost))
	// Запись учёта не должна теряться из-за отменённого запроса читателя:
	// деньги уже потрачены.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	var uid *int64
	if r.userID > 0 {
		uid = &r.userID
	}
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO llm_usage (user_id, feature, model, stage, counted, outcome,
			input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_microusd)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		uid, r.feature, r.model, r.stage, r.counted, r.outcome,
		r.usage.Input, r.usage.Output, r.usage.CacheRead, r.usage.CacheWrite, cost); err != nil {
		s.logger.Error("llm: usage not recorded — spend is undercounted", "feature", r.feature, "cost_usd", microToUSD(cost), "err", err)
	}
	return cost
}
