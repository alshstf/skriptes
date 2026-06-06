package metadata

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Prewarmer — фоновая ОБРАБОТКА КОЛЛЕКЦИИ: проход по fb2 и извлечение из них
// обложек / аннотаций / годов (что из этого делать — задаётся PrewarmConfig).
// Всё локально, из наших zip-архивов, без внешних API → без rate-limit'ов.
//
// Год написания (written_year) синкается в Meili-поле year автоматически в
// конце прохода, если за проход он у каких-то книг появился (YearResyncer).
type Prewarmer struct {
	enricher  *Enricher
	pool      *pgxpool.Pool
	booksRoot string
	logger    *slog.Logger
	cfg       PrewarmConfig
	resyncer  YearResyncer // nil → авто-ресинк года выключен

	yearChanged atomic.Int64 // сколько книг получили written_year за текущий проход
}

// PrewarmConfig — что и как обрабатывать (из settings.CoverConfig: под-тумблеры
// + интенсивность).
type PrewarmConfig struct {
	Covers      bool
	Annotations bool
	Years       bool
	Workers     int           // параллелизм (из пресета интенсивности)
	Delay       time.Duration // пауза между книгами (троттлинг IO на медленных дисках)
}

// YearResyncer пере-синкивает Meili-поле year из books.written_year
// (реализуется *importer.Importer). Прогрев дёргает после прохода, в котором
// год у каких-то книг появился — чтобы фильтр/сортировка «Год» на /books были
// актуальны без ручной кнопки.
type YearResyncer interface {
	ResyncYears(ctx context.Context) (int, error)
}

const (
	prewarmBatchSize      = 200
	prewarmRescanInterval = 5 * time.Minute
	prewarmTaskTimeout    = 30 * time.Second
)

