package gen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/skriptes/skriptes/backend/internal/bio"
)

// Engine — извлечение вех из источников: API (Extractor) или headless
// Claude Code (CLIExtractor). Оба дают один и тот же bio.Author.
type Engine interface {
	Extract(ctx context.Context, src *Sources, prev *bio.Author) (*bio.Author, string, error)
}

// CLIExtractor — извлечение через `claude -p` (Claude Code в headless-режиме):
// токены по подписке владельца, ключа API не нужно. Системный промпт — наш
// (`--system-prompt-file`, иначе к каждому вызову прилипает дефолтный промпт
// Claude Code на ~47 тыс. токенов), схема — та же `outputSchema()` через
// `--json-schema`, ответ — поле `structured_output` JSON-конверта
// `--output-format json`. Инструменты запрещены: только текст → JSON.
type CLIExtractor struct {
	// Binary — путь к `claude`; пусто — из PATH.
	Binary string
	// Model, Effort — пробрасываются как --model/--effort; пусто — умолчания CLI.
	Model  string
	Effort string
	// Timeout одного вызова; 0 — 15 минут.
	Timeout time.Duration
	// Dir — куда класть файл системного промпта; пусто — временная папка.
	Dir string
}

// cliEnvelope — часть JSON-конверта `claude -p --output-format json`.
type cliEnvelope struct {
	Type             string          `json:"type"`
	Subtype          string          `json:"subtype"`
	IsError          bool            `json:"is_error"`
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	StopReason       string          `json:"stop_reason"`
	Usage            json.RawMessage `json:"usage"`
}

// Extract — один вызов `claude -p` на автора.
func (x *CLIExtractor) Extract(ctx context.Context, src *Sources, prev *bio.Author) (*bio.Author, string, error) {
	bin := x.Binary
	if bin == "" {
		bin = "claude"
	}
	timeout := x.Timeout
	if timeout == 0 {
		timeout = 15 * time.Minute
	}
	user, err := userPrompt(src, prev)
	if err != nil {
		return nil, "", err
	}
	dir := x.Dir
	if dir == "" {
		dir, err = os.MkdirTemp("", "biogen-")
		if err != nil {
			return nil, "", err
		}
		defer func() { _ = os.RemoveAll(dir) }()
	}
	sysFile := filepath.Join(dir, "system-prompt.txt")
	if err := os.WriteFile(sysFile, []byte(systemPrompt), 0o600); err != nil {
		return nil, "", err
	}
	schema, err := json.Marshal(outputSchema())
	if err != nil {
		return nil, "", err
	}
	args := []string{
		"-p",
		"--output-format", "json",
		"--json-schema", string(schema),
		"--system-prompt-file", sysFile,
		"--disallowedTools", "Bash,Read,Write,Edit,MultiEdit,Glob,Grep,WebFetch,WebSearch,Task,NotebookEdit",
	}
	if x.Model != "" {
		args = append(args, "--model", x.Model)
	}
	if x.Effort != "" {
		args = append(args, "--effort", x.Effort)
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, args...) //nolint:gosec // бинарь и аргументы — из флагов CLI владельца
	cmd.Stdin = strings.NewReader(user)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(stdout.String())
		}
		if len(msg) > 600 {
			msg = msg[:600] + "…"
		}
		return nil, stdout.String(), fmt.Errorf("claude -p: %w: %s", err, msg)
	}
	raw := stdout.String()
	var env cliEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		return nil, raw, fmt.Errorf("claude -p: ответ не JSON-конверт: %w", err)
	}
	if env.IsError {
		return nil, raw, fmt.Errorf("claude -p: is_error (%s): %s", env.Subtype, truncate(env.Result, 300))
	}
	payload := env.StructuredOutput
	if len(payload) == 0 || string(payload) == "null" {
		// Без structured_output — пробуем текст результата как JSON.
		payload = json.RawMessage(strings.TrimSpace(env.Result))
	}
	if len(payload) == 0 {
		return nil, raw, errors.New("claude -p: пустой ответ")
	}
	var d draft
	if err := json.Unmarshal(payload, &d); err != nil {
		return nil, raw, fmt.Errorf("claude -p: ответ не по схеме: %w", err)
	}
	return fromDraft(src, &d, prev), raw, nil
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
