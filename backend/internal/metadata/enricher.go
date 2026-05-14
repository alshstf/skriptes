package metadata

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Enricher — оркестратор обогащения обложек.
//
// Содержит цепочку CoverProvider'ов (вызываются в порядке регистрации),
// каталог /cache/covers для бинарей и pgxpool для записи cover_path.
//
// Не потокобезопасен на уровне "две одновременные EnsureCover(bookID)
// для одной книги" — это допустимо, потому что обе запишут одинаковый
// результат. Если станет проблемой — добавим map[int64]chan struct{}
// для дедупа.
type Enricher struct {
	providers []CoverProvider
	pool      *pgxpool.Pool
	coverRoot string // абсолютный путь к /cache/covers
	logger    *slog.Logger

	// inflight предотвращает параллельные fetch'и для одной и той же
	// книги в рамках одного процесса. Не покрывает кластер — но у нас
	// один backend на хост.
	inflightMu sync.Mutex
	inflight   map[int64]struct{}
}

// New создаёт Enricher и обеспечивает существование coverRoot.
func New(pool *pgxpool.Pool, coverRoot string, providers []CoverProvider, logger *slog.Logger) (*Enricher, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if err := os.MkdirAll(coverRoot, 0o750); err != nil {
		return nil, fmt.Errorf("mkdir cover root: %w", err)
	}
	return &Enricher{
		providers: providers,
		pool:      pool,
		coverRoot: coverRoot,
		logger:    logger,
		inflight:  map[int64]struct{}{},
	}, nil
}

// EnsureCover — гарантировать что у книги есть cover_path. Если уже
// есть — мгновенно выходит. Иначе обходит провайдеры по очереди до
// первого успеха, сохраняет файл, обновляет БД.
//
// Безопасно вызывать из горутины, отвязанной от HTTP-запроса
// (использует свой контекст с deadline'ом).
func (e *Enricher) EnsureCover(ctx context.Context, q BookQuery) {
	if !e.tryLock(q.ID) {
		return // уже обрабатывается параллельным вызовом
	}
	defer e.unlock(q.ID)

	// Проверяем актуальное состояние БД на случай race условий
	// (другой запрос мог обогатить пока мы стояли в queue).
	var coverPath *string
	if err := e.pool.QueryRow(ctx, `SELECT cover_path FROM books WHERE id = $1`, q.ID).Scan(&coverPath); err != nil {
		e.logger.Warn("metadata: query book cover_path failed", "book_id", q.ID, "err", err)
		return
	}
	if coverPath != nil && *coverPath != "" {
		return
	}

	for _, p := range e.providers {
		img, err := p.FetchCover(ctx, q)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			e.logger.Info("metadata: provider failed", "provider", p.Name(), "book_id", q.ID, "err", err)
			continue
		}
		if img == nil || img.Reader == nil {
			continue
		}
		filename, err := e.saveCover(img)
		_ = img.Reader.Close()
		if err != nil {
			e.logger.Warn("metadata: save cover failed", "provider", p.Name(), "book_id", q.ID, "err", err)
			continue
		}
		if err := e.recordCover(ctx, q.ID, filename); err != nil {
			e.logger.Warn("metadata: record cover failed", "book_id", q.ID, "err", err)
			continue
		}
		e.logger.Info("metadata: cover saved", "provider", p.Name(), "book_id", q.ID, "file", filename)
		return
	}

	// Никто не нашёл — помечаем попытку, чтобы не молотить каждый
	// открыты карточки. Через TTL можно будет ретраиться.
	if _, err := e.pool.Exec(ctx,
		`UPDATE books SET metadata_fetched_at = now() WHERE id = $1`, q.ID,
	); err != nil {
		e.logger.Warn("metadata: mark fetched_at failed", "book_id", q.ID, "err", err)
	}
}

// saveCover — пишет байты в /cache/covers/{sha256}.{ext} и возвращает
// имя файла (без каталога) для записи в books.cover_path.
//
// Hash файла гарантирует идемпотентность: повторное скачивание той же
// картинки не дублирует место.
func (e *Enricher) saveCover(img *CoverImage) (string, error) {
	tmp, err := os.CreateTemp(e.coverRoot, "cover-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp: %w", err)
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	h := sha256.New()
	mw := io.MultiWriter(tmp, h)
	if _, err := io.Copy(mw, img.Reader); err != nil {
		return "", fmt.Errorf("copy cover: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp: %w", err)
	}

	ext := extFromMime(img.Mime)
	filename := fmt.Sprintf("%x%s", h.Sum(nil), ext)
	dst := filepath.Join(e.coverRoot, filename)
	if err := os.Rename(tmp.Name(), dst); err != nil {
		// если dst уже есть — это OK, идентичный файл; просто переиспользуем
		if _, statErr := os.Stat(dst); statErr == nil {
			return filename, nil
		}
		return "", fmt.Errorf("rename to %s: %w", dst, err)
	}
	return filename, nil
}

func (e *Enricher) recordCover(ctx context.Context, bookID int64, filename string) error {
	_, err := e.pool.Exec(ctx,
		`UPDATE books SET cover_path = $1, metadata_fetched_at = now() WHERE id = $2`,
		filename, bookID,
	)
	if err != nil {
		return fmt.Errorf("update cover_path: %w", err)
	}
	return nil
}

// CoverFile — абсолютный путь к файлу обложки в кэше. Используется
// HTTP-handler'ом /api/covers/{name} для отдачи клиенту.
func (e *Enricher) CoverFile(filename string) string {
	return filepath.Join(e.coverRoot, filename)
}

// CoverRoot — корень кэша обложек (для тестов).
func (e *Enricher) CoverRoot() string { return e.coverRoot }

func (e *Enricher) tryLock(id int64) bool {
	e.inflightMu.Lock()
	defer e.inflightMu.Unlock()
	if _, busy := e.inflight[id]; busy {
		return false
	}
	e.inflight[id] = struct{}{}
	return true
}

func (e *Enricher) unlock(id int64) {
	e.inflightMu.Lock()
	defer e.inflightMu.Unlock()
	delete(e.inflight, id)
}

// extFromMime — выбирает расширение для cache-файла. Не пытаемся быть
// исчерпывающими: ловим JPEG/PNG/WebP/GIF и валимся в .jpg для всего
// остального (большинство обложек — JPEG).
func extFromMime(mime string) string {
	switch mime {
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".jpg"
	}
}

// EnrichDeadline — дефолтное время на одну попытку обогащения.
// Используется handler'ами при создании detached-контекста.
const EnrichDeadline = 30 * time.Second