// NewPrewarmer создаёт прогрев. booksRoot — корень read-only volume с
// zip-архивами. resyncer может быть nil (тогда без авто-ресинка года).
func NewPrewarmer(e *Enricher, pool *pgxpool.Pool, booksRoot string, cfg PrewarmConfig, resyncer YearResyncer, logger *slog.Logger) *Prewarmer {
	if cfg.Workers <= 0 {
		cfg.Workers = 2
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Prewarmer{enricher: e, pool: pool, booksRoot: booksRoot, cfg: cfg, resyncer: resyncer, logger: logger}
}

// hasWork — есть ли вообще что синкать по текущему конфигу.
func (c PrewarmConfig) hasWork() bool { return c.Covers || c.Annotations || c.Years }

// candidateCond — SQL-условие «книга ещё не обработана» по включённым типам.
// Маркеры: metadata_fetched_at (обложки/аннотации), year_local_scanned_at (года).
func candidateCond(cfg PrewarmConfig) string {
	var conds []string
	if cfg.Covers || cfg.Annotations {
		conds = append(conds, "b.metadata_fetched_at IS NULL")
	}
	if cfg.Years {
		// Под тумблером «Года» идёт и год, и атрибуты издания — оба из заголовка
		// fb2. Маркеры РАЗНЫЕ (year_local_scanned_at / edition_meta_scanned_at),
		// чтобы уже-просканированные на год книги добрали edition-поля.
		conds = append(conds, "(b.year_local_scanned_at IS NULL OR b.edition_meta_scanned_at IS NULL)")
	}
	if len(conds) == 0 {
		return "false"
	}
	return "(" + strings.Join(conds, " OR ") + ")"
}

// Run — долгоживущий цикл: обработать всё pending, поспать, пересканить.
// Блокирующий; вызывать в горутине. Завершается по отмене ctx.
func (p *Prewarmer) Run(ctx context.Context) {
	if p.enricher == nil || p.pool == nil || p.booksRoot == "" || !p.cfg.hasWork() {
		return
	}
	p.logger.Info("collection processing: started",
		"workers", p.cfg.Workers, "covers", p.cfg.Covers, "annotations", p.cfg.Annotations, "years", p.cfg.Years)
	for {
		n := p.drain(ctx)
		if ctx.Err() != nil {
			return
		}
		if n > 0 {
			p.logger.Info("collection processing: pass complete", "processed", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(prewarmRescanInterval):
		}
	}
}

// drain прогоняет все pending-книги батчами по возрастанию id. В конце, если
// за проход появились года — авто-синк Meili. Возвращает сколько обработал.
func (p *Prewarmer) drain(ctx context.Context) int {
	if !p.cfg.hasWork() {
		return 0
	}
	p.yearChanged.Store(0)
	total := 0
	var cursor int64
	for {
		if ctx.Err() != nil {
			return total
		}
		if (p.cfg.Covers) && !p.enricher.CoverCacheHasRoom() {
			p.logger.Warn("collection processing: свободного места ниже порога — пауза до следующего прохода", "processed", total)
			break
		}
		batch, err := p.fetchBatch(ctx, cursor, prewarmBatchSize)
		if err != nil {
			p.logger.Warn("collection processing: fetch batch failed", "err", err)
			break
		}
		if len(batch) == 0 {
			break
		}
		p.processBatch(ctx, batch)
		total += len(batch)
		cursor = batch[len(batch)-1].id
	}
	p.maybeResyncYears(ctx)
	return total
}

// maybeResyncYears — авто-синк Meili-поля year, если за проход год у каких-то
// книг появился.
func (p *Prewarmer) maybeResyncYears(ctx context.Context) {
	if !p.cfg.Years || p.resyncer == nil || p.yearChanged.Load() == 0 || ctx.Err() != nil {
		return
	}
	if n, err := p.resyncer.ResyncYears(ctx); err != nil {
		p.logger.Warn("collection processing: resync years failed", "err", err)
	} else {
		p.logger.Info("collection processing: years resynced to meili", "changed", p.yearChanged.Load(), "synced", n)
	}
}

type prewarmBook struct {
	id             int64
	title          string
	lang           string
	archive        string
	fileName       string
	ext            string
	yearScanned    bool // year_local_scanned_at IS NOT NULL
	editionScanned bool // edition_meta_scanned_at IS NOT NULL
}

func (p *Prewarmer) fetchBatch(ctx context.Context, afterID int64, limit int) ([]prewarmBook, error) {
	q := fmt.Sprintf(`
		SELECT b.id, b.title, COALESCE(b.lang, ''), a.filename, b.file_name, b.ext,
		       (b.year_local_scanned_at IS NOT NULL), (b.edition_meta_scanned_at IS NOT NULL)
		FROM books b
		JOIN archives a ON a.id = b.archive_id
		WHERE b.deleted = false AND %s AND b.id > $1
		ORDER BY b.id
		LIMIT $2
	`, candidateCond(p.cfg))
	rows, err := p.pool.Query(ctx, q, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]prewarmBook, 0, limit)
	for rows.Next() {
		var b prewarmBook
		if err := rows.Scan(&b.id, &b.title, &b.lang, &b.archive, &b.fileName, &b.ext,
			&b.yearScanned, &b.editionScanned); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (p *Prewarmer) processBatch(ctx context.Context, batch []prewarmBook) {
	sem := make(chan struct{}, p.cfg.Workers)
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
	if p.cfg.Covers {
		p.enricher.EnsureCoverLocal(taskCtx, q)
	}
	if p.cfg.Annotations {
		p.enricher.EnsureAnnotationLocal(taskCtx, q)
	}
	// Маркер «обложки/аннотации пробовали» ставим только если хотя бы одно из
	// них включено — иначе candidateCond по metadata_fetched_at не участвует.
	if p.cfg.Covers || p.cfg.Annotations {
		if _, err := p.pool.Exec(ctx,
			`UPDATE books SET metadata_fetched_at = now() WHERE id = $1 AND metadata_fetched_at IS NULL`, b.id); err != nil {
			p.logger.Warn("collection processing: mark fetched_at failed", "book_id", b.id, "err", err)
		}
	}
	if p.cfg.Years {
		if !b.yearScanned {
			if p.enricher.EnsureYearLocal(taskCtx, q) {
				p.yearChanged.Add(1)
			}
		}
		if !b.editionScanned {
			p.enricher.EnsureEditionMeta(taskCtx, q)
		}
	}
	// Троттлинг IO между книгами (низкая интенсивность на медленных дисках).
	if p.cfg.Delay > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(p.cfg.Delay):
		}
	}
}

// PrewarmController — рантайм-управление джобой обработки коллекции:
// мастер вкл/выкл (SetEnabled) + смена под-настроек (SetConfig, с перезапуском)
// + разовый прогон (RunOnce). Создаётся один раз; Prewarmer'ы — на каждый запуск.
type PrewarmController struct {
	enricher  *Enricher
	pool      *pgxpool.Pool
	booksRoot string
	resyncer  YearResyncer
	logger    *slog.Logger

	mu         sync.Mutex
	cfg        PrewarmConfig
	contCancel context.CancelFunc
	onceCancel context.CancelFunc
}

// PrewarmStatus — текущее состояние для UI.
type PrewarmStatus struct {
	Running bool   `json:"prewarm_running"`
	Mode    string `json:"prewarm_mode"` // "off" | "continuous" | "once"
}

func NewPrewarmController(e *Enricher, pool *pgxpool.Pool, booksRoot string, cfg PrewarmConfig, resyncer YearResyncer, logger *slog.Logger) *PrewarmController {
	if logger == nil {
		logger = slog.Default()
	}
	return &PrewarmController{enricher: e, pool: pool, booksRoot: booksRoot, cfg: cfg, resyncer: resyncer, logger: logger}
}

func (pc *PrewarmController) ready() bool {
	return pc.enricher != nil && pc.pool != nil && pc.booksRoot != ""
}

func (pc *PrewarmController) Status() PrewarmStatus {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	switch {
	case pc.onceCancel != nil:
		return PrewarmStatus{Running: true, Mode: "once"}
	case pc.contCancel != nil:
		return PrewarmStatus{Running: true, Mode: "continuous"}
	default:
		return PrewarmStatus{Running: false, Mode: "off"}
	}
}

// Start запускает непрерывную обработку (идемпотентно).
func (pc *PrewarmController) Start() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.contCancel != nil || !pc.ready() || !pc.cfg.hasWork() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	pc.contCancel = cancel
	p := NewPrewarmer(pc.enricher, pc.pool, pc.booksRoot, pc.cfg, pc.resyncer, pc.logger)
	go p.Run(ctx)
	pc.logger.Info("collection processing: continuous job started")
}

