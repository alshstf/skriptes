package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
)

// Роли реплик.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Message — реплика разговора.
type Message struct {
	Role string
	Text string
}

// Request — один вызов модели.
type Request struct {
	// Feature — имя функции для учёта и метрик (explain, recap, translate…).
	Feature string
	UserID  int64
	// Fast — быстрая (дешёвая) модель вместо основной.
	Fast bool
	// Counted — первое обращение действия пользователя: идёт в суточную квоту.
	// Повторные вызовы того же действия (расширение контекста) — false.
	Counted bool
	// Stage — ступень контекста, только для учёта.
	Stage int
	// System — неизменная инструкция.
	System string
	// Context — большой неизменный материал (текст книги). Идёт первым блоком
	// первой реплики под точкой кэша промпта: следующий вопрос по тому же
	// материалу читает его из кэша.
	Context string
	// Messages — разговор; последняя реплика — пользователя.
	Messages []Message
	// MaxTokens — потолок ответа; 0 — defaultMaxTokens.
	MaxTokens int64
	// Effort — глубина рассуждения (low | medium | high); пусто — low: ответы
	// читателю должны начинаться быстро.
	Effort string
}

const defaultMaxTokens = 4000

// Result — итог вызова.
type Result struct {
	Text string
	// Truncated — ответ обрезан по MaxTokens.
	Truncated bool
	Usage     Usage
	// CostMicroUSD — стоимость по своей таблице цен.
	CostMicroUSD int64
}

// Stream вызывает модель и отдаёт текст ответа по мере генерации в onText
// (может быть nil). Квоту и потолок вызывающий проверяет заранее (Begin);
// каждый вызов, в том числе неудачный, попадает в учёт.
func (s *Service) Stream(ctx context.Context, req Request, onText func(string)) (Result, error) {
	if !s.Configured() {
		return Result{}, &Unavailable{Reason: ReasonNoKey}
	}
	if len(req.Messages) == 0 || req.Messages[len(req.Messages)-1].Role != RoleUser {
		return Result{}, errors.New("llm: last message must be from user")
	}
	model := s.cfg.Model
	if req.Fast {
		model = s.cfg.FastModel
	}
	params := anthropic.MessageNewParams{
		Model:     model,
		MaxTokens: req.MaxTokens,
		Messages:  buildMessages(req),
		OutputConfig: anthropic.OutputConfigParam{
			Effort: anthropic.OutputConfigEffort(req.Effort),
		},
	}
	if params.MaxTokens <= 0 {
		params.MaxTokens = defaultMaxTokens
	}
	if req.Effort == "" {
		params.OutputConfig.Effort = anthropic.OutputConfigEffortLow
	}
	if req.System != "" {
		params.System = []anthropic.TextBlockParam{{Text: req.System}}
	}

	row := usageRow{userID: req.UserID, feature: req.Feature, model: model, stage: req.Stage, counted: req.Counted}
	stream := s.client.Messages.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()
	var msg anthropic.Message
	var text strings.Builder
	for stream.Next() {
		event := stream.Current()
		if err := msg.Accumulate(event); err != nil {
			s.logger.Warn("llm: stream event not accumulated", "err", err)
		}
		delta, ok := event.AsAny().(anthropic.ContentBlockDeltaEvent)
		if !ok {
			continue
		}
		if t, ok := delta.Delta.AsAny().(anthropic.TextDelta); ok && t.Text != "" {
			text.WriteString(t.Text)
			if onText != nil {
				onText(t.Text)
			}
		}
	}
	row.usage = Usage{
		Input:      msg.Usage.InputTokens,
		Output:     msg.Usage.OutputTokens,
		CacheRead:  msg.Usage.CacheReadInputTokens,
		CacheWrite: msg.Usage.CacheCreationInputTokens,
	}
	res := Result{Text: text.String(), Usage: row.usage}
	if err := stream.Err(); err != nil {
		row.outcome = "error"
		res.CostMicroUSD = s.record(ctx, row)
		return res, classify(err)
	}
	switch msg.StopReason {
	case anthropic.StopReasonRefusal:
		row.outcome = "refusal"
		res.CostMicroUSD = s.record(ctx, row)
		return res, fmt.Errorf("%w: %s", ErrRefused, msg.StopDetails.Category)
	case anthropic.StopReasonMaxTokens:
		row.outcome, res.Truncated = "truncated", true
	default:
		row.outcome = "ok"
	}
	res.CostMicroUSD = s.record(ctx, row)
	return res, nil
}

// buildMessages — реплики для API; Context — первым блоком первой реплики,
// под точкой кэша.
func buildMessages(req Request) []anthropic.MessageParam {
	out := make([]anthropic.MessageParam, 0, len(req.Messages))
	for i, m := range req.Messages {
		blocks := []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock(m.Text)}
		if i == 0 && req.Context != "" {
			ctxBlock := anthropic.ContentBlockParamUnion{OfText: &anthropic.TextBlockParam{
				Text:         req.Context,
				CacheControl: anthropic.NewCacheControlEphemeralParam(),
			}}
			blocks = append([]anthropic.ContentBlockParamUnion{ctxBlock}, blocks...)
		}
		if m.Role == RoleAssistant {
			out = append(out, anthropic.NewAssistantMessage(blocks...))
		} else {
			out = append(out, anthropic.NewUserMessage(blocks...))
		}
	}
	return out
}

// classify сводит ошибку SDK к своим: ключ, сбой провайдера, отмена запроса.
func classify(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Errorf("%w: %d", ErrAuth, apiErr.StatusCode)
		}
		return fmt.Errorf("%w: status %d", ErrUpstream, apiErr.StatusCode)
	}
	return fmt.Errorf("%w: %v", ErrUpstream, err)
}
