package metadata

import (
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// CoverCache — ограниченный по размеру дисковый кэш обложек (и прочих
// картинок: фото авторов, постеры) в одном каталоге.
//
// Политика вытеснения — LRU по mtime: на отдаче файла вызывается Touch
// (обновляет mtime), а вытесняются файлы с самым старым mtime. Это
// дёшево и без отдельного индекса — файловая система сама себе индекс.
//
// Две гарантии «никогда не забьём диск»:
//   - перед записью CanWrite проверяет свободное место ≥ minFree;
//   - после записи Added запускает эвикцию, если суммарный размер
//     превысил maxBytes.
//
// Лимиты (maxBytes/minFree) атомарны и меняются в рантайме через
// SetLimits — админка правит их без рестарта. maxBytes<=0 → без лимита
// размера (режим «полного стора» под прогрев); minFree<=0 → проверка
// свободного места отключена (не рекомендуется).
type CoverCache struct {
	root     string
	maxBytes atomic.Int64
	minFree  atomic.Int64
	logger   *slog.Logger

	mu        sync.Mutex
	sizeBytes int64
}

// NewCoverCache создаёт кэш и сканирует текущий размер каталога.
func NewCoverCache(root string, maxBytes, minFree int64, logger *slog.Logger) (*CoverCache, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return nil, fmt.Errorf("mkdir cover cache: %w", err)
	}
	c := &CoverCache{root: root, logger: logger}
	c.maxBytes.Store(maxBytes)
	c.minFree.Store(minFree)
	c.sizeBytes = c.scanSize()
	return c, nil
}

// SetLimits меняет лимиты в рантайме (из админки). При ужесточении
// бюджета сразу подчищает лишнее.
func (c *CoverCache) SetLimits(maxBytes, minFree int64) {
	c.maxBytes.Store(maxBytes)
	c.minFree.Store(minFree)
	c.evictToLimit()
}

// Path — абсолютный путь к файлу в кэше по имени.
func (c *CoverCache) Path(name string) string { return filepath.Join(c.root, name) }

// Root — корневой каталог кэша.
func (c *CoverCache) Root() string { return c.root }

// Save пишет картинку в кэш под content-addressable именем
// {sha256}.{ext(mime)} и возвращает имя файла. Идемпотентно: одинаковые байты
// → один файл (повторная запись переиспользует существующий, размер не
// удваивается). При нехватке свободного места (ниже minFree) — ErrCacheFull,
// файл не пишется (не фатально).
func (c *CoverCache) Save(r io.Reader, mime string) (string, error) {
	if !c.CanWrite(0) {
		return "", ErrCacheFull
	}
	tmp, err := os.CreateTemp(c.root, "img-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create temp: %w", err)
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()

	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, h), r)
	if err != nil {
		return "", fmt.Errorf("copy image: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close temp: %w", err)
	}

	filename := fmt.Sprintf("%x%s", h.Sum(nil), extFromMime(mime))
	dst := filepath.Join(c.root, filename)
	if err := os.Rename(tmp.Name(), dst); err != nil {
		// dst уже есть — идентичный файл; переиспользуем (в учёт не добавляем).
		if _, statErr := os.Stat(dst); statErr == nil {
			return filename, nil
		}
		return "", fmt.Errorf("rename to %s: %w", dst, err)
	}
	c.Added(size)
	return filename, nil
}

// CanWrite — хватит ли места записать файл размера size так, чтобы
// свободного осталось не меньше minFree. На ошибке statfs не блокируем
// (лучше записать, чем ложно отказать).
func (c *CoverCache) CanWrite(size int64) bool {
	minFree := c.minFree.Load()
	if minFree <= 0 {
		return true
	}
	free := c.freeBytes()
	if free < 0 {
		return true
	}
	return free-size >= minFree
}

// Touch — отметка доступа для LRU (обновляет mtime). Best-effort.
func (c *CoverCache) Touch(name string) {
	now := time.Now()
	_ = os.Chtimes(c.Path(name), now, now)
}

// Added — сообщить кэшу, что добавлен новый файл размера size.
// При превышении maxBytes запускает эвикцию старейших по mtime.
func (c *CoverCache) Added(size int64) {
	c.mu.Lock()
	c.sizeBytes += size
	over := c.maxBytes.Load() > 0 && c.sizeBytes > c.maxBytes.Load()
	c.mu.Unlock()
	if over {
		c.evictToLimit()
	}
}

// Size — текущий суммарный размер кэша (для статистики админки).
func (c *CoverCache) Size() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sizeBytes
}

// FreeBytes — свободно на разделе кэша (для статистики админки); -1 если
// не удалось определить.
func (c *CoverCache) FreeBytes() int64 { return c.freeBytes() }

// Clear удаляет все файлы кэша и обнуляет учёт размера. Возвращает число
// удалённых файлов.
func (c *CoverCache) Clear() (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entries, err := os.ReadDir(c.root)
	if err != nil {
		return 0, fmt.Errorf("readdir for clear: %w", err)
	}
	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if err := os.Remove(filepath.Join(c.root, e.Name())); err == nil {
			removed++
		}
	}
	c.sizeBytes = 0
	return removed, nil
}

func (c *CoverCache) freeBytes() int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(c.root, &st); err != nil {
		return -1
	}
	// Bavail (uint64 на linux/darwin) — доступные блоки; Bsize — размер
	// блока (linux: int64, darwin: uint32). Считаем в uint64 (конверсия
	// Bsize необходима на обеих ОС → unconvert доволен), итог в int64.
	// Размеры ФС неотрицательны, overflow int64 нереалистичен.
	return int64(st.Bavail * uint64(st.Bsize)) //nolint:gosec // overflow int64 на реальных дисках нереалистичен
}

func (c *CoverCache) scanSize() int64 {
	var total int64
	entries, err := os.ReadDir(c.root)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if info, err := e.Info(); err == nil {
			total += info.Size()
		}
	}
	return total
}

// evictToLimit удаляет файлы по возрастанию mtime (LRU), пока суммарный
// размер не уйдёт под maxBytes. Полный readdir+sort делается только при
// превышении (редко), а не на каждую запись.
func (c *CoverCache) evictToLimit() {
	c.mu.Lock()
	defer c.mu.Unlock()
	maxBytes := c.maxBytes.Load()
	if maxBytes <= 0 || c.sizeBytes <= maxBytes {
		return
	}
	entries, err := os.ReadDir(c.root)
	if err != nil {
		c.logger.Warn("cover cache: readdir for eviction failed", "err", err)
		return
	}
	type ent struct {
		name string
		size int64
		mod  time.Time
	}
	files := make([]ent, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		files = append(files, ent{e.Name(), info.Size(), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.Before(files[j].mod) })

	freed := int64(0)
	removed := 0
	for _, f := range files {
		if c.sizeBytes <= maxBytes {
			break
		}
		if err := os.Remove(filepath.Join(c.root, f.name)); err != nil {
			continue
		}
		c.sizeBytes -= f.size
		freed += f.size
		removed++
	}
	c.logger.Info("cover cache: evicted (LRU by mtime)",
		"removed", removed, "freed_bytes", freed, "size_bytes", c.sizeBytes, "limit_bytes", maxBytes)
}
