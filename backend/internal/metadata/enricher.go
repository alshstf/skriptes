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

// Enricher — оркестратор обогащения карточек книг (обложки + аннотации).
//
// Содержит две независимые цепочки провайдеров (cover/annotation) и
// pgxpool для записи результатов. Каждая цепочка обходится в порядке
// регистрации до первого успеха.
//
// Не потокобезопасен на уровне "две одновременные EnsureXxx(bookID)
// для одной книги" — обе запишут одинаковый результат. Inflight-карты
// дедуплицируют параллельные вызовы для одной книги в одном процессе.
type Enricher struct {
	coverProviders       []CoverProvider
	annotationProviders  []AnnotationProvider
	authorPhotoProviders []AuthorPhotoProvider
	authorBioProviders   []AuthorBioProvider
	pool                 *pgxpool.Pool
	coverRoot            string // абсолютный путь к /cache/covers (используется и для фото авторов)
	logger               *slog.Logger

	inflightMu          sync.Mutex
	inflightCover       map[int64]struct{}
	inflightAnnotate    map[int64]struct{}
	inflightAuthorPhoto map[int64]struct{}
	inflightAuthorBio   map[int64]struct{}
}

// New создаёт Enricher и обеспечивает существование coverRoot.
// Любая цепочка провайдеров может быть nil — соответствующий Ensure-метод
// станет no-op'ом. coverRoot используется как для обложек книг, так и
// для фотографий авторов: имена content-addressable (sha256), коллизий нет.
func New(
	pool *pgxpool.Pool,
	coverRoot string,
	coverProviders []CoverProvider,
	annotationProviders []AnnotationProvider,
	authorPhotoProviders []AuthorPhotoProvider,
	authorBioProviders []AuthorBioProvider,
	logger *slog.Logger,
) (*Enricher, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if err := os.MkdirAll(coverRoot, 0o750); err != nil {
		return nil, fmt.Errorf("mkdir cover root: %w", err)
	}
	return &Enricher{
		coverProviders:       coverProviders,
		annotationProviders:  annotationProviders,
		authorPhotoProviders: authorPhotoProviders,
		authorBioProviders:   authorBioProviders,
		pool:                 pool,
		coverRoot:            coverRoot,
		logger:               logger,
		inflightCover:        map[int64]struct{}{},
		inflightAnnotate:     map[int64]struct{}{},
		inflightAuthorPhoto:  map[int64]struct{}{},
		inflightAuthorBio:    map[int64]struct{}{},
	}, nil
}

