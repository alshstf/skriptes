package gen

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/skriptes/skriptes/backend/internal/bio"
)

// DefaultModel — модель извлечения; Effort high: отбор вех — суждение, не
// разметка. Переопределяется флагом cmd/biogen.
const DefaultModel = "claude-opus-5-5"

// Extractor — извлечение вех моделью по строгой JSON-схеме (structured
// outputs). Клиент создаётся вызывающим (ключ — только у CLI, в приложении
// его нет).
type Extractor struct {
	Client anthropic.Client
	Model  string
	Effort anthropic.OutputConfigEffort
}

// draft — ответ модели; отличается от bio.Author отсутствием ключей вех (их
// назначает генератор) и обязательным основанием у каждой вехи.
type draft struct {
	Name     string        `json:"name"`
	Born     string        `json:"born"`
	Died     string        `json:"died"`
	Chapters []bio.Chapter `json:"chapters"`
	Events   []draftEvent  `json:"events"`
	Works    []draftWork   `json:"works"`
}

type draftEvent struct {
	Date struct {
		From      string `json:"from"`
		To        string `json:"to"`
		Precision string `json:"precision"`
	} `json:"date"`
	Type    string `json:"type"`
	Tier    string `json:"tier"`
	Short   string `json:"short"`
	Long    string `json:"long"`
	Place   string `json:"place"`
	Chapter string `json:"chapter"`
	Works   []struct {
		Ref  string `json:"ref"`
		Note string `json:"note"`
	} `json:"works"`
	Evidence string `json:"evidence"`
}

type draftWork struct {
	Ref     string `json:"ref"`
	Title   string `json:"title"`
	QID     string `json:"qid"`
	Written struct {
		From int `json:"from"`
		To   int `json:"to"`
	} `json:"written"`
}

// outputSchema — строгая JSON-схема ответа (все поля обязательны,
// additionalProperties запрещены; необязательные — nullable).
func outputSchema() map[string]any {
	str := map[string]any{"type": "string"}
	nstr := map[string]any{"type": []string{"string", "null"}}
	obj := func(props map[string]any) map[string]any {
		keys := make([]string, 0, len(props))
		for k := range props {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return map[string]any{"type": "object", "properties": props, "required": keys, "additionalProperties": false}
	}
	types := make([]string, 0, len(bio.EventTypes))
	for _, t := range bio.EventTypes {
		types = append(types, string(t))
	}
	return obj(map[string]any{
		"name": str,
		"born": str,
		"died": nstr,
		"chapters": map[string]any{"type": "array", "items": obj(map[string]any{
			"key": str, "title": str,
			"from": map[string]any{"type": "integer"}, "to": map[string]any{"type": "integer"},
		})},
		"events": map[string]any{"type": "array", "items": obj(map[string]any{
			"date": obj(map[string]any{
				"from": str, "to": nstr,
				"precision": map[string]any{"type": "string", "enum": []string{"year", "month", "day"}},
			}),
			"type":    map[string]any{"type": "string", "enum": types},
			"tier":    map[string]any{"type": "string", "enum": []string{"major", "minor"}},
			"short":   str,
			"long":    str,
			"place":   nstr,
			"chapter": str,
			"works": map[string]any{"type": "array", "items": obj(map[string]any{
				"ref": str, "note": nstr,
			})},
			"evidence": str,
		})},
		"works": map[string]any{"type": "array", "items": obj(map[string]any{
			"ref": str, "title": str, "qid": nstr,
			"written": obj(map[string]any{"from": map[string]any{"type": "integer"}, "to": map[string]any{"type": "integer"}}),
		})},
	})
}

// Extract — один запрос на автора. prev — прежний файл датасета (регенерация):
// модель просят сохранить формулировки и состав, где факты те же, а
// генератор переиспользует ключи вех (по типу и году) — чтобы «скрытые»
// пережили регенерацию.
func (x *Extractor) Extract(ctx context.Context, src *Sources, prev *bio.Author) (*bio.Author, string, error) {
	model := x.Model
	if model == "" {
		model = DefaultModel
	}
	effort := x.Effort
	if effort == "" {
		effort = anthropic.OutputConfigEffortHigh
	}
	user, err := userPrompt(src, prev)
	if err != nil {
		return nil, "", err
	}
	resp, err := x.Client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     model,
		MaxTokens: 16000,
		System: []anthropic.TextBlockParam{{
			Text:         systemPrompt,
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}},
		OutputConfig: anthropic.OutputConfigParam{
			Effort: effort,
			Format: anthropic.JSONOutputFormatParam{Schema: outputSchema()},
		},
		Messages: []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(user))},
	})
	if err != nil {
		return nil, "", fmt.Errorf("messages: %w", err)
	}
	if resp.StopReason == anthropic.StopReasonRefusal {
		return nil, "", fmt.Errorf("модель отказала: %s %s", resp.StopDetails.Category, resp.StopDetails.Explanation)
	}
	if resp.StopReason == anthropic.StopReasonMaxTokens {
		return nil, "", fmt.Errorf("ответ обрезан по max_tokens")
	}
	var raw strings.Builder
	for _, block := range resp.Content {
		if t, ok := block.AsAny().(anthropic.TextBlock); ok {
			raw.WriteString(t.Text)
		}
	}
	var d draft
	if err := json.Unmarshal([]byte(raw.String()), &d); err != nil {
		return nil, raw.String(), fmt.Errorf("ответ не по схеме: %w", err)
	}
	a := fromDraft(src, &d, prev)
	return a, raw.String(), nil
}

