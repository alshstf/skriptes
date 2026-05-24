package metadata

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
	adaptationProviders  []AdaptationProvider
	pool                 *pgxpool.Pool
	coverRoot            string // абсолютный путь к /cache/covers (используется и для фото авторов, и для постеров экранизаций)
	logger               *slog.Logger
	posterHTTPClient     *http.Client // для скачивания PosterURL экранизаций; nil → берётся http.DefaultClient

	inflightMu          sync.Mutex
	inflightCover       map[int64]struct{}
	inflightAnnotate    map[int64]struct{}
	inflightAuthorPhoto map[int64]struct{}
	inflightAuthorBio   map[int64]struct{}
	inflightAdaptations map[int64]struct{}
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
	adaptationProviders []AdaptationProvider,
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
		adaptationProviders:  adaptationProviders,
		pool:                 pool,
		coverRoot:            coverRoot,
		logger:               logger,
		posterHTTPClient:     &http.Client{Timeout: 15 * time.Second},
		inflightCover:        map[int64]struct{}{},
		inflightAnnotate:     map[int64]struct{}{},
		inflightAuthorPhoto:  map[int64]struct{}{},
		inflightAuthorBio:    map[int64]struct{}{},
		inflightAdaptations:  map[int64]struct{}{},
	}, nil
}

// WithPosterHTTPClient — для тестов: подменить HTTP-клиент для скачивания
// постеров экранизаций.
func (e *Enricher) WithPosterHTTPClient(c *http.Client) *Enricher {
	if c != nil {
		e.posterHTTPClient = c
	}
	return e
}

// EnsureCover — гарантировать что у книги есть cover_path. Если уже
// есть — мгновенно выходит. Иначе обходит провайдеры по очереди до
// первого успеха, сохраняет файл, обновляет БД.
//
// Безопасно вызывать из горутины, отвязанной от HTTP-запроса
// (использует свой контекст с deadline'ом).
func (e *Enricher) EnsureCover(ctx context.Context, q BookQuery) {
	e.ensureCover(ctx, q, acceptAllCover, true)
}

// EnsureCoverLocal — как EnsureCover, но обходит ТОЛЬКО local-провайдеры
// (fb2, без внешних API). Используется фоновым прогревом: fb2-обложка
// есть у ~99% книг и достаётся локально из zip без rate-limit'ов.
//
// Отличие от EnsureCover: при промахе НЕ ставит metadata_fetched_at —
// чтобы ленивый внешний путь (OL/GB при открытии карточки) всё ещё мог
// сработать для редких книг без fb2-обложки. Отметку «прогрето» ставит
// сам прогрев (Prewarmer), чтобы не молотить промахи каждый цикл.
//
// Возвращает true, если обложка сохранена.
func (e *Enricher) EnsureCoverLocal(ctx context.Context, q BookQuery) bool {
	return e.ensureCover(ctx, q, isLocalCover, false)
}

// ensureCover — общая реализация: обходит coverProviders, прошедшие
// фильтр accept, на первом успехе сохраняет и возвращает true. Если
// markOnMiss=true и никто не нашёл — ставит metadata_fetched_at.
func (e *Enricher) ensureCover(
	ctx context.Context,
	q BookQuery,
	accept func(CoverProvider) bool,
	markOnMiss bool,
) bool {
	if !e.tryLock(e.inflightCover, q.ID) {
		return false
	}
	defer e.unlock(e.inflightCover, q.ID)

	// Проверяем актуальное состояние БД на случай race условий
	// (другой запрос мог обогатить пока мы стояли в queue).
	var coverPath *string
	if err := e.pool.QueryRow(ctx, `SELECT cover_path FROM books WHERE id = $1`, q.ID).Scan(&coverPath); err != nil {
		e.logger.Warn("metadata: query book cover_path failed", "book_id", q.ID, "err", err)
		return false
	}
	if coverPath != nil && *coverPath != "" {
		return false
	}

	for _, p := range e.coverProviders {
		if !accept(p) {
			continue
		}
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
		return true
	}

	if markOnMiss {
		// Никто не нашёл — помечаем попытку, чтобы не молотить каждый
		// открытие карточки. Через TTL можно будет ретраиться.
		if _, err := e.pool.Exec(ctx,
			`UPDATE books SET metadata_fetched_at = now() WHERE id = $1`, q.ID,
		); err != nil {
			e.logger.Warn("metadata: mark fetched_at failed", "book_id", q.ID, "err", err)
		}
	}
	return false
}

