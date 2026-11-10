package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skriptes/skriptes/backend/internal/settings"
	"github.com/skriptes/skriptes/backend/internal/testpg"
)

func TestCost(t *testing.T) {
	// Sonnet: 10 000 входа × 2 $ + 500 выхода × 10 $ за миллион.
	require.EqualValues(t, 25_000, Cost("claude-sonnet-5-5", Usage{Input: 10_000, Output: 500}))
	// Чтение кэша — 0,10 $ за миллион, запись — в 1,25 раза дороже входа.
	require.EqualValues(t, 12_500, Cost("claude-sonnet-5-5", Usage{CacheRead: 125_000}))
	require.EqualValues(t, 250_000, Cost("claude-sonnet-5-5", Usage{CacheWrite: 100_000}))
	// Haiku: до 100 тыс. токенов входа — один тариф, длиннее — другой (весь запрос).
	require.EqualValues(t, 1_500, Cost("claude-haiku-5-5", Usage{Input: 10_000, Output: 1_000}))
	require.EqualValues(t, 65_000, Cost("claude-haiku-5-5", Usage{Input: 125_000, Output: 1_000}))
	// Снимок модели с датой — по базовому имени; неизвестная — по самому дорогому тарифу.
	require.Equal(t, Cost("claude-sonnet-5-5", Usage{Input: 1000}), Cost("claude-sonnet-5-5-20261001", Usage{Input: 1000}))
	require.Equal(t, Cost("claude-fable-5-2", Usage{Input: 1000, Output: 10}), Cost("some-future-model", Usage{Input: 1000, Output: 10}))
}

// fakeAPI — подставной Anthropic Messages API: запоминает тела запросов и
// отвечает потоком SSE из reply.
type fakeAPI struct {
	mu     sync.Mutex
	bodies []map[string]any
	reply  func(w http.ResponseWriter)
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	f.mu.Lock()
	f.bodies = append(f.bodies, body)
	f.mu.Unlock()
	if r.URL.Path != "/v1/messages" || r.Header.Get("X-Api-Key") != "test-key" {
		http.Error(w, `{"type":"error","error":{"type":"authentication_error","message":"bad key"}}`, http.StatusUnauthorized)
		return
	}
	f.reply(w)
}

func sse(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, e := range events {
		var probe struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal([]byte(e), &probe)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", probe.Type, e)
	}
}

// textReply — обычный ответ: два куска текста, stop_reason и usage.
func textReply(stop string, chunks ...string) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		events := []string{
			`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-sonnet-5-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1200,"output_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":8000}}}`,
			`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		}
		for _, c := range chunks {
			b, _ := json.Marshal(c)
			events = append(events, `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":`+string(b)+`}}`)
		}
		events = append(events,
			`{"type":"content_block_stop","index":0}`,
			`{"type":"message_delta","delta":{"stop_reason":"`+stop+`","stop_sequence":null,"stop_details":null},"usage":{"input_tokens":1200,"output_tokens":40,"cache_creation_input_tokens":0,"cache_read_input_tokens":8000}}`,
			`{"type":"message_stop"}`)
		sse(w, events...)
	}
}

type fixedSettings struct{ cfg settings.AIConfig }

func (f *fixedSettings) AI(context.Context) (settings.AIConfig, error) { return f.cfg, nil }