// Stop останавливает непрерывную обработку.
func (pc *PrewarmController) Stop() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.contCancel == nil {
		return
	}
	pc.contCancel()
	pc.contCancel = nil
	pc.logger.Info("collection processing: continuous job stopped")
}

// SetEnabled — мастер-тумблер: вкл → Start, выкл → Stop.
func (pc *PrewarmController) SetEnabled(on bool) {
	if on {
		pc.Start()
	} else {
		pc.Stop()
	}
}

// SetConfig применяет новые под-настройки (тумблеры/интенсивность). Если
// непрерывная джоба запущена — перезапускает, чтобы подхватить cfg.
func (pc *PrewarmController) SetConfig(cfg PrewarmConfig) {
	pc.mu.Lock()
	pc.cfg = cfg
	running := pc.contCancel != nil
	pc.mu.Unlock()
	if running {
		pc.Stop()
		pc.Start()
	}
}

// RunOnce делает ОДИН проход (кнопка «Запустить сейчас»), не запуская
// непрерывный цикл. No-op если уже идёт разовый или активен непрерывный.
func (pc *PrewarmController) RunOnce() {
	pc.mu.Lock()
	if pc.onceCancel != nil || pc.contCancel != nil || !pc.ready() || !pc.cfg.hasWork() {
		pc.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	pc.onceCancel = cancel
	cfg := pc.cfg
	pc.mu.Unlock()
	go func() {
		p := NewPrewarmer(pc.enricher, pc.pool, pc.booksRoot, cfg, pc.resyncer, pc.logger)
		n := p.drain(ctx)
		cancel()
		pc.mu.Lock()
		pc.onceCancel = nil
		pc.mu.Unlock()
		pc.logger.Info("collection processing: one-shot pass done", "processed", n)
	}()
}

// StopOnce отменяет идущий разовый прогон (между батчами).
func (pc *PrewarmController) StopOnce() {
	pc.mu.Lock()
	defer pc.mu.Unlock()
	if pc.onceCancel == nil {
		return
	}
	pc.onceCancel()
	pc.logger.Info("collection processing: one-shot pass stop requested")
}
