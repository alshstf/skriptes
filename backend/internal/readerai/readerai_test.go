package readerai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skriptes/skriptes/backend/internal/booktext"
	"github.com/skriptes/skriptes/backend/internal/llm"
)

// Книга для тестов: в начале — причина («Анна спрятала письмо…»), потом много
// текста, место чтения — вопрос про письмо, после него — то, чего читатель ещё
// не читал.
const (
	cause   = "Анна спрятала письмо отца в зелёную шкатулку и никому не сказала."
	hereSel = "Где письмо? — спросил Пётр, и Анна побледнела."
	spoiler = "СПОЙЛЕР: в конце выяснится, что письмо сжёг Пётр."
)

func filler(tag string, paras int) string {
	var b strings.Builder
	for i := 0; i < paras; i++ {
		fmt.Fprintf(&b, "%s, абзац %d: день тянулся медленно, за окном шёл дождь, и никто не говорил о главном.\n", tag, i)
	}
	return b.String()
}

// testBook: раздел 1 — причина и ~50 тыс. знаков; раздел 2 — ~30 тыс. знаков,
// место чтения и продолжение. early — сколько абзацев в первом разделе.
func testBook(early int) *booktext.Book {
	return &booktext.Book{Version: "v1", Sections: []booktext.Section{
		{Index: 0, Linear: false},
		{Index: 1, Linear: true, Text: strings.TrimSpace("Глава первая\n" + cause + "\n" + filler("раньше", early))},
		{Index: 2, Linear: true, Text: strings.TrimSpace("Глава вторая\n" + filler("недавно", 300) + hereSel + "\n" + spoiler + "\n" + filler("дальше", 50))},
	}}
}

// fakeModel — подставная модель: на каждый вызов берёт следующий ответ из
// очереди своей функции и запоминает запрос.
type fakeModel struct {
	mu      sync.Mutex
	reqs    []llm.Request
	answers map[string][]llm.Result // feature → ответы по порядку
}

func (f *fakeModel) Stream(_ context.Context, req llm.Request, onText func(string)) (llm.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	q := f.answers[req.Feature]
	if len(q) == 0 {
		return llm.Result{}, fmt.Errorf("fakeModel: no answer queued for %q", req.Feature)
	}
	res := q[0]
	if len(q) > 1 {
		f.answers[req.Feature] = q[1:]
	}
	if onText != nil && res.Text != "" {
		onText(res.Text)
	}
	return res, nil
}

func (f *fakeModel) byFeature(name string) []llm.Request {
	var out []llm.Request
	for _, r := range f.reqs {
		if r.Feature == name {
			out = append(out, r)
		}
	}
	return out
}

func askEarlier(terms ...string) llm.Result {
	in, _ := json.Marshal(map[string]any{"reason": "причина раньше", "terms": terms})
	return llm.Result{ToolCalls: []llm.ToolCall{{Name: toolEarlier, Input: in}}}
}

type memSummaries struct {
	mu sync.Mutex
	m  map[int]string
}

func (s *memSummaries) Get(_ context.Context, _ int64, _ string, chunks []int) (map[int]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[int]string{}
	for _, c := range chunks {
		if v, ok := s.m[c]; ok {
			out[c] = v
		}
	}
	return out, nil
}

func (s *memSummaries) Put(_ context.Context, _ int64, _ string, chunk int, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[chunk] = text
	return nil
}

func run(t *testing.T, e *Engine, book *booktext.Book, ask Ask) []Event {
	t.Helper()
	var events []Event
	require.NoError(t, e.Answer(context.Background(), book, ask, func(ev Event) { events = append(events, ev) }))
	return events
}

func kinds(events []Event) string {
	var b strings.Builder
	for _, e := range events {
		switch e.Kind {
		case "stage":
			fmt.Fprintf(&b, "stage%d ", e.Stage)
		default:
			b.WriteString(e.Kind + " ")
		}
	}
	return strings.TrimSpace(b.String())
}

