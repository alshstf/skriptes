// Package libcheck сверяет архивы книг из каталога с файлами в
// SKRIPTES_BOOKS_ROOT и проверяет, что их можно прочитать (#632).
//
// Каталог в PG и Meili живёт отдельно от тома с книгами: если том не
// подключился (диск на хосте смонтировался позже docker — bind-mount взял
// пустую папку) или хранилище под ним пропало (диск ВМ на iSCSI-датасторе
// выключенного NAS, 2026-11-04), поиск и карточки работают, а скачивание и
// ридер — нет. Healthcheck при этом зелёный. Проверка раз в минуту выставляет
// метрики skriptes_library_{archives,archives_missing,readable} (по ним алерты),
// пишет ERROR при смене состояния, а скачивание при нечитаемом томе сразу
// отвечает «недоступно», а не висит.
package libcheck

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/metrics"
)

const (
	// sampleSize — сколько имён недоступных архивов показать в логе и админке.
	sampleSize = 5
	// DiskTimeout — дольше дисковую часть проверки не ждём. Диск ВМ на
	// iSCSI-датасторе пропавшего NAS отвечает I/O error только через 6×180 с,
	// всё это время чтение (и даже stat без кэша) висит в D-state.
	DiskTimeout = 30 * time.Second
	// probeSize — сколько читаем пробой: блок, выровненный под O_DIRECT.
	probeSize = 4096
)

var (
	// ErrDiskTimeout — дисковая часть проверки не уложилась в DiskTimeout.
	ErrDiskTimeout = errors.New("books volume does not respond")
	// errDiskBusy — прошлая дисковая проверка ещё висит в ядре; новую не
	// запускаем, чтобы не копить зависшие горутины.
	errDiskBusy = errors.New("books volume does not respond (previous check still blocked)")
)

// Result — итог одной проверки.
type Result struct {
	Archives int      // архивов с живыми книгами в каталоге
	Missing  []string // из них нет в booksRoot
	// Readable — пробное чтение архива (в обход кэша страниц) прошло. stat и
	// оглавление zip отвечают из кэша ядра и при пропавшем диске — так было
	// 2026-11-04: «0 недоступных», а чтение висло по 18 минут.
	Readable bool
	ReadErr  string // почему не читается: тайм-аут или ошибка чтения
}

// Sample — первые имена недоступных архивов (не больше sampleSize).
func (r Result) Sample() []string {
	if len(r.Missing) > sampleSize {
		return r.Missing[:sampleSize]
	}
	return r.Missing
}

// Checker — периодическая проверка тома с книгами.
type Checker struct {
	pool      *pgxpool.Pool
	booksRoot string
	logger    *slog.Logger
	timeout   time.Duration

	// Смена состояния для лога (только горутина Run).
	lastMissing  int // -1 — ещё не проверяли
	lastReadable bool
	next         int // какой архив читать пробой следующим (по кругу)

	diskMu   sync.Mutex
	diskBusy bool // дисковая часть прошлой проверки ещё не вернулась из ядра

	mu        sync.RWMutex
	last      Result
	checkedAt time.Time // ноль — ещё не проверяли
}

// New создаёт проверку.
func New(pool *pgxpool.Pool, booksRoot string, logger *slog.Logger) *Checker {
	return &Checker{pool: pool, booksRoot: booksRoot, logger: logger, timeout: DiskTimeout, lastMissing: -1, lastReadable: true}
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
	return c.checkDisk(names), nil
}

// checkDisk — дисковая часть: stat всех архивов и пробное чтение одного, в
// отдельной горутине с тайм-аутом. Зависшая в ядре горутина так и остаётся
// висеть (прервать системный вызов в D-state нельзя), но проверку не держит:
// пока она не вернулась, новые не запускаются, а том считается нечитаемым.
// Горутина только читает файлы, в БД не пишет — поэтому без metadata.Go.
func (c *Checker) checkDisk(names []string) Result {
	res := Result{Archives: len(names)}
	c.diskMu.Lock()
	if c.diskBusy {
		c.diskMu.Unlock()
		res.ReadErr = errDiskBusy.Error()
		return res
	}
	c.diskBusy = true
	c.diskMu.Unlock()

	probe := c.next
	c.next++
	type diskResult struct {
		missing []string
		err     error
	}
	done := make(chan diskResult, 1)
	go func() {
		missing, err := diskPass(c.booksRoot, names, probe)
		c.diskMu.Lock()
		c.diskBusy = false
		c.diskMu.Unlock()
		done <- diskResult{missing, err}
	}()
	select {
	case r := <-done:
		res.Missing = r.missing
		res.Readable = r.err == nil
		if r.err != nil {
			res.ReadErr = r.err.Error()
		}
	case <-time.After(c.timeout):
		res.ReadErr = ErrDiskTimeout.Error()
	}
	return res
}