// EnsureAnnotation — параллель EnsureCover для текстового описания.
// Если у книги уже есть annotation — мгновенно выходит. Иначе обходит
// annotationProviders, первый непустой результат пишется в books.annotation.
func (e *Enricher) EnsureAnnotation(ctx context.Context, q BookQuery) {
	e.ensureAnnotation(ctx, q, acceptAllAnnotation)
}

// EnsureAnnotationLocal — как EnsureAnnotation, но только local-провайдеры
// (fb2 <annotation>, без внешних API). Для фонового прогрева. Возвращает
// true, если аннотация сохранена.
func (e *Enricher) EnsureAnnotationLocal(ctx context.Context, q BookQuery) bool {
	return e.ensureAnnotation(ctx, q, isLocalAnnotation)
}

// ensureAnnotation — общая реализация: обходит annotationProviders,
// прошедшие фильтр accept, первый непустой результат пишет в БД.
// metadata_fetched_at на промахе НЕ ставит (в отличие от cover) —
// аннотация менее критична, отметку «прогрето» ставит Prewarmer.
func (e *Enricher) ensureAnnotation(
	ctx context.Context,
	q BookQuery,
	accept func(AnnotationProvider) bool,
) bool {
	if len(e.annotationProviders) == 0 {
		return false
	}
	if !e.tryLock(e.inflightAnnotate, q.ID) {
		return false
	}
	defer e.unlock(e.inflightAnnotate, q.ID)

	var existing *string
	if err := e.pool.QueryRow(ctx, `SELECT annotation FROM books WHERE id = $1`, q.ID).Scan(&existing); err != nil {
		e.logger.Warn("metadata: query book annotation failed", "book_id", q.ID, "err", err)
		return false
	}
	if existing != nil && *existing != "" {
		return false
	}

	for _, p := range e.annotationProviders {
		if !accept(p) {
			continue
		}
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
		return true
	}
	return false
}

// ── provider filters ────────────────────────────────────────────

// localProvider — опциональный маркер провайдера, не ходящего в сеть
// (читает из наших же файлов). Реализуется Fb2Provider'ом. Прогрев
// использует только такие, чтобы не упереться в rate-limit внешних API.
type localProvider interface{ Local() bool }

func acceptAllCover(CoverProvider) bool { return true }

func isLocalCover(p CoverProvider) bool {
	lp, ok := p.(localProvider)
	return ok && lp.Local()
}

func acceptAllAnnotation(AnnotationProvider) bool { return true }

func isLocalAnnotation(p AnnotationProvider) bool {
	lp, ok := p.(localProvider)
	return ok && lp.Local()
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

// EnsureAdaptations — поиск экранизаций для книги через цепочку
// AdaptationProvider'ов. Семантика отличается от Cover/Annotation:
//
//   - "успехом" провайдера считается ЛЮБОЙ непустой результат + отсутствие
//     ошибки. Если первый провайдер вернул экранизации — следующие не
//     опрашиваются (как для Cover).
//   - Пустой результат без ErrNotFound — валидный: "книгу нашли, но
//     экранизаций нет". В этом случае пишем adaptations_fetched_at,
//     выходим, повторно не лезем (TTL обходится через скрипт-ретригер
//     enrichment'а; в текущей версии — не реализован).
//   - ErrNotFound от ВСЕХ провайдеров → книгу нигде не сопоставили;
//     adaptations_fetched_at всё равно ставим, чтобы фронт показал
//     "Экранизаций не найдено", а не вечный скелетон.
//
// Записи в book_adaptations пишутся ON CONFLICT DO NOTHING — это
// делает повторный вызов идемпотентным.
func (e *Enricher) EnsureAdaptations(ctx context.Context, q BookQuery) {
	if len(e.adaptationProviders) == 0 {
		return
	}
	if !e.tryLock(e.inflightAdaptations, q.ID) {
		return
	}
	defer e.unlock(e.inflightAdaptations, q.ID)

	var fetchedAt *time.Time
	if err := e.pool.QueryRow(ctx,
		`SELECT adaptations_fetched_at FROM books WHERE id = $1`, q.ID,
	).Scan(&fetchedAt); err != nil {
		e.logger.Warn("metadata: query book adaptations_fetched_at failed", "book_id", q.ID, "err", err)
		return
	}
	if fetchedAt != nil {
		return // уже пробовали; ретрай — отдельный механизм (вне scope этой версии)
	}

	for _, p := range e.adaptationProviders {
		items, err := p.FetchAdaptations(ctx, q)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			e.logger.Info("metadata: adaptations provider failed", "provider", p.Name(), "book_id", q.ID, "err", err)
			continue
		}
		// Успех: пишем records (даже если len==0 — это валидное
		// "ничего не сняли"). Помечаем adaptations_fetched_at.
		if err := e.saveAdaptations(ctx, q.ID, items); err != nil {
			e.logger.Warn("metadata: save adaptations failed", "provider", p.Name(), "book_id", q.ID, "err", err)
			continue
		}
		e.logger.Info("metadata: adaptations saved", "provider", p.Name(), "book_id", q.ID, "count", len(items))
		return
	}

	// Все провайдеры мимо: помечаем попытку чтобы UI показал fallback.
	if _, err := e.pool.Exec(ctx,
		`UPDATE books SET adaptations_fetched_at = now() WHERE id = $1`, q.ID,
	); err != nil {
		e.logger.Warn("metadata: mark adaptations_fetched_at failed", "book_id", q.ID, "err", err)
	}
}

