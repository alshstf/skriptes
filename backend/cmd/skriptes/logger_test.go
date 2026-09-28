package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

// TestShutdownQuietHandler — #270: во время остановки обрывы фоновых работ
// («context canceled», «closed pool») пишутся INFO; настоящие ошибки и всё, что
// до остановки, — как есть.
func TestShutdownQuietHandler(t *testing.T) {
	var buf bytes.Buffer
	stopping := false
	log := slog.New(shutdownQuietHandler{
		Handler:  slog.NewTextHandler(&buf, nil),
		stopping: func() bool { return stopping },
	}).With("worker", "renown")
	canceled := fmt.Errorf("query: %w", context.Canceled)
	closed := errors.New("closed pool")

	log.Warn("before", "err", canceled)
	stopping = true
	log.Warn("canceled", "err", canceled)
	log.Error("closed", "err", closed)
	log.Warn("real", "err", errors.New("disk full"))

	want := map[string]string{"before": "WARN", "canceled": "INFO", "closed": "INFO", "real": "WARN"}
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		for msg, level := range want {
			if strings.Contains(line, "msg="+msg+" ") && !strings.Contains(line, "level="+level) {
				t.Errorf("%q: want level %s: %s", msg, level, line)
			}
		}
	}
}
