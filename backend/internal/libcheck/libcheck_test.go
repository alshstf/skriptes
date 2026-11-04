package libcheck

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestMissingArchives(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.zip"), []byte("PK"), 0o600))

	missing, err := missingArchives(root, []string{"a.zip", "b.zip"})
	require.NoError(t, err)
	require.Equal(t, []string{"b.zip"}, missing)

	// Том не подключён: папки нет вовсе — все архивы недоступны, не ошибка.
	missing, err = missingArchives(filepath.Join(root, "absent"), []string{"a.zip"})
	require.NoError(t, err)
	require.Equal(t, []string{"a.zip"}, missing)
}

// TestReport — в лог попадает только смена состояния: первая пропажа (ERROR,
// с подсказкой про том, если пропало всё), смена числа (ERROR), восстановление
// (INFO); повтор того же состояния молчит.
func TestReport(t *testing.T) {
	var buf bytes.Buffer
	c := New(nil, "/data/books", slog.New(slog.NewTextHandler(&buf, nil)))
	lines := func() []string {
		out := strings.Split(strings.TrimSpace(buf.String()), "\n")
		buf.Reset()
		if len(out) == 1 && out[0] == "" {
			return nil
		}
		return out
	}

	ok := func(missing ...string) Result {
		return Result{Archives: 3, Missing: missing, Readable: true}
	}
	c.report(ok())
	require.Empty(t, lines(), "всё на месте с самого старта — без строк")

	c.report(ok("a.zip", "b.zip", "c.zip"))
	got := lines()
	require.Len(t, got, 1)
	require.Contains(t, got[0], "level=ERROR")
	require.Contains(t, got[0], "not mounted")

	c.report(ok("a.zip", "b.zip", "c.zip"))
	require.Empty(t, lines(), "то же состояние — без повторной строки")

	c.report(ok("c.zip"))
	got = lines()
	require.Len(t, got, 1)
	require.Contains(t, got[0], "level=ERROR")
	require.NotContains(t, got[0], "not mounted")

	c.report(ok())
	got = lines()
	require.Len(t, got, 1)
	require.Contains(t, got[0], "level=INFO")

	// Том перестал отвечать: одна строка ERROR, о пропавших архивах — ничего
	// (диск не ответил, неизвестно); снова читается — INFO.
	c.report(Result{Archives: 3, ReadErr: ErrDiskTimeout.Error()})
	got = lines()
	require.Len(t, got, 1)
	require.Contains(t, got[0], "level=ERROR")
	require.Contains(t, got[0], "unreadable")
	c.report(Result{Archives: 3, ReadErr: ErrDiskTimeout.Error()})
	require.Empty(t, lines())
	c.report(ok())
	got = lines()
	require.Len(t, got, 1)
	require.Contains(t, got[0], "readable again")
}

