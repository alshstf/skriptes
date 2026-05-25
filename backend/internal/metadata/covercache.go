package metadata

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
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
// maxBytes <= 0 → без лимита размера (режим «полного стора» для прогрева;
// тогда единственный ограничитель — minFree). minFree <= 0 → проверка
// свободного места отключена (не рекомендуется).
type CoverCache struct {
	root     string
	maxBytes int64
	minFree  int64
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
	c := &CoverCache{root: root, maxBytes: maxBytes, minFree: minFree, logger: logger}
	c.sizeBytes = c.scanSize()
	return c, nil
}

// Path — абсолютный путь к файлу в кэше по имени.
func (c *CoverCache) Path(name string) string { return filepath.Join(c.root, name) }

// CanWrite — хватит ли места записать файл размера size так, чтобы
// свободного осталось не меньше minFree. На ошибке statfs не блокируем
// (лучше записать, чем ложно отказать).
func (c *CoverCache) CanWrite(size int64) bool {
	if c.minFree <= 0 {
		return true
	}
	free := c.freeBytes()
	if free < 0 {
		return true
	}
	return free-size >= c.minFree
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
	over := c.maxBytes > 0 && c.sizeBytes > c.maxBytes
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

func (c *CoverCache) freeBytes() int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(c.root, &st); err != nil {
		return -1
	}
	// Bavail — доступно непривилегированному пользователю; Bsize — размер
	// блока. int64-конверсии нужны т.к. типы полей различаются по ОС
	// (linux: int64, darwin: uint32). Оба неотрицательны — переполнение
	// int64 на реальных дисках нереалистично.
	return int64(st.Bavail) * int64(st.Bsize) //nolint:gosec // ФС-размеры неотрицательны, overflow int64 нереалистичен
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
// размер не уйдёт под maxBytes. Полный readdir+sort делается только в
// момент превышения (редко), а не на каждую запись.
func (c *CoverCache) evictToLimit() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.maxBytes <= 0 || c.sizeBytes <= c.maxBytes {
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
		if c.sizeBytes <= c.maxBytes {
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
		"removed", removed, "freed_bytes", freed, "size_bytes", c.sizeBytes, "limit_bytes", c.maxBytes)
}