// EnsureCover — гарантировать что у книги есть cover_path. Если уже
// есть — мгновенно выходит. Иначе обходит провайдеры по очереди до
// первого успеха, сохраняет файл, обновляет БД.
//
// Безопасно вызывать из горутины, отвязанной от HTTP-запроса
// (использует свой контекст с deadline'ом).
func (e *Enricher) EnsureCover(ctx context.Context, q BookQuery) {
	if !e.tryLock(e.inflightCover, q.ID) {
		return
	}
	defer e.unlock(e.inflightCover, q.ID)

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

	for _, p := range e.coverProviders {
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

// EnsureAnnotation — параллель EnsureCover для текстового описания.
// Если у книги уже есть annotation — мгновенно выходит. Иначе обходит
// annotationProviders, первый непустой результат пишется в books.annotation.
func (e *Enricher) EnsureAnnotation(ctx context.Context, q BookQuery) {
	if len(e.annotationProviders) == 0 {
		return
	}
	if !e.tryLock(e.inflightAnnotate, q.ID) {
		return
	}
	defer e.unlock(e.inflightAnnotate, q.ID)

	var existing *string
	if err := e.pool.QueryRow(ctx, `SELECT annotation FROM books WHERE id = $1`, q.ID).Scan(&existing); err != nil {
		e.logger.Warn("metadata: query book annotation failed", "book_id", q.ID, "err", err)
		return
	}
	if existing != nil && *existing != "" {
		return
	}

	for _, p := range e.annotationProviders {
		text, err := p.FetchAnnotation(ctx, q)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			e.logger.Info("metadata: annotation provider failed", "provider", p.Name(), "book_id", q.ID, "err", err)
			continue
		}
		if text == "" {
			continue
		}
		if _, err := e.pool.Exec(ctx,
			`UPDATE books SET annotation = $1, metadata_fetched_at = now() WHERE id = $2`,
			text, q.ID,
		); err != nil {
			e.logger.Warn("metadata: record annotation failed", "book_id", q.ID, "err", err)
			continue
		}
		e.logger.Info("metadata: annotation saved", "provider", p.Name(), "book_id", q.ID, "len", len(text))
		return
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

func (e *Enricher) tryLock(set map[int64]struct{}, id int64) bool {
	e.inflightMu.Lock()
	defer e.inflightMu.Unlock()
	if _, busy := set[id]; busy {
		return false
	}
	set[id] = struct{}{}
	return true
}

func (e *Enricher) unlock(set map[int64]struct{}, id int64) {
	e.inflightMu.Lock()
	defer e.inflightMu.Unlock()
	delete(set, id)
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

// EnsureAuthorPhoto — гарантирует наличие authors.photo_path. Файл
// сохраняется в тот же /cache/covers — у него content-addressable
// имя, коллизий с обложками книг быть не может.
func (e *Enricher) EnsureAuthorPhoto(ctx context.Context, q AuthorQuery) {
	if len(e.authorPhotoProviders) == 0 {
		return
	}
	if !e.tryLock(e.inflightAuthorPhoto, q.ID) {
		return
	}
	defer e.unlock(e.inflightAuthorPhoto, q.ID)

	var existing *string
	if err := e.pool.QueryRow(ctx, `SELECT photo_path FROM authors WHERE id = $1`, q.ID).Scan(&existing); err != nil {
		e.logger.Warn("metadata: query author photo failed", "author_id", q.ID, "err", err)
		return
	}
	if existing != nil && *existing != "" {
		return
	}

	for _, p := range e.authorPhotoProviders {
		img, err := p.FetchAuthorPhoto(ctx, q)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			e.logger.Info("metadata: author photo provider failed", "provider", p.Name(), "author_id", q.ID, "err", err)
			continue
		}
		if img == nil || img.Reader == nil {
			continue
		}
		filename, err := e.saveCover(img)
		_ = img.Reader.Close()
		if err != nil {
			e.logger.Warn("metadata: save author photo failed", "provider", p.Name(), "author_id", q.ID, "err", err)
			continue
		}
		if _, err := e.pool.Exec(ctx,
			`UPDATE authors SET photo_path = $1, metadata_fetched_at = now() WHERE id = $2`,
			filename, q.ID,
		); err != nil {
			e.logger.Warn("metadata: record author photo failed", "author_id", q.ID, "err", err)
			continue
		}
		e.logger.Info("metadata: author photo saved", "provider", p.Name(), "author_id", q.ID, "file", filename)
		return
	}

	// Все провайдеры мимо — отмечаем попытку, чтобы фронт мог решить
	// "polling сдался" и показать fallback. Совместимо с EnsureAuthorBio:
	// они оба пишут metadata_fetched_at независимо, последний раз обновлённый
	// время используется как "момент последней попытки enrichment'а".
	if _, err := e.pool.Exec(ctx,
		`UPDATE authors SET metadata_fetched_at = now() WHERE id = $1`, q.ID,
	); err != nil {
		e.logger.Warn("metadata: mark author fetched_at failed", "author_id", q.ID, "err", err)
	}
}

// EnsureAuthorBio — параллельно EnsureAuthorPhoto, но пишет authors.bio.
func (e *Enricher) EnsureAuthorBio(ctx context.Context, q AuthorQuery) {
	if len(e.authorBioProviders) == 0 {
		return
	}
	if !e.tryLock(e.inflightAuthorBio, q.ID) {
		return
	}
	defer e.unlock(e.inflightAuthorBio, q.ID)

	var existing *string
	if err := e.pool.QueryRow(ctx, `SELECT bio FROM authors WHERE id = $1`, q.ID).Scan(&existing); err != nil {
		e.logger.Warn("metadata: query author bio failed", "author_id", q.ID, "err", err)
		return
	}
	if existing != nil && *existing != "" {
		return
	}

	for _, p := range e.authorBioProviders {
		text, err := p.FetchAuthorBio(ctx, q)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			e.logger.Info("metadata: author bio provider failed", "provider", p.Name(), "author_id", q.ID, "err", err)
			continue
		}
		if text == "" {
			continue
		}
		if _, err := e.pool.Exec(ctx,
			`UPDATE authors SET bio = $1, metadata_fetched_at = now() WHERE id = $2`,
			text, q.ID,
		); err != nil {
			e.logger.Warn("metadata: record author bio failed", "author_id", q.ID, "err", err)
			continue
		}
		e.logger.Info("metadata: author bio saved", "provider", p.Name(), "author_id", q.ID, "len", len(text))
		return
	}
}
