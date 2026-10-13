package gen

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/skriptes/skriptes/backend/internal/bio"
)

const canned = `{"name":"Фёдор Достоевский","born":"1821-11-11","died":"1881-02-09",
"chapters":[{"key":"europe","title":"Европа","from":1867,"to":1871}],
"events":[
 {"date":{"from":"1868-05-24","to":null,"precision":"day"},"type":"loss","tier":"major","short":"Смерть трёхмесячной дочери Сони","long":"Умерла в Женеве.","place":"Женева","chapter":"europe","works":[{"ref":"idiot","note":null}],"evidence":"24 мая 1868 года Соня умерла"},
 {"date":{"from":"1868-03-05","to":null,"precision":"day"},"type":"child","tier":"major","short":"Рождение дочери Сони","long":"","place":null,"chapter":"europe","works":[],"evidence":"5 марта 1868"},
 {"date":{"from":"1868","to":null,"precision":"year"},"type":"child","tier":"minor","short":"Ещё одно рождение","long":"","place":null,"chapter":"europe","works":[],"evidence":"x"}
],
"works":[{"ref":"idiot","title":"Идиот","qid":"Q2156","written":{"from":1867,"to":1869}}]}`

func TestExtractParsesAndAssignsKeys(t *testing.T) {
	var gotReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotReq)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-opus-5-5",
			"content":     []map[string]any{{"type": "text", "text": canned}},
			"stop_reason": "end_turn", "usage": map[string]int{"input_tokens": 10, "output_tokens": 20},
		})
	}))
	defer srv.Close()
	x := &Extractor{Client: anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("test"))}
	src := &Sources{QID: "Q991", Label: "Достоевский", Born: "1821-11-11", Articles: []Article{{Lang: "ru", URL: "u", Rev: 5, Text: "…"}}}
	prev := &bio.Author{Context: []string{"ru"}, Events: []bio.Event{{Key: "loss-1868-old", Type: bio.EventLoss, Date: bio.Date{From: "1868-05", Precision: bio.PrecisionMonth}}}}

	a, raw, err := x.Extract(context.Background(), src, prev)
	if err != nil {
		t.Fatal(err)
	}
	if raw == "" {
		t.Fatal("raw пуст")
	}
	// Запрос: structured output со схемой, системный промпт с кэшем.
	oc, _ := gotReq["output_config"].(map[string]any)
	if oc == nil || oc["format"] == nil {
		t.Fatalf("нет output_config.format в запросе: %v", gotReq["output_config"])
	}
	if gotReq["model"] != "claude-opus-5-5" {
		t.Fatalf("model=%v", gotReq["model"])
	}
	// Результат: ключи назначены, прежний ключ переиспользован, отсортировано по дате.
	if len(a.Events) != 3 {
		t.Fatalf("вех %d", len(a.Events))
	}
	keys := []string{a.Events[0].Key, a.Events[1].Key, a.Events[2].Key}
	want := []string{"child-1868", "child-1868-2", "loss-1868-old"}
	if strings.Join(keys, ",") != strings.Join(want, ",") {
		t.Fatalf("keys=%v, want %v", keys, want)
	}
	if a.Events[2].Works[0].Ref != "idiot" || a.Events[2].Place.Label != "Женева" || a.Events[2].Evidence.Source != "ruwiki" {
		t.Fatalf("поля вехи: %+v", a.Events[2])
	}
	if a.Context[0] != "ru" || a.Sources[0].Rev != 5 || a.Works[0].Written.To != 1869 {
		t.Fatalf("author: %+v", a)
	}
}

func TestExtractRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-opus-5-5",
			"content": []map[string]any{}, "stop_reason": "refusal",
			"stop_details": map[string]any{"type": "refusal", "category": "general_harms", "explanation": "no"},
			"usage":        map[string]int{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer srv.Close()
	x := &Extractor{Client: anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("test"))}
	if _, _, err := x.Extract(context.Background(), &Sources{QID: "Q1"}, nil); err == nil || !strings.Contains(err.Error(), "отказала") {
		t.Fatalf("ожидалась ошибка отказа, получено %v", err)
	}
}

func TestOutputSchemaStrict(t *testing.T) {
	s := outputSchema()
	if s["additionalProperties"] != false {
		t.Fatal("корень не strict")
	}
	req := s["required"].([]string)
	if len(req) != len(s["properties"].(map[string]any)) {
		t.Fatal("не все свойства обязательны")
	}
}

func TestEvidenceSourceByArticle(t *testing.T) {
	src := &Sources{Articles: []Article{{Lang: "ru", Text: "Родился в Москве в 1821 году."}, {Lang: "en", Text: "He converted to Catholicism in 1947 with his wife."}}}
	if got := evidenceSource(src, "converted to Catholicism in 1947"); got != "enwiki" {
		t.Fatalf("en quote → %q", got)
	}
	if got := evidenceSource(src, "Родился в Москве в 1821 году"); got != "ruwiki" {
		t.Fatalf("ru quote → %q", got)
	}
	if got := evidenceSource(src, "wikidata P40: Соня 1868"); got != "wikidata" {
		t.Fatalf("wikidata → %q", got)
	}
	if got := evidenceSource(src, "нет такой цитаты нигде вообще"); got != "ruwiki" {
		t.Fatalf("fallback → %q", got)
	}
}