// TestCheckDisk_Hang — том не отвечает (open FIFO без писателя висит, как
// чтение с пропавшего iSCSI): проверка не ждёт дольше тайм-аута, пока прошлая
// висит — новую не запускает, а когда диск ответил — снова читаемый.
func TestCheckDisk_Hang(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, syscall.Mkfifo(filepath.Join(root, "a.zip"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.zip"), bytes.Repeat([]byte("PK"), 8192), 0o600))
	c := New(nil, root, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.timeout = 200 * time.Millisecond
	names := []string{"a.zip", "b.zip"}

	start := time.Now()
	res := c.checkDisk(names) // проба читает a.zip — висит
	require.Less(t, time.Since(start), 2*time.Second)
	require.False(t, res.Readable)
	require.Equal(t, ErrDiskTimeout.Error(), res.ReadErr)

	res = c.checkDisk(names)
	require.False(t, res.Readable)
	require.Equal(t, errDiskBusy.Error(), res.ReadErr, "прошлая проверка ещё в ядре — новую не запускаем")

	// «Диск ответил»: писатель отпускает open, зависшая горутина возвращается.
	// Открываем в цикле и без блокировки: на Linux проба открывает FIFO дважды
	// (O_DIRECT → EINVAL → обычное открытие), и оба раза ждёт писателя.
	require.Eventually(t, func() bool {
		if w, err := os.OpenFile(filepath.Join(root, "a.zip"), os.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = w.Close()
		}
		c.diskMu.Lock()
		defer c.diskMu.Unlock()
		return !c.diskBusy
	}, 5*time.Second, 10*time.Millisecond)

	res = c.checkDisk(names) // проба по кругу — b.zip
	require.True(t, res.Readable, res.ReadErr)
	require.Empty(t, res.Missing)
}

func TestDiskPass(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.zip"), []byte("PK"), 0o600))

	missing, err := diskPass(root, nil, 0)
	require.NoError(t, err, "пустой каталог — читать нечего, том в порядке")
	require.Empty(t, missing)

	missing, err = diskPass(root, []string{"a.zip", "b.zip"}, 1)
	require.NoError(t, err, "проба берёт только архивы, которые есть")
	require.Equal(t, []string{"b.zip"}, missing)

	missing, err = diskPass(filepath.Join(root, "absent"), []string{"a.zip"}, 0)
	require.Error(t, err, "архивов на диске нет вовсе — том нечитаемый")
	require.Equal(t, []string{"a.zip"}, missing)
}

func TestAlignedBuf(t *testing.T) {
	for range 8 {
		b := alignedBuf(probeSize)
		require.Len(t, b, probeSize)
		require.Zero(t, uintptr(unsafe.Pointer(&b[0]))%probeSize)
	}
}

// TestCheck_Integration — в проверку идут только архивы с неудалёнными книгами
// и без пометки available = false.
func TestCheck_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)

	var collID int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO collections (name, inpx_filename) VALUES ('c','c.inpx') RETURNING id`).Scan(&collID))
	archive := func(name string, available bool) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO archives (collection_id, filename, available) VALUES ($1,$2,$3) RETURNING id`,
			collID, name, available).Scan(&id))
		return id
	}
	book := func(archID int64, file string, deleted bool) {
		_, err := pool.Exec(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, deleted)
			VALUES ($1,$2,$3,$4,'fb2',$5,$6,$7)`, collID, archID, file, file, file, file, deleted)
		require.NoError(t, err)
	}
	book(archive("live.zip", true), "1", false)
	book(archive("gone.zip", true), "2", false)
	book(archive("deleted-only.zip", true), "3", true)
	book(archive("x_lost.zip", false), "4", false)
	archive("empty.zip", true)

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "live.zip"), []byte("PK"), 0o600))

	c := New(pool, root, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, at := c.Last()
	require.True(t, at.IsZero(), "до первой проверки")

	res, err := c.Check(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, res.Archives)
	require.Equal(t, []string{"gone.zip"}, res.Missing)

	require.True(t, res.Readable, res.ReadErr)

	// Итог прохода — для плашки в админке.
	bad, _ := c.Unreadable()
	require.False(t, bad, "до первой проверки — читаемый")
	c.runOnce(ctx)
	last, at := c.Last()
	require.False(t, at.IsZero())
	require.Equal(t, res, last)
	require.Equal(t, []string{"gone.zip"}, last.Sample())
	bad, _ = c.Unreadable()
	require.False(t, bad)
}

// TestReadSample_Path — ручная проверка пробы на настоящем томе:
// LIBCHECK_PROBE_PATH=/путь/к/архиву go test -run ReadSample_Path.
func TestReadSample_Path(t *testing.T) {
	path := os.Getenv("LIBCHECK_PROBE_PATH")
	if path == "" {
		t.Skip("LIBCHECK_PROBE_PATH не задан")
	}
	for range 5 {
		start := time.Now()
		require.NoError(t, readSample(path))
		t.Logf("read %d B in %s", probeSize, time.Since(start))
	}
}
