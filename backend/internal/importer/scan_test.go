package importer

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func writeFile(t *testing.T, path, content string, mtime time.Time) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	require.NoError(t, os.Chtimes(path, mtime, mtime))
}

func TestInpxWatch_Baseline(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Now().Add(-time.Hour)
	writeFile(t, filepath.Join(dir, "b.inpx"), "b", t0)
	writeFile(t, filepath.Join(dir, "A.INPX"), "a", t0)
	writeFile(t, filepath.Join(dir, "notes.txt"), "x", t0)
	writeFile(t, filepath.Join(dir, "c.inpx.part"), "c", t0) // недокачанный — не INPX
	require.NoError(t, os.Mkdir(filepath.Join(dir, "d.inpx"), 0o700))

	files, missing, err := NewInpxWatch(dir, nil).Baseline()
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(dir, "A.INPX"), filepath.Join(dir, "b.inpx")}, files)
	require.Empty(t, missing)

	files, missing, err = NewInpxWatch(dir, []string{" b.inpx", "", "gone.inpx"}).Baseline()
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(dir, "b.inpx")}, files, "явный список — только перечисленные")
	require.Equal(t, []string{"gone.inpx"}, missing)

	files, missing, err = NewInpxWatch(filepath.Join(dir, "nope"), nil).Baseline()
	require.NoError(t, err, "нет каталога — пусто, не ошибка")
	require.Empty(t, files)
	require.Empty(t, missing)
}

func TestInpxWatch_Poll(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Now().Add(-time.Hour)
	a := filepath.Join(dir, "a.inpx")
	writeFile(t, a, "v1", t0)

	w := NewInpxWatch(dir, nil)
	_, _, err := w.Baseline()
	require.NoError(t, err)

	poll := func() []string {
		t.Helper()
		got, err := w.Poll()
		require.NoError(t, err)
		return got
	}
	require.Nil(t, poll(), "ничего не менялось")

	// Новый файл: отдаётся, когда не менялся между двумя проверками.
	b := filepath.Join(dir, "b.inpx")
	writeFile(t, b, "part", t0.Add(time.Minute))
	require.Nil(t, poll(), "только что появился — ждём")
	writeFile(t, b, "partial+more", t0.Add(2*time.Minute))
	require.Nil(t, poll(), "ещё пишется — ждём")
	require.Equal(t, []string{b}, poll(), "запись закончилась — импортируем")
	require.Nil(t, poll(), "уже обработан")

	// Изменился старый файл — отдаётся только он.
	writeFile(t, a, "v2", t0.Add(3*time.Minute))
	require.Nil(t, poll())
	require.Equal(t, []string{a}, poll())

	// Удаление прохода не вызывает.
	require.NoError(t, os.Remove(b))
	require.Nil(t, poll())
	require.Nil(t, poll())

	// Вернули под тем же именем — снова новый файл.
	writeFile(t, b, "again", t0.Add(4*time.Minute))
	require.Nil(t, poll())
	require.Equal(t, []string{b}, poll())
}

func TestInpxWatch_PollRespectsAllowList(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Now().Add(-time.Hour)
	w := NewInpxWatch(dir, []string{"main.inpx"})
	_, _, err := w.Baseline()
	require.NoError(t, err)

	writeFile(t, filepath.Join(dir, "other.inpx"), "x", t0)
	writeFile(t, filepath.Join(dir, "main.inpx"), "x", t0)
	got, err := w.Poll()
	require.NoError(t, err)
	require.Nil(t, got)
	got, err = w.Poll()
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(dir, "main.inpx")}, got, "файл вне списка не импортируется")
}
