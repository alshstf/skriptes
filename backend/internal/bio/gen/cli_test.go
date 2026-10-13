package gen

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/skriptes/skriptes/backend/internal/bio"
)

// Подставной `claude`: проверяет флаги и stdin, отдаёт конверт с structured_output.
func fakeClaude(t *testing.T, structured string, isError bool) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	stdinFile := filepath.Join(dir, "stdin.txt")
	envelope := map[string]any{"type": "result", "subtype": "success", "is_error": isError, "result": structured}
	if !isError {
		envelope["structured_output"] = json.RawMessage(structured)
	}
	env, _ := json.Marshal(envelope)
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argsFile + "\ncat > " + stdinFile + "\ncat <<'ENVELOPE'\n" + string(env) + "\nENVELOPE\n"
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil { //nolint:gosec // исполняемый скрипт теста
		t.Fatal(err)
	}
	t.Setenv("BIOGEN_FAKE_ARGS", argsFile)
	t.Setenv("BIOGEN_FAKE_STDIN", stdinFile)
	return bin
}

func TestCLIExtractorRunsClaudeHeadless(t *testing.T) {
	bin := fakeClaude(t, canned, false)
	x := &CLIExtractor{Binary: bin, Model: "claude-opus-5-5", Effort: "high", Dir: t.TempDir()}
	src := &Sources{QID: "Q991", Label: "Достоевский", Born: "1821-11-11", Articles: []Article{{Lang: "ru", URL: "u", Rev: 5, Text: "Текст статьи про Соню."}}}
	a, raw, err := x.Extract(context.Background(), src, nil)
	if err != nil {
		t.Fatal(err)
	}
	if raw == "" || len(a.Events) != 3 || a.Events[2].Key != "loss-1868" {
		t.Fatalf("author: %+v", a)
	}
	args, _ := os.ReadFile(os.Getenv("BIOGEN_FAKE_ARGS"))
	s := string(args)
	for _, want := range []string{"-p\n", "--output-format\njson\n", "--json-schema\n", "--system-prompt-file\n", "--disallowedTools\n", "--model\nclaude-opus-5-5\n", "--effort\nhigh\n", `"additionalProperties":false`} {
		if !strings.Contains(s, want) {
			t.Errorf("нет аргумента %q в:\n%s", want, s)
		}
	}
	stdin, _ := os.ReadFile(os.Getenv("BIOGEN_FAKE_STDIN"))
	if !strings.Contains(string(stdin), "<article lang=\"ru\"") || !strings.Contains(string(stdin), "Текст статьи про Соню") {
		t.Fatalf("stdin без статьи:\n%s", stdin)
	}
	// Системный промпт ушёл файлом, а не в argv.
	i := strings.Index(s, "--system-prompt-file\n")
	path := strings.SplitN(s[i+len("--system-prompt-file\n"):], "\n", 2)[0]
	sys, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(sys), "Две формулировки") {
		t.Fatalf("system prompt file: %v", err)
	}
}

func TestCLIExtractorReportsError(t *testing.T) {
	bin := fakeClaude(t, "rate limited", true)
	x := &CLIExtractor{Binary: bin, Dir: t.TempDir()}
	_, _, err := x.Extract(context.Background(), &Sources{QID: "Q1"}, nil)
	if err == nil || !strings.Contains(err.Error(), "is_error") {
		t.Fatalf("ожидалась ошибка is_error, получено %v", err)
	}
	// Отсутствующий бинарь — понятная ошибка.
	x = &CLIExtractor{Binary: filepath.Join(t.TempDir(), "nope"), Dir: t.TempDir()}
	if _, _, err := x.Extract(context.Background(), &Sources{QID: "Q1"}, nil); err == nil {
		t.Fatal("ожидалась ошибка запуска")
	}
}

// Движки взаимозаменяемы для CLI.
var _ Engine = (*Extractor)(nil)
var _ Engine = (*CLIExtractor)(nil)

func TestFromDraftSharedByEngines(t *testing.T) {
	var d draft
	if err := json.Unmarshal([]byte(canned), &d); err != nil {
		t.Fatal(err)
	}
	a := fromDraft(&Sources{QID: "Q991"}, &d, nil)
	if a.QID != "Q991" || a.Works[0].Written.From != 1867 {
		t.Fatalf("%+v", a)
	}
	_ = bio.TierMajor
}
