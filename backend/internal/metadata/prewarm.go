package metadata

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Prewarmer — фоновый прогрев обложек и аннотаций из fb2.
//
// Зачем: fb2-обложка есть у ~99% книг и достаётся локально из zip без
// внешних API. Но EnsureCover/EnsureAnnotation стреляют лениво только
// при открытии карточки → в списке книг у большинства обложек нет, и
// thumbnail'ы выглядят как пустые плейсхолдеры. Прогрев извлекает их
// заранее (local-only), чтобы список был реально с обложками.
//
// Чего НЕ делает: не ходит во внешние провайдеры (Open Library /
// Google Books) — у них rate-limit, а fb2-промахи это ~1%, их добирает
// ленивый путь при открытии карточки.
//
// Идемпотентность: после обработки книги ставит metadata_fetched_at,
// поэтому она не перечитывается каждый цикл. Отметка не блокирует
// ленивый внешний путь (он гейтится по cover_path/annotation).
type Prewarmer struct {
	enricher  *Enricher
	pool      *pgxpool.Pool
	booksRoot string
	logger    *slog.Logger
	workers   int
}

const (
	prewarmBatchSize      = 200
	prewarmDefaultWorkers = 4
	prewarmRescanInterval = 5 * time.Minute
	prewarmTaskTimeout    = 30 * time.Second
)

// NewPrewarmer создаёт прогрев. workers<=0 → дефолт. booksRoot — корень
// read-only volume с zip-архивами (нужен fb2-провайдеру).
func NewPrewarmer(e *Enricher, pool *pgxpool.Pool, booksRoot string, workers int, logger *slog.Logger) *Prewarmer {
	if workers <= 0 {
		workers = prewarmDefaultWorkers
	}
	return &Prewarmer{enricher: e, pool: pool, booksRoot: booksRoot, workers: workers, logger: logger}
}

// Run — долгоживущий цикл: прогреть все ещё-не-прогретые книги, затем
// поспать и пересканировать (чтобы свежеимпортированные тоже прогрелись
// без рестарта). Завершается по отмене ctx. Блокирующий — вызывать в
// отдельной горутине.
func (p *Prewarmer) Run(ctx context.Context) {
	if p.enricher == nil || p.pool == nil || p.booksRoot == "" {
		return
	}
	p.logger.Info("cover prewarm: started", "workers", p.workers)
	for {
		n := p.drain(ctx)
		if ctx.Err() != nil {
			return
		}
		if n > 0 {
			p.logger.Info("cover prewarm: pass complete", "processed", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(prewarmRescanInterval):
		}
	}
}

// drain прогоняет все pending-книги батчами по возрастанию id, пока они
// не кончатся. Курсор по id гарантирует продвижение вперёд независимо
// от того, удалось ли пометить книгу (не зацикливается на «застрявшей»).
// Возвращает сколько книг обработал.
func (p *Prewarmer) drain(ctx context.Context) int {
	total := 0
	var cursor int64
	for {
		if ctx.Err() != nil {
			return total
		}
		batch, err := p.fetchBatch(ctx, cursor, prewarmBatchSize)
		if err != nil {
			p.logger.Warn("cover prewarm: fetch batch failed", "err", err)
			return total
		}
		if len(batch) == 0 {
			return total
		}
		p.processBatch(ctx, batch)
		total += len(batch)
		cursor = batch[len(batch)-1].id
	}
}

type prewarmBook struct {
	id       int64
	title    string
	lang     string
	archive  string
	fileName string
	ext      string
}

func (p *Prewarmer) fetchBatch(ctx context.Context, afterID int64, limit int) ([]prewarmBook, error) {
	rows, err := p.pool.Query(ctx, `
		SELECT b.id, b.title, COALESCE(b.lang, ''), a.filename, b.file_name, b.ext
		FROM books b
		JOIN archives a ON a.id = b.archive_id
		WHERE b.deleted = false AND b.metadata_fetched_at IS NULL AND b.id > $1
		ORDER BY b.id
		LIMIT $2
	`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]prewarmBook, 0, limit)
	for rows.Next() {
		var b prewarmBook
		if err := rows.Scan(&b.id, &b.title, &b.lang, &b.archive, &b.fileName, &b.ext); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// processBatch обрабатывает батч пулом из p.workers горутин.
func (p *Prewarmer) processBatch(ctx context.Context, batch []prewarmBook) {
	sem := make(chan struct{}, p.workers)
	var wg sync.WaitGroup
	for _, b := range batch {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(b prewarmBook) {
			defer wg.Done()
			defer func() { <-sem }()
			p.processOne(ctx, b)
		}(b)
	}
	wg.Wait()
}

func (p *Prewarmer) processOne(ctx context.Context, b prewarmBook) {
	taskCtx, cancel := context.WithTimeout(ctx, prewarmTaskTimeout)
	defer cancel()
	q := BookQuery{
		ID:          b.id,
		Title:       b.title,
		Lang:        b.lang,
		ArchivePath: filepath.Join(p.booksRoot, b.archive),
		FB2Name:     b.fileName + "." + b.ext,
	}
	// Authors не нужны — fb2-провайдер читает из zip, не ищет по имени.
	p.enricher.EnsureCoverLocal(taskCtx, q)
	p.enricher.EnsureAnnotationLocal(taskCtx, q)
	// Помечаем «прогрето» независимо от результата: fb2-промахи не должны
	// перечитываться каждый цикл. AND metadata_fetched_at IS NULL — на
	// случай если Ensure* уже проставил его при успехе (no-op тогда).
	if _, err := p.pool.Exec(ctx,
		`UPDATE books SET metadata_fetched_at = now() WHERE id = $1 AND metadata_fetched_at IS NULL`,
		b.id,
	); err != nil {
		p.logger.Warn("cover prewarm: mark fetched_at failed", "book_id", b.id, "err", err)
	}
}
