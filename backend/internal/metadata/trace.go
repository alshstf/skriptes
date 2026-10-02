package metadata

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// Трасса решения по автору (#280, разбор ошибок обогащения био и фото): какой
// источник что спросил и какая проверка что решила. Провайдеры пишут шаги,
// только если вызывающий положил трассу в контекст (WithAuthorTrace), — в
// остальных путях шаги ничего не стоят и поведение не меняют.

// Исходы шага.
const (
	TracePass   = "pass"   // проверка пропустила кандидата
	TraceReject = "reject" // проверка отвергла — источник вернёт «не найдено»
	TraceError  = "error"  // сбой источника (429, сеть) — не решение, автор повторится
	TraceInfo   = "info"   // промежуточный факт: что вернул поиск, какой QID, какие профессии
)

// TraceStep — один шаг. Stage — что проверялось: opensearch, name_gate, strict,
// qid, occupation, disambiguation, article_is_author, extract, thumbnail, search,
// bio, photo, request (сбой запроса).
type TraceStep struct {
	Source  string `json:"source"`
	Lang    string `json:"lang,omitempty"`
	Stage   string `json:"stage"`
	Outcome string `json:"outcome"`
	Input   string `json:"input,omitempty"`
	Value   string `json:"value,omitempty"`
}

// AuthorTrace — шаги одного поиска (био или фото одного автора).
type AuthorTrace struct {
	mu    sync.Mutex
	steps []TraceStep
}

type authorTraceKey struct{}

// WithAuthorTrace кладёт трассу в контекст: провайдеры авторов будут писать в неё шаги.
func WithAuthorTrace(ctx context.Context, t *AuthorTrace) context.Context {
	return context.WithValue(ctx, authorTraceKey{}, t)
}

func traceFrom(ctx context.Context) *AuthorTrace {
	t, _ := ctx.Value(authorTraceKey{}).(*AuthorTrace)
	return t
}

// traceOn — трассу пишут: можно позволить себе лишний запрос ради подробностей
// (названия профессий).
func traceOn(ctx context.Context) bool { return traceFrom(ctx) != nil }

func traceStep(ctx context.Context, s TraceStep) {
	t := traceFrom(ctx)
	if t == nil {
		return
	}
	s.Input = clipRunes(s.Input, traceValueRunes)
	s.Value = clipRunes(s.Value, traceValueRunes)
	t.mu.Lock()
	t.steps = append(t.steps, s)
	t.mu.Unlock()
}

// traceValueRunes — значения шага (начало статьи, список профессий) режем:
// трасса нужна для разбора, а не для хранения статей.
const traceValueRunes = 240

// Steps — копия шагов.
func (t *AuthorTrace) Steps() []TraceStep {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]TraceStep(nil), t.steps...)
}

// Reason — коротко, чем кончился поиск в каждом источнике (и языке): решающий
// шаг — последний reject или error, а если их нет — последний pass. Шаги
// info и вспомогательные источники (wikidata) не решают. Пример:
// «wikipedia/ru: reject name_gate «Гарднер, Иван»; openlibrary: reject search».
func (t *AuthorTrace) Reason() string {
	steps := t.Steps()
	var order []string
	decisive := map[string]TraceStep{}
	for _, s := range steps {
		if s.Outcome == TraceInfo || s.Source == "wikidata" {
			continue
		}
		key := s.Source
		if s.Lang != "" {
			key += "/" + s.Lang
		}
		prev, seen := decisive[key]
		if !seen {
			order = append(order, key)
		}
		// pass не перебивает уже случившийся отказ в этом источнике: после
		// неоднозначности поиск повторяется строгим путём, но решает его итог.
		if !seen || s.Outcome != TracePass || prev.Outcome == TracePass {
			decisive[key] = s
		}
	}
	parts := make([]string, 0, len(order))
	for _, key := range order {
		s := decisive[key]
		part := fmt.Sprintf("%s: %s %s", key, s.Outcome, s.Stage)
		if v := strings.TrimSpace(s.Value); v != "" {
			part += " «" + clipRunes(v, 80) + "»"
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "; ")
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
