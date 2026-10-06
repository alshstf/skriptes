// skriptes-explain — сухой прогон обогащения авторов (#280, разбор ошибок био и
// фото): по каждому id автора из stdin печатает строку JSON — что нашли бы
// текущие провайдеры и гейты, кто нашёл и почему (трасса шагов). В базу не
// пишет, фото не скачивает. Те же провайдеры и проверка профессии, что у
// бекенда.
//
// Использование (в контейнере бекенда, рядом с базой):
//
//	docker compose exec -T backend skriptes-explain -rpm 30 < ids.txt > explain.jsonl
//
// -rpm — авторов в минуту (каждый — несколько запросов к Википедии, Wikidata,
// OpenLibrary), как у фоновой перепроверки. Ошибки по автору — в stderr, прогон
// продолжается.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/skriptes/skriptes/backend/internal/config"
	"github.com/skriptes/skriptes/backend/internal/db"
	"github.com/skriptes/skriptes/backend/internal/metadata"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run() error {
	rpm := flag.Int("rpm", 30, "авторов в минуту")
	flag.Parse()
	if *rpm <= 0 {
		return errors.New("-rpm must be positive")
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	defer pool.Close()

	// Провайдеры авторов — как в cmd/skriptes: Wikipedia → OpenLibrary, оба с
	// политикой приёма кандидата по фактам Wikidata.
	wd := metadata.NewWikidataAdaptationsProvider(&http.Client{Timeout: 15 * time.Second})
	candidateFacts := metadata.CachedCandidateFacts(wd.CandidateFacts)
	candidateCheck := metadata.NewCandidateCheck(candidateFacts)
	wiki := metadata.NewWikipediaProvider(&http.Client{Timeout: 10 * time.Second}).WithCandidateCheck(candidateCheck).WithCandidateFacts(candidateFacts)
	ol := metadata.NewOpenLibraryProvider(metadata.NewEnricherHTTPClient(20 * time.Second)).WithCandidateCheck(candidateCheck)
	enricher, err := metadata.New(pool, filepath.Join(cfg.CacheRoot, "covers"), nil, nil,
		[]metadata.AuthorPhotoProvider{wiki, ol}, []metadata.AuthorBioProvider{wiki, ol}, nil, logger)
	if err != nil {
		return fmt.Errorf("metadata init: %w", err)
	}

	pace := time.NewTicker(time.Minute / time.Duration(*rpm))
	defer pace.Stop()
	out := bufio.NewWriter(os.Stdout)
	defer func() { _ = out.Flush() }()
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)

	in := bufio.NewScanner(os.Stdin)
	first := true
	for in.Scan() {
		line := strings.TrimSpace(in.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		id, err := strconv.ParseInt(line, 10, 64)
		if err != nil {
			logger.Warn("skip: not an author id", "line", line)
			continue
		}
		if !first {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-pace.C:
			}
		}
		first = false
		taskCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		ex, err := enricher.ExplainAuthor(taskCtx, id)
		cancel()
		if err != nil {
			logger.Warn("explain failed", "author_id", id, "err", err)
			continue
		}
		if err := enc.Encode(ex); err != nil {
			return fmt.Errorf("write: %w", err)
		}
		_ = out.Flush()
	}
	return in.Err()
}