// saveAdaptations — пишем найденные адаптации в БД одной транзакцией:
// сначала скачиваем все постеры (если есть PosterURL) → пишем строки →
// обновляем adaptations_fetched_at. ON CONFLICT DO NOTHING — повторный
// вызов с теми же ext_id не дублирует.
func (e *Enricher) saveAdaptations(ctx context.Context, bookID int64, items []Adaptation) error {
	// Скачиваем постеры до транзакции — IO не должен держать lock.
	posters := make([]string, len(items)) // путь в /cache/covers или ""
	for i, it := range items {
		if it.PosterURL == "" {
			continue
		}
		path, err := e.downloadPoster(ctx, it.PosterURL)
		if err != nil {
			// Постер опционален — лог и идём дальше.
			e.logger.Info("metadata: download poster failed", "book_id", bookID, "url", it.PosterURL, "err", err)
			continue
		}
		posters[i] = path
	}

	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for i, it := range items {
		// popularity=0 пишем как NULL — это означает "источник не дал
		// сигнала" (например статья в Wikidata без sitelinks), а не
		// "ровно ноль". В Service.List "NULLS LAST" положит такие
		// записи в конец.
		var popularity *int
		if it.Popularity > 0 {
			p := it.Popularity
			popularity = &p
		}
		_, err := tx.Exec(ctx, `
			INSERT INTO book_adaptations
				(book_id, provider, ext_id, title, year, director, kind, poster_path, ext_url, popularity)
			VALUES ($1, $2, $3, $4, $5, $6, $7, NULLIF($8,''), NULLIF($9,''), $10)
			ON CONFLICT (book_id, provider, ext_id) DO NOTHING
		`, bookID, it.Provider, it.ExtID, it.Title, it.Year, nullIfEmpty(it.Director), it.Kind, posters[i], it.ExtURL, popularity)
		if err != nil {
			return fmt.Errorf("insert adaptation %s: %w", it.ExtID, err)
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE books SET adaptations_fetched_at = now() WHERE id = $1`, bookID,
	); err != nil {
		return fmt.Errorf("mark adaptations_fetched_at: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// downloadPoster — скачивает картинку по URL, кэширует в /cache/covers
// (content-addressable, sha256). Возвращает имя файла для poster_path.
//
// Использует existing saveCover (тот же конвейер: temp-файл с хешем,
// переименование в финальный путь). Mime берём из Content-Type ответа.
func (e *Enricher) downloadPoster(ctx context.Context, src string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return "", fmt.Errorf("build poster request: %w", err)
	}
	// Большинство CDN (commons.wikimedia.org, image.tmdb.org) принимают
	// любой UA, но для повторяемости поведения добавим осмысленный.
	req.Header.Set("User-Agent", wdUserAgent)

	client := e.posterHTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("poster fetch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("poster status %d", resp.StatusCode)
	}
	mime := resp.Header.Get("Content-Type")
	if mime == "" || !strings.HasPrefix(mime, "image/") {
		mime = "image/jpeg"
	}
	img := &CoverImage{
		Reader: resp.Body, // saveCover закроет
		Mime:   mime,
	}
	defer func() { _ = img.Reader.Close() }()
	return e.saveCover(img)
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
