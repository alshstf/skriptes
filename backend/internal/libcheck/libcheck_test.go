package libcheck

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

	c.report(Result{Archives: 3})
	require.Empty(t, lines(), "всё на месте с самого старта — без строк")

	c.report(Result{Archives: 3, Missing: []string{"a.zip", "b.zip", "c.zip"}})
	got := lines()
	require.Len(t, got, 1)
	require.Contains(t, got[0], "level=ERROR")
	require.Contains(t, got[0], "not mounted")

	c.report(Result{Archives: 3, Missing: []string{"a.zip", "b.zip", "c.zip"}})
	require.Empty(t, lines(), "то же состояние — без повторной строки")

	c.report(Result{Archives: 3, Missing: []string{"c.zip"}})
	got = lines()
	require.Len(t, got, 1)
	require.Contains(t, got[0], "level=ERROR")
	require.NotContains(t, got[0], "not mounted")

	c.report(Result{Archives: 3})
	got = lines()
	require.Len(t, got, 1)
	require.Contains(t, got[0], "level=INFO")
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

	// Итог прохода — для плашки в админке.
	c.runOnce(ctx)
	last, at := c.Last()
	require.False(t, at.IsZero())
	require.Equal(t, res, last)
	require.Equal(t, []string{"gone.zip"}, last.Sample())
}
