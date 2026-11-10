// Package readerai — ИИ-функции читалки: «Объяснить», «Кто это», «Напомнить,
// что было», «Что значит», «Перевести».
//
// Два правила, на которых всё построено (план — ~/projects/plans/skriptes/
// ai-roadmap.md, этап 1; решения владельца 2026-11-10):
//
//   - Без спойлеров — конструкцией: модели уходит только текст до места чтения.
//   - Контекст — от малого к большому («лестница»): абзац → окно назад от места
//     чтения → пересказы и найденные места из прочитанного → всё прочитанное.
//     Подняться на ступень просит сама модель (инструмент need_earlier_context),
//     когда в присланном нет ответа или причины события.
package readerai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/skriptes/skriptes/backend/internal/booktext"
	"github.com/skriptes/skriptes/backend/internal/llm"
)

// Mode — что просит читатель.
type Mode string

// Режимы. Имя режима — оно же имя функции в учёте расходов.
const (
	ModeExplain   Mode = "explain"   // что тут произошло и почему
	ModeWho       Mode = "who"       // кто это (на текущий момент чтения)
	ModeRecap     Mode = "recap"     // напомнить, что было
	ModeDefine    Mode = "define"    // что значит слово или выражение
	ModeTranslate Mode = "translate" // перевести фрагмент
)

// Valid — известный режим.
func (m Mode) Valid() bool {
	switch m {
	case ModeExplain, ModeWho, ModeRecap, ModeDefine, ModeTranslate:
		return true
	}
	return false
}

// Ступени контекста.
const (
	StageParagraph = 0 // абзац вокруг выделения
	StageNearby    = 1 // окно назад от места чтения
	StageMemory    = 2 // пересказы ранних кусков + найденные места + окно
	StageFull      = 3 // всё прочитанное
)

// Размеры (в знаках). Подобраны по замеру книг прода (ai/baseline-2026-11.md):
// медианная книга — 550 тыс. знаков, раздел — до 430 тыс.
const (
	paragraphRunes = 1500    // ступень 0: до выделения
	windowRunes    = 20_000  // ступень 1: «страница-две назад» с запасом
	chunkRunes     = 40_000  // кусок для пересказа
	maxSummaries   = 25      // пересказываем не больше стольких последних кусков
	passagesRunes  = 12_000  // найденные места на ступени 2
	fullCapRunes   = 900_000 // ступень 3 длиннее — не отправляем
	maxExcerpt     = 2000
	maxQuestion    = 500
	maxThread      = 6
)

// Position — место читателя, как его знает ридер.
type Position struct {
	// Section — номер раздела (spine), тот же, что у foliate.
	Section int
	// Excerpt — выделенный фрагмент; для «напомнить» — первые слова видимой страницы.
	Excerpt string
	// Hint — примерная доля раздела (0..1), где фрагмент: выбор из нескольких
	// вхождений и запасное место, если фрагмент в тексте не нашёлся.
	Hint float64
}

// Ask — запрос читателя.
type Ask struct {
	BookID   int64
	UserID   int64
	Mode     Mode
	Pos      Position
	Question string        // свой вопрос (необязателен)
	Thread   []llm.Message // прошлые реплики этого разговора
	// MinStage — с какой ступени начинать (продолжение разговора остаётся на
	// достигнутой ступени: тот же контекст читается из кэша промпта).
	MinStage int
	// Lang — язык интерфейса читателя (для перевода), например «ru».
	Lang string
}

// Quote — цитата из ответа, найденная в прочитанном тексте: по ней ридер
// показывает место.
type Quote struct {
	Text    string `json:"text"`
	Section int    `json:"section"`
}

// Event — событие ответа.
type Event struct {
	// Kind: stage — ответ идёт на ступени Stage (текст предыдущей ступени, если
	// был, отбросить); delta — кусок текста; done — ответ закончен.
	Kind      string  `json:"kind"`
	Stage     int     `json:"stage,omitempty"`
	Text      string  `json:"text,omitempty"`
	Quotes    []Quote `json:"quotes,omitempty"`
	Truncated bool    `json:"truncated,omitempty"`
}

