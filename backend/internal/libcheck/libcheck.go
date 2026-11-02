// Package libcheck сверяет архивы книг из каталога с файлами в
// SKRIPTES_BOOKS_ROOT (#632).
//
// Каталог в PG и Meili живёт отдельно от тома с книгами: если том не
// подключился (диск на хосте смонтировался позже docker — bind-mount взял
// пустую папку), поиск и карточки работают, а скачивание и ридер отвечают
// «archive file not available». Healthcheck при этом зелёный. Проверка
// раз в несколько минут выставляет метрики skriptes_library_archives{,_missing}
// (по ним алерт) и пишет ERROR, когда архивы пропали.
package libcheck

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/metrics"
)

// sampleSize — сколько имён недоступных архивов показать в логе.
const sampleSize = 5

// Result — итог одной проверки.
type Result struct {
	Archives int      // архивов с живыми книгами в каталоге
	Missing  []string // из них нет в booksRoot
}

// Checker — периодическая проверка тома с книгами.
type Checker struct {
	pool      *pgxpool.Pool
	booksRoot string
	logger    *slog.Logger

	lastMissing int // -1 — ещё не проверяли
}

// New создаёт проверку.
func New(pool *pgxpool.Pool, booksRoot string, logger *slog.Logger) *Checker {
	return &Checker{pool: pool, booksRoot: booksRoot, logger: logger, lastMissing: -1}
}

// Check сверяет каталог с диском. Архивы с пометкой available = false
// (_lost из INPX) и архивы, где все книги удалены, не проверяются: их
// отсутствие на диске скачивание не ломает.
func (c *Checker) Check(ctx context.Context) (Result, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT a.filename FROM archives a
		WHERE a.available
		  AND EXISTS (SELECT 1 FROM books b WHERE b.archive_id = a.id AND NOT b.deleted)
		ORDER BY a.filename`)
	if err != nil {
		return Result{}, fmt.Errorf("list archives: %w", err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return Result{}, fmt.Errorf("scan archive: %w", err)
		}
		names = append(names, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Result{}, fmt.Errorf("list archives: %w", err)
	}
	missing, err := missingArchives(c.booksRoot, names)
	if err != nil {
		return Result{}, err
	}
	return Result{Archives: len(names), Missing: missing}, nil
}

// missingArchives — имена из names, которых нет файлом в root. Ошибка — только
// когда сам stat сломался не на «нет файла» (например, нет прав).
func missingArchives(root string, names []string) ([]string, error) {
	var missing []string
	for _, name := range names {
		_, err := os.Stat(filepath.Join(root, name))
		switch {
		case err == nil:
		case errors.Is(err, fs.ErrNotExist):
			missing = append(missing, name)
		default:
			return nil, fmt.Errorf("stat %s: %w", name, err)
		}
	}
	return missing, nil
}

// Run проверяет сразу и затем раз в interval, пока жив ctx.
func (c *Checker) Run(ctx context.Context, interval time.Duration) {
	c.runOnce(ctx)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.runOnce(ctx)
		}
	}
}

func (c *Checker) runOnce(ctx context.Context) {
	res, err := c.Check(ctx)
	if err != nil {
		if ctx.Err() == nil {
			c.logger.Warn("library check failed", "books_root", c.booksRoot, "err", err)
		}
		return
	}
	metrics.SetLibrary(res.Archives, len(res.Missing))
	c.report(res)
}

// report пишет в лог только смену состояния: пропали архивы (или их число
// изменилось) — ERROR, все снова на месте — INFO. Пока ничего не меняется,
// строк нет: состояние видно по метрике.
func (c *Checker) report(res Result) {
	n := len(res.Missing)
	prev := c.lastMissing
	c.lastMissing = n
	switch {
	case n > 0 && n != prev:
		sample := res.Missing
		if len(sample) > sampleSize {
			sample = sample[:sampleSize]
		}
		msg := "library: book archives missing — downloads and reader fail for their books"
		if n == res.Archives {
			msg = "library: no book archives found — books volume not mounted? downloads and reader fail"
		}
		c.logger.Error(msg, "books_root", c.booksRoot, "missing", n, "archives", res.Archives, "sample", sample)
	case n == 0 && prev > 0:
		c.logger.Info("library: all book archives available again", "books_root", c.booksRoot, "archives", res.Archives)
	}
}
