package metadata

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// writeCacheFile пишет файл размера nbytes в каталог кэша и выставляет
// ему mtime — чтобы детерминированно проверять LRU-вытеснение.
func writeCacheFile(t *testing.T, dir, name string, nbytes int, mtime time.Time) {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, make([]byte, nbytes), 0o644))
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
	writeCacheFile(t, dir, "a", 100, base)
	c.Added(100)
	writeCacheFile(t, dir, "b", 100, base.Add(time.Minute))
	c.Added(100)
	writeCacheFile(t, dir, "c", 100, base.Add(2*time.Minute))
	c.Added(100)
	// 300 == лимит → эвикции нет.
	require.Equal(t, int64(300), c.Size())
	require.True(t, exists(dir, "a"))

	// Четвёртый файл → 400 > 300 → вытесняется старейший по mtime (a).
	writeCacheFile(t, dir, "d", 100, base.Add(3*time.Minute))
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
	writeCacheFile(t, dir, "a", 100, base)
	c.Added(100)
	writeCacheFile(t, dir, "b", 100, base.Add(time.Minute))
	c.Added(100)
	writeCacheFile(t, dir, "c", 100, base.Add(2*time.Minute))
	c.Added(100)

	// Touch старейшего (a) → его mtime становится самым свежим.
	c.Touch("a")

	// Добавляем d → эвикция: теперь старейший — b (a только что touched).
	writeCacheFile(t, dir, "d", 100, base.Add(3*time.Minute))
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

func TestCoverCache_NoLimit_NoEviction(t *testing.T) {
	dir := t.TempDir()
	// maxBytes<=0 → «полный стор», эвикции нет.
	c, err := NewCoverCache(dir, 0, 0, discardLogger())
	require.NoError(t, err)
	for i := 0; i < 50; i++ {
		name := "f" + string(rune('A'+i))
		writeCacheFile(t, dir, name, 100, time.Now())
		c.Added(100)
	}
	require.Equal(t, int64(5000), c.Size())
	require.True(t, exists(dir, "fA"), "без лимита ничего не вытесняется")
}