// diskPass — недоступные архивы и пробное чтение одного из оставшихся (probe —
// номер по кругу). Ошибка — том не читается: stat сломался не на «нет файла»
// или чтение упало. Пустой каталог — читать нечего, том в порядке; все архивы
// пропали — тоже нечитаемый.
func diskPass(root string, names []string, probe int) ([]string, error) {
	missing, err := missingArchives(root, names)
	if err != nil {
		return nil, err
	}
	present := make([]string, 0, len(names)-len(missing))
	gone := make(map[string]bool, len(missing))
	for _, m := range missing {
		gone[m] = true
	}
	for _, n := range names {
		if !gone[n] {
			present = append(present, n)
		}
	}
	if len(names) == 0 {
		return missing, nil
	}
	if len(present) == 0 {
		return missing, errors.New("no book archives on disk")
	}
	name := present[probe%len(present)]
	if err := readSample(filepath.Join(root, name)); err != nil {
		return missing, fmt.Errorf("read %s: %w", name, err)
	}
	return missing, nil
}

// missingArchives — имена из names, которых нет файлом в root. Ошибка — только
// когда сам stat сломался не на «нет файла» (например, нет прав или I/O error).
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

// readSample читает probeSize байт со случайного выровненного смещения файла
// в обход кэша страниц (O_DIRECT, где есть): иначе ответ придёт из памяти и
// пропавший диск не заметить.
func readSample(path string) error {
	f, err := openDirect(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	var off int64
	if blocks := st.Size() / probeSize; blocks > 1 {
		off = rand.Int64N(blocks-1) * probeSize //nolint:gosec // смещение пробы, не криптография
	}
	_, err = f.ReadAt(alignedBuf(probeSize), off)
	if errors.Is(err, syscall.EINVAL) {
		// O_DIRECT не принял выравнивание или размер — читаем обычным путём:
		// ложное «нечитаемо» закрыло бы все скачивания.
		plain, oerr := os.Open(path) //nolint:gosec // путь — booksRoot + имя архива из каталога, не ввод пользователя
		if oerr != nil {
			return oerr
		}
		defer func() { _ = plain.Close() }()
		_, err = plain.ReadAt(alignedBuf(probeSize), off)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// alignedBuf — буфер с началом на границе probeSize: O_DIRECT требует
// выровненный адрес, make([]byte) этого не обещает.
func alignedBuf(size int) []byte {
	buf := make([]byte, size+probeSize)
	shift := int(uintptr(unsafe.Pointer(&buf[0])) & (probeSize - 1)) // #nosec G103 — только адрес для выравнивания
	start := 0
	if shift != 0 {
		start = probeSize - shift
	}
	return buf[start : start+size]
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
	metrics.SetLibrary(res.Archives, len(res.Missing), res.Readable)
	c.report(res)
	c.mu.Lock()
	c.last, c.checkedAt = res, time.Now()
	c.mu.Unlock()
}

// Last — итог последней удачной проверки (плашка в админке). checkedAt
// нулевой — проверки ещё не было.
func (c *Checker) Last() (res Result, checkedAt time.Time) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.last, c.checkedAt
}

// Unreadable — последняя проверка не смогла прочитать том: скачивание и ридер
// отвечают сразу, а не висят в ядре (до первой проверки — читаемым).
func (c *Checker) Unreadable() (bool, string) {
	res, at := c.Last()
	if at.IsZero() || res.Readable {
		return false, ""
	}
	return true, res.ReadErr
}

// report пишет в лог только смену состояния: том перестал читаться — ERROR,
// снова читается — INFO; пропали архивы (или их число изменилось) — ERROR,
// все снова на месте — INFO. Пока ничего не меняется, строк нет: состояние
// видно по метрике.
func (c *Checker) report(res Result) {
	switch {
	case !res.Readable && c.lastReadable:
		c.logger.Error("library: books volume unreadable — downloads and reader fail", "books_root", c.booksRoot, "err", res.ReadErr)
	case res.Readable && !c.lastReadable:
		c.logger.Info("library: books volume readable again", "books_root", c.booksRoot)
	}
	c.lastReadable = res.Readable
	if res.ReadErr != "" && res.Missing == nil {
		return // диск не ответил — о пропавших архивах ничего не известно
	}
	n := len(res.Missing)
	prev := c.lastMissing
	c.lastMissing = n
	switch {
	case n > 0 && n != prev:
		msg := "library: book archives missing — downloads and reader fail for their books"
		if n == res.Archives {
			msg = "library: no book archives found — books volume not mounted? downloads and reader fail"
		}
		c.logger.Error(msg, "books_root", c.booksRoot, "missing", n, "archives", res.Archives, "sample", res.Sample())
	case n == 0 && prev > 0:
		c.logger.Info("library: all book archives available again", "books_root", c.booksRoot, "archives", res.Archives)
	}
}
