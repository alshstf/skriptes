package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meilisearch/meilisearch-go"
	"github.com/skriptes/skriptes/backend/internal/api"
	"github.com/skriptes/skriptes/backend/internal/auth"
	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/catalog"
	"github.com/skriptes/skriptes/backend/internal/config"
	"github.com/skriptes/skriptes/backend/internal/converter"
	"github.com/skriptes/skriptes/backend/internal/db"
	"github.com/skriptes/skriptes/backend/internal/history"
	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/skriptes/skriptes/backend/internal/metadata"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config load: %w", err)
	}

	logger := newLogger(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(logger)

	dbCtx, dbCancel := context.WithTimeout(context.Background(), cfg.DatabaseTimeout)
	defer dbCancel()

	pool, err := db.NewPool(dbCtx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("db connect: %w", err)
	}
	defer pool.Close()
	logger.Info("database connected")

	if err := db.Migrate(cfg.DatabaseURL); err != nil {
		return fmt.Errorf("db migrate: %w", err)
	}
	logger.Info("migrations applied")

	meili := meilisearch.New(cfg.MeiliURL, meilisearch.WithAPIKey(cfg.MeiliAPIKey))
	logger.Info("meilisearch client configured", "url", cfg.MeiliURL)

	// Стартовый scan: импортируем все *.inpx из каталога SKRIPTES_INPX_ROOT.
	// Идемпотентно: повторные старты на тех же файлах — no-op за счёт хэш-проверки.
	// Не блокируем HTTP — крутим в горутине; если /readyz нужно учитывать импорт,
	// добавим отдельный флаг в PR 5 вместе с queue/jobs API.
	go runStartupImport(ctx(), pool, meili, cfg.InpxRoot, logger)

	authSvc := auth.New(pool, 0)
	catalogSvc := catalog.New(pool)
	historySvc := history.New(pool)
	booksSvc := books.New(pool, meili, historySvc)

	conv, err := converter.New(cfg.BooksRoot, cfg.CacheRoot, cfg.FBCPath)
	if err != nil {
		return fmt.Errorf("converter init: %w", err)
	}
	logger.Info("converter ready", "fbc", cfg.FBCPath, "cache", cfg.CacheRoot)

	// Metadata enricher: цепочки провайдеров для обложек/аннотаций книг
	// и для фото/био авторов. Порядок книжных — fb2 (локально, ~99% hit)
	// → Open Library → Google Books. Авторские пока только Wikipedia.
	httpClient := &http.Client{Timeout: 10 * time.Second}
	fb2Provider := metadata.NewFb2Provider()
	olProvider := metadata.NewOpenLibraryProvider(httpClient)
	gbProvider := metadata.NewGoogleBooksProvider(httpClient)
	wikiProvider := metadata.NewWikipediaProvider(httpClient)
	enricher, err := metadata.New(
		pool,
		filepath.Join(cfg.CacheRoot, "covers"),
		[]metadata.CoverProvider{fb2Provider, olProvider, gbProvider},
		[]metadata.AnnotationProvider{fb2Provider, olProvider, gbProvider},
		[]metadata.AuthorPhotoProvider{wikiProvider},
		[]metadata.AuthorBioProvider{wikiProvider},
		logger,
	)
	if err != nil {
		return fmt.Errorf("metadata init: %w", err)
	}
	logger.Info("metadata enricher ready", "cover_root", filepath.Join(cfg.CacheRoot, "covers"))

	router := api.NewRouter(api.Deps{
		Version: cfg.Version,
		DB:      pool,
		Auth: api.AuthDeps{
			Service:        authSvc,
			CookieSecure:   cfg.CookieSecure,
			CookieDomain:   cfg.CookieDomain,
			AllowedOrigins: cfg.AllowedOrigins,
		},
		Books:    api.BooksDeps{Service: booksSvc},
		Catalog:  api.CatalogDeps{Service: catalogSvc},
		Download: api.DownloadDeps{Books: booksSvc, Converter: conv},
		History:  api.HistoryDeps{Service: historySvc},
		Metadata: api.MetadataDeps{Service: enricher, BooksRoot: cfg.BooksRoot},
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("http server starting", "addr", cfg.HTTPAddr, "version", cfg.Version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failed", "err", err)
			stop()
		}
	}()

	<-sigCtx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	logger.Info("bye")
	return nil
}

// ctx — фоновый контекст для startup-сканера.
// Отдельная функция чтобы было видно, что у скана нет shutdown-контекста
// (импорт всё равно отрабатывает до конца, даже если процесс ловит SIGTERM —
// безопасно благодаря пер-записной транзакции).
func ctx() context.Context { return context.Background() }

func runStartupImport(ctx context.Context, pool *pgxpool.Pool, meili meilisearch.ServiceManager, inpxRoot string, logger *slog.Logger) {
	files, err := findInpxFiles(inpxRoot)
	if err != nil {
		logger.Warn("startup import skipped — failed to scan inpx root", "root", inpxRoot, "err", err)
		return
	}
	if len(files) == 0 {
		logger.Info("startup import — no INPX files found", "root", inpxRoot)
		return
	}
	imp := importer.New(importer.Deps{Pool: pool, Meili: meili, Logger: logger})
	logger.Info("startup import beginning", "count", len(files), "root", inpxRoot)
	for _, f := range files {
		stats, err := imp.Run(ctx, f)
		if err != nil {
			logger.Error("startup import failed for file", "file", f, "err", err)
			continue
		}
		_ = stats // важная статистика уже залогирована изнутри Run
	}
	logger.Info("startup import finished")
}

// findInpxFiles возвращает все *.inpx из каталога (нерекурсивно), отсортированные.
func findInpxFiles(root string) ([]string, error) {
	if root == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(strings.ToLower(name), ".inpx") {
			out = append(out, filepath.Join(root, name))
		}
	}
	sort.Strings(out)
	return out, nil
}

func newLogger(level, format string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	if format == "text" {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(h)
}