func newEngine(m *fakeModel, s SummaryStore) *Engine {
	return &Engine{Model: m, Summaries: s, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func explain() Ask {
	return Ask{BookID: 7, UserID: 3, Mode: ModeExplain, Pos: Position{Section: 2, Excerpt: hereSel, Hint: 0.9}}
}

// Главное правило: что бы ни происходило на лестнице, текст после места чтения
// не попадает ни в один запрос к модели.
func requireNoSpoiler(t *testing.T, m *fakeModel) {
	t.Helper()
	for i, r := range m.reqs {
		all := r.Context + r.System
		for _, msg := range r.Messages {
			all += msg.Text
		}
		require.NotContains(t, all, "СПОЙЛЕР", "запрос %d (%s, ступень %d)", i, r.Feature, r.Stage)
		require.NotContains(t, all, "дальше, абзац", "запрос %d", i)
	}
}

func TestExplain_AnsweredNearby(t *testing.T) {
	m := &fakeModel{answers: map[string][]llm.Result{"explain": {{Text: "Пётр ищет письмо: «Где письмо? — спросил Пётр»."}}}}
	events := run(t, newEngine(m, &memSummaries{m: map[int]string{}}), testBook(600), explain())

	require.Equal(t, "stage1 delta done", kinds(events))
	require.Len(t, m.reqs, 1, "ответ нашёлся рядом — один вызов, пересказы не строились")
	r := m.reqs[0]
	require.True(t, r.Counted)
	require.Equal(t, StageNearby, r.Stage)
	require.False(t, r.Fast)
	require.Len(t, r.Tools, 1, "модель может попросить более ранний текст")
	require.Contains(t, r.System, "need_earlier_context")
	require.Contains(t, r.Context, hereSel, "окно доходит до выделенного места")
	require.NotContains(t, r.Context, cause, "ранняя часть на первой ступени не уходит")
	require.Less(t, len([]rune(r.Context)), windowRunes+500)
	require.Contains(t, r.Messages[0].Text, hereSel)
	requireNoSpoiler(t, m)

	done := events[len(events)-1]
	require.Equal(t, []Quote{{Text: "Где письмо? — спросил Пётр", Section: 2}}, done.Quotes, "цитата из ответа найдена в книге")
}

func TestExplain_ClimbsToMemory(t *testing.T) {
	m := &fakeModel{answers: map[string][]llm.Result{
		"explain": {askEarlier("письмо", "Анна"), {Text: "Анна побледнела, потому что «Анна спрятала письмо отца в зелёную шкатулку»; а ещё «этого в книге нет вовсе»."}},
		"summary": {{Text: "Пересказ куска."}},
	}}
	sums := &memSummaries{m: map[int]string{}}
	events := run(t, newEngine(m, sums), testBook(600), explain())

	require.Equal(t, "stage1 stage2 delta done", kinds(events))
	ex := m.byFeature("explain")
	require.Len(t, ex, 2)
	require.True(t, ex[0].Counted)
	require.False(t, ex[1].Counted, "в квоту идёт действие, а не каждый вызов")
	second := ex[1]
	require.Equal(t, StageMemory, second.Stage)
	require.Contains(t, second.Context, "Пересказ куска.", "пересказы ранних кусков")
	require.Contains(t, second.Context, cause, "найденное по словам модели место — дословно")
	require.Contains(t, second.Context, hereSel, "и последние страницы")
	require.Len(t, second.Tools, 1, "книга невелика — можно попросить всё прочитанное")

	sm := m.byFeature("summary")
	require.NotEmpty(t, sm)
	for _, r := range sm {
		require.True(t, r.Fast)
		require.False(t, r.Counted)
	}
	require.Len(t, sums.m, len(sm), "пересказы легли в общий кэш")
	requireNoSpoiler(t, m)

	done := events[len(events)-1]
	require.Len(t, done.Quotes, 1, "выдуманная цитата ссылкой не становится")
	require.Equal(t, 1, done.Quotes[0].Section)
	require.Contains(t, done.Quotes[0].Text, "спрятала письмо отца")

	// Второй читатель той же книги: пересказы берутся из кэша.
	m2 := &fakeModel{answers: map[string][]llm.Result{"explain": {askEarlier("письмо"), {Text: "Ответ."}}}}
	run(t, newEngine(m2, sums), testBook(600), explain())
	require.Empty(t, m2.byFeature("summary"))
}

func TestExplain_ClimbsToFull(t *testing.T) {
	m := &fakeModel{answers: map[string][]llm.Result{
		"explain": {askEarlier("письмо"), askEarlier("шкатулка"), {Text: "Теперь ясно."}},
	}}
	book := testBook(600)
	events := run(t, newEngine(m, nil), book, explain())

	require.Equal(t, "stage1 stage2 stage3 delta done", kinds(events))
	last := m.reqs[len(m.reqs)-1]
	require.Equal(t, StageFull, last.Stage)
	require.Empty(t, last.Tools, "дальше подниматься некуда")
	require.NotContains(t, last.System, "need_earlier_context")
	require.Contains(t, last.Context, cause)
	require.Contains(t, last.Context, "раньше, абзац 599", "весь прочитанный текст")
	require.True(t, strings.HasSuffix(last.Context, hereSel), "и ни знака дальше места чтения")
	requireNoSpoiler(t, m)
}

func TestExplain_ShortBookSingleStage(t *testing.T) {
	m := &fakeModel{answers: map[string][]llm.Result{"explain": {{Text: "Ответ."}}}}
	book := &booktext.Book{Version: "v", Sections: []booktext.Section{{Index: 0, Linear: true, Text: cause + "\n" + hereSel + "\n" + spoiler}}}
	ask := explain()
	ask.Pos.Section = 0
	events := run(t, newEngine(m, nil), book, ask)

	require.Equal(t, "stage1 delta done", kinds(events))
	require.Empty(t, m.reqs[0].Tools, "окно покрывает всё прочитанное — просить нечего")
	require.Contains(t, m.reqs[0].Context, cause)
	requireNoSpoiler(t, m)
}

func TestExplain_ContinuesOnReachedStage(t *testing.T) {
	m := &fakeModel{answers: map[string][]llm.Result{"explain": {{Text: "Продолжение."}}}}
	ask := explain()
	ask.MinStage = StageFull
	ask.Question = "А кто отец?"
	ask.Thread = []llm.Message{{Role: llm.RoleUser, Text: "первый вопрос"}, {Role: llm.RoleAssistant, Text: "первый ответ"}}
	events := run(t, newEngine(m, nil), testBook(600), ask)

	require.Equal(t, "stage3 delta done", kinds(events), "разговор продолжается на достигнутой ступени")
	r := m.reqs[0]
	require.Len(t, r.Messages, 3)
	require.Contains(t, r.Messages[2].Text, "А кто отец?")
	requireNoSpoiler(t, m)
}

func TestDefineAndTranslate(t *testing.T) {
	for _, mode := range []Mode{ModeDefine, ModeTranslate} {
		m := &fakeModel{answers: map[string][]llm.Result{string(mode): {{Text: "Ответ."}}}}
		ask := explain()
		ask.Mode, ask.Pos.Excerpt, ask.Lang = mode, "Анна побледнела", "en"
		events := run(t, newEngine(m, nil), testBook(600), ask)

		require.Equal(t, "stage0 delta done", kinds(events))
		r := m.reqs[0]
		require.True(t, r.Fast, "дешёвая операция — быстрая модель")
		require.Empty(t, r.Tools)
		require.Less(t, len([]rune(r.Context)), paragraphRunes+200, "только абзац вокруг")
		require.Contains(t, r.Context, "Анна побледнела")
		if mode == ModeTranslate {
			require.Contains(t, r.Messages[0].Text, "английский")
		}
		requireNoSpoiler(t, m)
	}
}

func TestRecap(t *testing.T) {
	m := &fakeModel{answers: map[string][]llm.Result{
		"recap":   {{Text: "Было так."}},
		"summary": {{Text: "Пересказ куска."}},
	}}
	ask := Ask{BookID: 7, UserID: 3, Mode: ModeRecap, Pos: Position{Section: 2, Excerpt: hereSel, Hint: 0.9}}
	events := run(t, newEngine(m, &memSummaries{m: map[int]string{}}), testBook(600), ask)

	require.Equal(t, "stage2 delta done", kinds(events))
	r := m.byFeature("recap")[0]
	require.Empty(t, r.Tools)
	require.Contains(t, r.Context, "Пересказ куска.")
	require.Contains(t, r.Context, "недавно, абзац 299")
	require.NotContains(t, r.Context, hereSel, "видимая страница ещё не прочитана — в пересказ не входит")
	requireNoSpoiler(t, m)
}

func TestExcerptNotFound_StaysWithinReadPart(t *testing.T) {
	m := &fakeModel{answers: map[string][]llm.Result{"explain": {{Text: "Ответ."}}}}
	ask := explain()
	ask.Pos.Excerpt = "фрагмент, которого в тексте нет (другая вёрстка)"
	ask.Pos.Hint = 0.5
	run(t, newEngine(m, nil), testBook(600), ask)
	require.NotContains(t, m.reqs[0].Context, hereSel, "место — по доле раздела, не дальше")
	requireNoSpoiler(t, m)
}

func TestBadRequests(t *testing.T) {
	e := newEngine(&fakeModel{}, nil)
	noop := func(Event) {}
	require.ErrorIs(t, e.Answer(context.Background(), testBook(10), Ask{Mode: "sing"}, noop), ErrBadRequest)
	require.ErrorIs(t, e.Answer(context.Background(), testBook(10), Ask{Mode: ModeExplain}, noop), ErrBadRequest, "нет фрагмента")
	require.ErrorIs(t, e.Answer(context.Background(), &booktext.Book{}, explain(), noop), ErrBadRequest, "книга без текста")
}

func TestSearchPassages(t *testing.T) {
	text := "Анна писала письмо.\nШёл дождь.\nПётр видел Анну у шкатулки с письмами.\nПисьма не было.\n" + strings.Repeat("Про Анну. ", 400)
	got := searchPassages(text, []string{"Анна", "письмо", "шкатулка"}, 200)
	require.Equal(t, []string{"Анна писала письмо.", "Пётр видел Анну у шкатулки с письмами.", "Письма не было."}, got,
		"словоформы находятся, порядок — как в книге, длинный абзац в бюджет не влез")
	require.Empty(t, searchPassages(text, []string{"и", "в"}, 200), "короткие слова не ищем")
	require.Empty(t, searchPassages("", []string{"Анна"}, 200))
}

func TestGuessTerms(t *testing.T) {
	require.Equal(t, []string{"Пётр", "Анна"}, guessTerms("Где письмо? — спросил Пётр, и Анна побледнела."))
	require.Equal(t, []string{"письмо", "побледнела"}, guessTerms("где письмо и кто побледнела"))
}