func TestService(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	var userID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, password_hash, display_name, role) VALUES ('r@x','h','R','user') RETURNING id`).Scan(&userID))

	api := &fakeAPI{reply: textReply("end_turn", "Потому что ", "он обещал.")}
	srv := httptest.NewServer(api)
	defer srv.Close()
	st := &fixedSettings{}
	cfg := Config{APIKey: "test-key", BaseURL: srv.URL, Model: "claude-sonnet-5-5", FastModel: "claude-haiku-5-5", DailyPerUser: 2, DefaultBudgetUSD: 10}
	s := New(cfg, pool, st, srv.Client(), quiet)

	t.Run("без ключа функций нет", func(t *testing.T) {
		off := New(Config{}, pool, st, nil, quiet)
		require.False(t, off.Configured())
		require.Equal(t, Status{Reason: ReasonNoKey}, off.Status(ctx, userID))
		_, err := off.Stream(ctx, Request{Messages: []Message{{Role: RoleUser, Text: "?"}}}, nil)
		var un *Unavailable
		require.ErrorAs(t, err, &un)
		require.Empty(t, api.bodies, "без ключа запрос никуда не уходит")
		var nilSvc *Service
		require.False(t, nilSvc.Configured())
	})

	t.Run("ответ потоком, запрос собран как задумано, расход записан", func(t *testing.T) {
		require.Equal(t, Status{Available: true, DailyLeft: 2}, s.Status(ctx, userID))
		var got []string
		res, err := s.Stream(ctx, Request{
			Feature: "explain", UserID: userID, Counted: true, Stage: 1,
			System:   "Отвечай по тексту.",
			Context:  "ТЕКСТ КНИГИ",
			Messages: []Message{{Role: RoleUser, Text: "Почему он уехал?"}},
		}, func(s string) { got = append(got, s) })
		require.NoError(t, err)
		require.Equal(t, []string{"Потому что ", "он обещал."}, got, "текст отдаётся по мере генерации")
		require.Equal(t, "Потому что он обещал.", res.Text)
		require.False(t, res.Truncated)
		require.Equal(t, Usage{Input: 1200, Output: 40, CacheRead: 8000}, res.Usage)
		// 1200 × 2 + 8000 × 0,10 + 40 × 10 = 3600 микродолларов.
		require.EqualValues(t, 3600, res.CostMicroUSD)

		body := api.bodies[len(api.bodies)-1]
		require.Equal(t, "claude-sonnet-5-5", body["model"])
		require.Equal(t, true, body["stream"])
		require.Equal(t, "low", body["output_config"].(map[string]any)["effort"], "по умолчанию — быстрый ответ")
		require.Equal(t, "Отвечай по тексту.", body["system"].([]any)[0].(map[string]any)["text"])
		blocks := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
		require.Len(t, blocks, 2)
		book := blocks[0].(map[string]any)
		require.Equal(t, "ТЕКСТ КНИГИ", book["text"])
		require.Equal(t, "ephemeral", book["cache_control"].(map[string]any)["type"], "текст книги — под точкой кэша промпта")
		require.Equal(t, "Почему он уехал?", blocks[1].(map[string]any)["text"])
		require.NotContains(t, fmt.Sprint(body), "r@x", "о пользователе наружу ничего не уходит")

		var feature, model, outcome string
		var stage int
		var counted bool
		var cost int64
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT feature, model, stage, counted, outcome, cost_microusd FROM llm_usage WHERE user_id=$1`, userID).
			Scan(&feature, &model, &stage, &counted, &outcome, &cost))
		require.Equal(t, []any{"explain", "claude-sonnet-5-5", 1, true, "ok", int64(3600)}, []any{feature, model, stage, counted, outcome, cost})
	})

	t.Run("быстрая модель и продолжение разговора", func(t *testing.T) {
		_, err := s.Stream(ctx, Request{Feature: "translate", UserID: userID, Fast: true, Effort: "medium",
			Messages: []Message{{Role: RoleUser, Text: "a"}, {Role: RoleAssistant, Text: "b"}, {Role: RoleUser, Text: "c"}}}, nil)
		require.NoError(t, err)
		body := api.bodies[len(api.bodies)-1]
		require.Equal(t, "claude-haiku-5-5", body["model"])
		require.Equal(t, "medium", body["output_config"].(map[string]any)["effort"])
		msgs := body["messages"].([]any)
		require.Len(t, msgs, 3)
		require.Equal(t, "assistant", msgs[1].(map[string]any)["role"])
		require.Nil(t, body["system"], "пустая инструкция не отправляется")

		_, err = s.Stream(ctx, Request{Messages: []Message{{Role: RoleAssistant, Text: "b"}}}, nil)
		require.Error(t, err, "последняя реплика должна быть пользователя")
	})

	t.Run("квота считает действия, а не вызовы", func(t *testing.T) {
		// Одно действие уже учтено (counted), вызов перевода — не в счёт.
		require.Equal(t, 1, s.Status(ctx, userID).DailyLeft)
		_, err := s.Stream(ctx, Request{Feature: "explain", UserID: userID, Counted: true,
			Messages: []Message{{Role: RoleUser, Text: "?"}}}, nil)
		require.NoError(t, err)
		require.Equal(t, Status{Reason: ReasonQuota}, s.Status(ctx, userID))
		var un *Unavailable
		require.ErrorAs(t, s.Begin(ctx, userID), &un)
		require.Equal(t, ReasonQuota, un.Reason)
		require.True(t, s.Status(ctx, userID+1).Available, "квота — на пользователя")
	})

	t.Run("выключатели и потолок", func(t *testing.T) {
		other := userID + 1
		st.cfg = settings.AIConfig{Disabled: true}
		require.Equal(t, ReasonDisabled, s.Status(ctx, other).Reason)
		st.cfg = settings.AIConfig{DisabledUsers: []int64{other}}
		require.Equal(t, ReasonUserDisabled, s.Status(ctx, other).Reason)

		// Потрачено 3600 × 2 + перевод; потолок из настроек админа важнее env.
		tiny := 0.005
		st.cfg = settings.AIConfig{MonthlyBudgetUSD: &tiny}
		require.Equal(t, ReasonBudget, s.Status(ctx, other).Reason)
		zero := 0.0
		st.cfg = settings.AIConfig{MonthlyBudgetUSD: &zero}
		require.Equal(t, ReasonBudget, s.Status(ctx, other).Reason, "нулевой потолок — ничего не тратим")
		st.cfg = settings.AIConfig{}
		require.True(t, s.Status(ctx, other).Available, "без своей настройки — потолок из env")

		// Расход прошлого месяца в потолок этого не идёт.
		_, err := pool.Exec(ctx, `INSERT INTO llm_usage (at, feature, model, outcome, cost_microusd)
			VALUES (date_trunc('month', now()) - interval '1 day', 'explain', 'm', 'ok', 999000000)`)
		require.NoError(t, err)
		require.True(t, s.Status(ctx, other).Available)
	})

	t.Run("сводка для админки", func(t *testing.T) {
		sum, err := s.Summary(ctx)
		require.NoError(t, err)
		require.True(t, sum.Configured)
		require.InDelta(t, 10, sum.BudgetUSD, 1e-9)
		require.Len(t, sum.ByFeature, 2)
		require.Equal(t, "explain", sum.ByFeature[0].Feature, "сначала самая дорогая функция")
		require.Equal(t, 2, sum.ByFeature[0].Requests)
		require.Equal(t, 2, sum.ByFeature[0].Actions)
		require.InDelta(t, 0.0072, sum.ByFeature[0].USD, 1e-9)
		require.Greater(t, sum.SpentUSD, 0.0072)
	})

	t.Run("отказ модели, обрезка, сбой и плохой ключ", func(t *testing.T) {
		ask := func() (Result, error) {
			return s.Stream(ctx, Request{Feature: "explain", Messages: []Message{{Role: RoleUser, Text: "?"}}}, nil)
		}
		outcomeOfLast := func() string {
			var o string
			require.NoError(t, pool.QueryRow(ctx, `SELECT outcome FROM llm_usage ORDER BY id DESC LIMIT 1`).Scan(&o))
			return o
		}

		api.reply = func(w http.ResponseWriter) {
			sse(w,
				`{"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant","model":"m","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":50,"output_tokens":1}}}`,
				`{"type":"message_delta","delta":{"stop_reason":"refusal","stop_sequence":null,"stop_details":{"type":"refusal","category":"cyber","explanation":"x"}},"usage":{"input_tokens":50,"output_tokens":1}}`,
				`{"type":"message_stop"}`)
		}
		_, err := ask()
		require.ErrorIs(t, err, ErrRefused)
		require.Equal(t, "refusal", outcomeOfLast(), "отказ тоже стоит денег и попадает в учёт")

		api.reply = textReply("max_tokens", "Оборванный отв")
		res, err := ask()
		require.NoError(t, err)
		require.True(t, res.Truncated)
		require.Equal(t, "Оборванный отв", res.Text)
		require.Equal(t, "truncated", outcomeOfLast())

		api.reply = func(w http.ResponseWriter) {
			http.Error(w, `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`, 529)
		}
		_, err = ask()
		require.ErrorIs(t, err, ErrUpstream)
		require.Equal(t, "error", outcomeOfLast())

		bad := New(Config{APIKey: "wrong", BaseURL: srv.URL, Model: "m", FastModel: "m", DailyPerUser: 5, DefaultBudgetUSD: 10}, pool, st, srv.Client(), quiet)
		_, err = bad.Stream(ctx, Request{Feature: "explain", Messages: []Message{{Role: RoleUser, Text: "?"}}}, nil)
		require.ErrorIs(t, err, ErrAuth)
	})

	t.Run("отмена запроса читателем — расход всё равно записан", func(t *testing.T) {
		api.reply = textReply("end_turn", "ответ")
		var before, after int
		require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM llm_usage`).Scan(&before))
		cctx, stop := context.WithCancel(ctx)
		_, _ = s.Stream(cctx, Request{Feature: "explain", Messages: []Message{{Role: RoleUser, Text: "?"}}}, func(string) { stop() })
		require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM llm_usage`).Scan(&after))
		require.Equal(t, before+1, after)
	})

	require.True(t, strings.HasPrefix(srv.URL, "http://127.0.0.1"), "тесты не ходят в настоящий API")
}
