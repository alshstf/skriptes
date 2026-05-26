package metadata

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// cacheFileSize — размер файлов-заглушек в тестах кэша; бюджеты в тестах
// заданы кратно ему.
const cacheFileSize = 100

// writeCacheFile пишет файл фиксированного размера в каталог кэша и
// выставляет ему mtime — чтобы детерминированно проверять LRU-вытеснение.
func writeCacheFile(t *testing.T, dir, name string, mtime time.Time) {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, make([]byte, cacheFileSize), 0o644))
	require.NoError(t, os.Chtimes(p, mtime, mtime))
}

func exists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

func TestCoverCache_EvictsOldestByMtime(t *testing.T) {
	dir := t.TempDir()
	c, err := NewCoverCache(dir, 300, 0, discardLogger())
	require.NoError(t, err)

	base := time.Now().Add(-time.Hour)
	writeCacheFile(t, dir, "a", base)
	c.Added(100)
	writeCacheFile(t, dir, "b", base.Add(time.Minute))
	c.Added(100)
	writeCacheFile(t, dir, "c", base.Add(2*time.Minute))
	c.Added(100)
	// 300 == лимит → эвикции нет.
	require.Equal(t, int64(300), c.Size())
	require.True(t, exists(dir, "a"))

	// Четвёртый файл → 400 > 300 → вытесняется старейший по mtime (a).
	writeCacheFile(t, dir, "d", base.Add(3*time.Minute))
	c.Added(100)
	require.Equal(t, int64(300), c.Size())
	require.False(t, exists(dir, "a"), "старейший по mtime должен быть вытеснен")
	require.True(t, exists(dir, "b"))
	require.True(t, exists(dir, "c"))
	require.True(t, exists(dir, "d"))
}

func TestCoverCache_TouchProtectsFromEviction(t *testing.T) {
	dir := t.TempDir()
	c, err := NewCoverCache(dir, 300, 0, discardLogger())
	require.NoError(t, err)

	base := time.Now().Add(-time.Hour)
	writeCacheFile(t, dir, "a", base)
	c.Added(100)
	writeCacheFile(t, dir, "b", base.Add(time.Minute))
	c.Added(100)
	writeCacheFile(t, dir, "c", base.Add(2*time.Minute))
	c.Added(100)

	// Touch старейшего (a) → его mtime становится самым свежим.
	c.Touch("a")

	// Добавляем d → эвикция: теперь старейший — b (a только что touched).
	writeCacheFile(t, dir, "d", base.Add(3*time.Minute))
	c.Added(100)
	require.True(t, exists(dir, "a"), "touched-файл не должен вытесняться")
	require.False(t, exists(dir, "b"), "после touch(a) старейший — b")
}

func TestCoverCache_CanWrite_MinFreeFloor(t *testing.T) {
	dir := t.TempDir()
	// minFree = огромный → запись запрещена (свободного места меньше пола).
	cBlocked, err := NewCoverCache(dir, 0, 1<<62, discardLogger())
	require.NoError(t, err)
	require.False(t, cBlocked.CanWrite(1024), "при недостижимом поле писать нельзя")

	// minFree = 0 → проверка свободного места выключена, всегда можно.
	cOpen, err := NewCoverCache(dir, 0, 0, discardLogger())
	require.NoError(t, err)
	require.True(t, cOpen.CanWrite(1<<40))
}

func TestCoverCache_SetLimits_EvictsOnTighten(t *testing.T) {
	dir := t.TempDir()
	c, err := NewCoverCache(dir, 0, 0, discardLogger()) // старт без лимита
	require.NoError(t, err)
	base := time.Now().Add(-time.Hour)
	for i := 0; i < 5; i++ {
		writeCacheFile(t, dir, fmt.Sprintf("f%d", i), base.Add(time.Duration(i)*time.Minute))
		c.Added(cacheFileSize)
	}
	require.Equal(t, int64(500), c.Size())

	// Рантайм-ужесточение бюджета → немедленная эвикция старейших.
	c.SetLimits(250, 0)
	require.LessOrEqual(t, c.Size(), int64(250))
	require.False(t, exists(dir, "f0"), "старейший вытеснен")
	require.True(t, exists(dir, "f4"), "новейший сохранён")
}

func TestCoverCache_Clear(t *testing.T) {
	dir := t.TempDir()
	c, err := NewCoverCache(dir, 0, 0, discardLogger())
	require.NoError(t, err)
	for i := 0; i < 3; i++ {
		writeCacheFile(t, dir, fmt.Sprintf("f%d", i), time.Now())
		c.Added(cacheFileSize)
	}
	n, err := c.Clear()
	require.NoError(t, err)
	require.Equal(t, 3, n)
	require.Equal(t, int64(0), c.Size())
	require.False(t, exists(dir, "f0"))
}

func TestCoverCache_NoLimit_NoEviction(t *testing.T) {
	dir := t.TempDir()
	// maxBytes<=0 → «полный стор», эвикции нет.
	c, err := NewCoverCache(dir, 0, 0, discardLogger())
	require.NoError(t, err)
	for i := 0; i < 50; i++ {
		name := "f" + string(rune('A'+i))
		writeCacheFile(t, dir, name, time.Now())
		c.Added(100)
	}
	require.Equal(t, int64(5000), c.Size())
	require.True(t, exists(dir, "fA"), "без лимита ничего не вытесняется")
}