// fromDraft — ответ модели → bio.Author: ключи вех, источники, фон.
func fromDraft(src *Sources, d *draft, prev *bio.Author) *bio.Author {
	a := &bio.Author{QID: src.QID, Name: d.Name, Born: d.Born, Died: d.Died, Chapters: d.Chapters}
	if a.Name == "" {
		a.Name = src.Label
	}
	if a.Born == "" {
		a.Born = src.Born
	}
	if a.Died == "" {
		a.Died = src.Died
	}
	for _, art := range src.Articles {
		a.Sources = append(a.Sources, bio.Source{ID: art.Lang + "wiki", URL: art.URL, Rev: art.Rev})
	}
	if prev != nil {
		a.Context = prev.Context
	}
	for _, w := range d.Works {
		wk := bio.Work{Ref: w.Ref, Title: w.Title, QID: w.QID}
		if w.Written.From > 0 {
			wk.Written = &bio.Written{From: w.Written.From, To: w.Written.To}
		}
		a.Works = append(a.Works, wk)
	}
	// Ключи: прежние — где тип и год совпали; иначе <type>-<yyyy>[-n].
	prevKeys := map[string][]string{}
	if prev != nil {
		for _, e := range prev.Events {
			k := string(e.Type) + "-" + strconv.Itoa(bio.YearOf(e.Date.From))
			prevKeys[k] = append(prevKeys[k], e.Key)
		}
	}
	// Сначала хронология, потом ключи: нумерация -2, -3 идёт по дате.
	sort.SliceStable(d.Events, func(i, j int) bool { return d.Events[i].Date.From < d.Events[j].Date.From })
	used := map[string]bool{}
	for _, de := range d.Events {
		e := bio.Event{
			Type:    bio.EventType(de.Type),
			Tier:    bio.Tier(de.Tier),
			Short:   strings.TrimSpace(de.Short),
			Long:    strings.TrimSpace(de.Long),
			Chapter: de.Chapter,
		}
		e.Date.From = de.Date.From
		e.Date.To = de.Date.To
		e.Date.Precision = bio.Precision(de.Date.Precision)
		if p := strings.TrimSpace(de.Place); p != "" {
			e.Place = &bio.Place{Label: p}
		}
		for _, l := range de.Works {
			e.Works = append(e.Works, bio.WorkLink{Ref: l.Ref, Note: strings.TrimSpace(l.Note)})
		}
		if q := strings.TrimSpace(de.Evidence); q != "" {
			e.Evidence = &bio.Evidence{Source: evidenceSource(src, q), Quote: q}
		}
		base := string(e.Type) + "-" + strconv.Itoa(bio.YearOf(e.Date.From))
		key := ""
		for _, pk := range prevKeys[base] {
			if !used[pk] {
				key = pk
				break
			}
		}
		if key == "" {
			key = base
			for n := 2; used[key]; n++ {
				key = base + "-" + strconv.Itoa(n)
			}
		}
		used[key] = true
		e.Key = key
		a.Events = append(a.Events, e)
	}
	return a
}

// evidenceSource — чья цитата: "wikidata" для строк факта, иначе язык статьи,
// в которой она нашлась (ru → en); не нашлась нигде — первая статья (verify
// такую веху всё равно отбракует).
func evidenceSource(src *Sources, quote string) string {
	if strings.HasPrefix(strings.ToLower(quote), "wikidata") {
		return "wikidata"
	}
	for _, art := range src.Articles {
		if quoteFound(quote, []string{normalize(art.Text)}) {
			return art.Lang + "wiki"
		}
	}
	if len(src.Articles) > 0 {
		return src.Articles[0].Lang + "wiki"
	}
	return "wikidata"
}

// userPrompt — факты Wikidata + тексты статей + (при регенерации) прежний файл.
func userPrompt(src *Sources, prev *bio.Author) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "<author qid=%q born=%q died=%q>%s</author>\n\n", src.QID, src.Born, src.Died, src.Label)
	facts, err := json.MarshalIndent(src.Facts, "", " ")
	if err != nil {
		return "", err
	}
	b.WriteString("<wikidata_facts>\n")
	b.Write(facts)
	b.WriteString("\n</wikidata_facts>\n\n")
	for _, art := range src.Articles {
		fmt.Fprintf(&b, "<article lang=%q title=%q>\n%s\n</article>\n\n", art.Lang, art.Title, art.Text)
	}
	if prev != nil {
		pj, err := json.MarshalIndent(prev, "", " ")
		if err != nil {
			return "", err
		}
		b.WriteString("<previous_dataset_file>\n")
		b.Write(pj)
		b.WriteString("\n</previous_dataset_file>\n\n")
		b.WriteString("Это регенерация: сохрани состав и формулировки вех, у которых факты не изменились; меняй только то, что стало точнее или было ошибкой.\n")
	}
	return b.String(), nil
}