// Model — вызов языковой модели (llm.Service).
type Model interface {
	Stream(ctx context.Context, req llm.Request, onText func(string)) (llm.Result, error)
}

// SummaryStore — кэш пересказов кусков книги, общий для всех читателей.
type SummaryStore interface {
	Get(ctx context.Context, bookID int64, version string, chunks []int) (map[int]string, error)
	Put(ctx context.Context, bookID int64, version string, chunk int, text string) error
}

// Engine отвечает на вопросы читателя.
type Engine struct {
	Model     Model
	Summaries SummaryStore // nil — без пересказов (ступень 2 — только найденные места)
	Logger    *slog.Logger
}

// ErrBadRequest — запрос не имеет смысла (нет фрагмента, неизвестный режим).
var ErrBadRequest = errors.New("readerai: bad request")

const toolEarlier = "need_earlier_context"

var earlierTool = llm.Tool{
	Name: toolEarlier,
	Description: "Вызови, если в приведённом тексте нет ответа на вопрос читателя или нет причины того, о чём он спрашивает " +
		"(событие есть, а почему так вышло — было раньше). Тебе покажут более раннюю часть прочитанного. " +
		"Не вызывай, если ответ уже можно дать по приведённому тексту.",
	Properties: map[string]any{
		"reason": map[string]any{"type": "string", "description": "Чего не хватает, одной фразой."},
		"terms": map[string]any{
			"type": "array", "items": map[string]any{"type": "string"},
			"description": "Что искать в более ранней части: имена, предметы, места, ключевые слова из книги (3–8 штук).",
		},
	},
	Required: []string{"reason", "terms"},
}

// Answer отвечает на запрос читателя, отдавая события в emit. Квоту и потолок
// расходов вызывающий проверяет заранее (llm.Service.Begin).
func (e *Engine) Answer(ctx context.Context, book *booktext.Book, ask Ask, emit func(Event)) error {
	if !ask.Mode.Valid() {
		return fmt.Errorf("%w: mode %q", ErrBadRequest, ask.Mode)
	}
	ask.Pos.Excerpt = clip(strings.TrimSpace(ask.Pos.Excerpt), maxExcerpt)
	ask.Question = clip(strings.TrimSpace(ask.Question), maxQuestion)
	if ask.Pos.Excerpt == "" && ask.Mode != ModeRecap {
		return fmt.Errorf("%w: empty excerpt", ErrBadRequest)
	}
	if len(ask.Thread) > maxThread {
		ask.Thread = ask.Thread[len(ask.Thread)-maxThread:]
	}
	flat := book.Flat()
	if flat.Text == "" {
		return fmt.Errorf("%w: book has no text", ErrBadRequest)
	}
	upto := e.readUpto(book, flat, ask)

	switch ask.Mode {
	case ModeDefine, ModeTranslate:
		ctxText, _ := flat.Window(upto, paragraphRunes)
		return e.final(ctx, flat, upto, ask, StageParagraph, ctxText, true, emit)
	case ModeRecap:
		window, start := flat.Window(upto, windowRunes)
		ctxText := e.memoryContext(ctx, book, flat, ask, start, window, nil)
		return e.final(ctx, flat, upto, ask, StageMemory, ctxText, true, emit)
	}

	// «Объяснить» и «Кто это»: лестница.
	window, start := flat.Window(upto, windowRunes)
	stage := max(ask.MinStage, StageNearby)
	if start == 0 {
		// Окно уже покрывает всё прочитанное — подниматься некуда.
		return e.final(ctx, flat, upto, ask, StageNearby, window, true, emit)
	}
	counted := true
	var terms []string
	if stage == StageNearby {
		emit(Event{Kind: "stage", Stage: StageNearby})
		res, err := e.call(ctx, ask, StageNearby, window, counted, true, emit)
		if err != nil {
			return err
		}
		counted = false
		var more bool
		if terms, more = wantsEarlier(res); !more {
			return e.finish(flat, upto, res, emit)
		}
		stage = StageMemory
	}
	fullOK := utf8.RuneCountInString(flat.Text[:upto]) <= fullCapRunes
	if stage == StageMemory {
		emit(Event{Kind: "stage", Stage: StageMemory})
		ctxText := e.memoryContext(ctx, book, flat, ask, start, window, terms)
		res, err := e.call(ctx, ask, StageMemory, ctxText, counted, fullOK, emit)
		if err != nil {
			return err
		}
		counted = false
		if _, more := wantsEarlier(res); !more {
			return e.finish(flat, upto, res, emit)
		}
	}
	if !fullOK {
		// Всё прочитанное слишком велико: отвечаем по тому, что есть, без права
		// просить ещё.
		ctxText := e.memoryContext(ctx, book, flat, ask, start, window, terms)
		return e.final(ctx, flat, upto, ask, StageMemory, ctxText, counted, emit)
	}
	return e.final(ctx, flat, upto, ask, StageFull, flat.Text[:upto], counted, emit)
}

