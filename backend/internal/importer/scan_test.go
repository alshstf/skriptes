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
	files, _, err := w.Baseline()
	require.NoError(t, err)
	require.Equal(t, []string{a}, files)
	w.MarkDone(a) // стартовый импорт удался

	poll := func() []string {
		t.Helper()
		got, err := w.Poll()
		require.NoError(t, err)
		return got
	}
	require.Empty(t, poll(), "ничего не менялось")

	// Новый файл: отдаётся, когда не менялся между двумя проверками.
	b := filepath.Join(dir, "b.inpx")
	writeFile(t, b, "part", t0.Add(time.Minute))
	require.Empty(t, poll(), "только что появился — ждём")
	writeFile(t, b, "partial+more", t0.Add(2*time.Minute))
	require.Empty(t, poll(), "ещё пишется — ждём")
	require.Equal(t, []string{b}, poll(), "запись закончилась — импортируем")
	w.MarkDone(b)
	require.Empty(t, poll(), "уже обработан")

	// Изменился старый файл — отдаётся только он.
	writeFile(t, a, "v2", t0.Add(3*time.Minute))
	require.Empty(t, poll())
	require.Equal(t, []string{a}, poll())
	w.MarkDone(a)

	// Удаление прохода не вызывает.
	require.NoError(t, os.Remove(b))
	require.Empty(t, poll())
	require.Empty(t, poll())

	// Вернули под тем же именем — снова новый файл.
	writeFile(t, b, "again", t0.Add(4*time.Minute))
	require.Empty(t, poll())
	require.Equal(t, []string{b}, poll())
}

// Импорт упал (MarkDone не звали) — файл отдаётся на каждой следующей проверке,
// пока не получится; в том числе после неудачного стартового импорта.
func TestInpxWatch_RetriesFailedImport(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Now().Add(-time.Hour)
	a := filepath.Join(dir, "a.inpx")
	writeFile(t, a, "v1", t0)

	w := NewInpxWatch(dir, nil)
	_, _, err := w.Baseline()
	require.NoError(t, err)
	// стартовый импорт упал — MarkDone нет

	got, err := w.Poll()
	require.NoError(t, err)
	require.Equal(t, []string{a}, got, "повтор уже на первой проверке")
	got, err = w.Poll()
	require.NoError(t, err)
	require.Equal(t, []string{a}, got, "опять упал — снова повтор")

	w.MarkDone(a)
	got, err = w.Poll()
	require.NoError(t, err)
	require.Empty(t, got, "получилось — больше не отдаём")
}

// Файл, изменившийся за время импорта, после MarkDone снова считается новым.
func TestInpxWatch_ChangedDuringImport(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Now().Add(-time.Hour)
	a := filepath.Join(dir, "a.inpx")
	writeFile(t, a, "v1", t0)
	w := NewInpxWatch(dir, nil)
	_, _, err := w.Baseline()
	require.NoError(t, err)

	writeFile(t, a, "v2 — пока шёл импорт", t0.Add(time.Minute))
	w.MarkDone(a) // импорт v1 закончился
	got, err := w.Poll()
	require.NoError(t, err)
	require.Empty(t, got, "изменение замечено — ждём конца записи")
	got, err = w.Poll()
	require.NoError(t, err)
	require.Equal(t, []string{a}, got)
}

// Готовность — по каждому файлу: соседний файл, который всё ещё пишется, не
// задерживает уже дописанный.
func TestInpxWatch_PerFileStability(t *testing.T) {
	dir := t.TempDir()
	t0 := time.Now().Add(-time.Hour)
	w := NewInpxWatch(dir, nil)
	_, _, err := w.Baseline()
	require.NoError(t, err)

	a := filepath.Join(dir, "a.inpx")
	b := filepath.Join(dir, "b.inpx")
	writeFile(t, a, "done", t0)
	writeFile(t, b, "1", t0)
	got, err := w.Poll()
	require.NoError(t, err)
	require.Empty(t, got)

	writeFile(t, b, "12", t0.Add(time.Minute)) // b ещё пишется
	got, err = w.Poll()
	require.NoError(t, err)
	require.Equal(t, []string{a}, got, "a дописан — не ждём b")
	w.MarkDone(a)

	writeFile(t, b, "123", t0.Add(2*time.Minute))
	got, err = w.Poll()
	require.NoError(t, err)
	require.Empty(t, got)
	got, err = w.Poll()
	require.NoError(t, err)
	require.Equal(t, []string{b}, got)
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
	require.Empty(t, got)
	got, err = w.Poll()
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(dir, "main.inpx")}, got, "файл вне списка не импортируется")
}