// readUpto — место чтения в тексте книги: конец выделения (вместе с абзацем,
// где оно кончается) или, для «напомнить», начало видимой страницы.
func (e *Engine) readUpto(book *booktext.Book, flat *booktext.Flat, ask Ask) int {
	var text string
	for _, s := range book.Sections {
		if s.Index == ask.Pos.Section {
			text = s.Text
		}
	}
	hint := min(max(ask.Pos.Hint, 0), 1)
	start, end, ok := booktext.Locate(text, ask.Pos.Excerpt, hint)
	if !ok {
		// Фрагмент не нашёлся (другая вёрстка пробелов, сноска, картинка) —
		// место по доле раздела: чуть менее точно, но не дальше прочитанного.
		return flat.Offset(ask.Pos.Section, int(hint*float64(len(text))))
	}
	if ask.Mode == ModeRecap {
		return flat.Offset(ask.Pos.Section, start)
	}
	if nl := strings.IndexByte(text[end:], '\n'); nl >= 0 {
		end += nl
	} else {
		end = len(text)
	}
	return flat.Offset(ask.Pos.Section, end)
}

// final — последний вызов: без инструмента, ответ уходит читателю как есть.
func (e *Engine) final(ctx context.Context, flat *booktext.Flat, upto int, ask Ask, stage int, ctxText string, counted bool, emit func(Event)) error {
	emit(Event{Kind: "stage", Stage: stage})
	res, err := e.call(ctx, ask, stage, ctxText, counted, false, emit)
	if err != nil {
		return err
	}
	return e.finish(flat, upto, res, emit)
}

func (e *Engine) finish(flat *booktext.Flat, upto int, res llm.Result, emit func(Event)) error {
	emit(Event{Kind: "done", Quotes: verifyQuotes(flat, upto, res.Text), Truncated: res.Truncated})
	return nil
}

func (e *Engine) call(ctx context.Context, ask Ask, stage int, ctxText string, counted, canAskMore bool, emit func(Event)) (llm.Result, error) {
	req := llm.Request{
		Feature:  string(ask.Mode),
		UserID:   ask.UserID,
		Fast:     ask.Mode == ModeDefine || ask.Mode == ModeTranslate,
		Counted:  counted,
		Stage:    stage,
		System:   systemPrompt(ask.Mode, canAskMore),
		Context:  contextHeader(stage) + ctxText,
		Messages: append(append([]llm.Message{}, ask.Thread...), llm.Message{Role: llm.RoleUser, Text: userPrompt(ask)}),
	}
	if canAskMore {
		req.Tools = []llm.Tool{earlierTool}
	}
	return e.Model.Stream(ctx, req, func(s string) { emit(Event{Kind: "delta", Text: s}) })
}

// wantsEarlier — модель попросила более ранний текст; возвращает, что искать.
func wantsEarlier(res llm.Result) ([]string, bool) {
	for _, c := range res.ToolCalls {
		if c.Name != toolEarlier {
			continue
		}
		var in struct {
			Terms []string `json:"terms"`
		}
		_ = json.Unmarshal(c.Input, &in)
		return in.Terms, true
	}
	return nil, false
}

func clip(s string, maxRunes int) string {
	if utf8.RuneCountInString(s) <= maxRunes {
		return s
	}
	return string([]rune(s)[:maxRunes])
}
