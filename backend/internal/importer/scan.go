package importer

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// InpxWatch следит за INPX в каталоге без рестарта backend (#247): сравнивает
// размер и mtime файлов с состоянием на последнем проходе импорта. Сам импорт
// решает по хэшу (collections.last_inpx_hash), что менять; здесь — только
// дешёвый сигнал «пора пройти».
//
// Изменённый файл отдаётся не сразу, а когда он не менялся между двумя
// проверками: торрент-клиент или копирование пишут INPX постепенно, и
// полузаписанный zip импорт бы не открыл (или открыл бы не целиком).
type InpxWatch struct {
	root     string
	only     []string
	imported map[string]fileStamp // состояние на последнем проходе импорта
	pending  map[string]fileStamp // состояние прошлой проверки, если оно отличалось
}

// fileStamp — размер и mtime (в наносекундах: сравнение через ==).
type fileStamp struct {
	size  int64
	mtime int64
}

// NewInpxWatch — root: каталог с INPX (нерекурсивно); only — явный список имён
// файлов (SKRIPTES_INPX_FILES): непустой → смотрим только на них.
func NewInpxWatch(root string, only []string) *InpxWatch {
	return &InpxWatch{root: root, only: only}
}

// Baseline запоминает текущее состояние каталога как обработанное и
// возвращает файлы для стартового импорта (по алфавиту) и имена из only,
// которых в каталоге нет. Нет каталога → пусто.
func (w *InpxWatch) Baseline() (files, missing []string, err error) {
	stamps, err := scanInpx(w.root, w.only)
	if err != nil {
		return nil, nil, err
	}
	w.imported, w.pending = stamps, nil
	for _, name := range cleanNames(w.only) {
		if _, ok := stamps[name]; !ok {
			missing = append(missing, name)
		}
	}
	return sortedPaths(w.root, stamps), missing, nil
}

// Poll возвращает новые или изменённые с последнего прохода файлы, если
// каталог не менялся с прошлой проверки; иначе nil (ждём, пока запись
// закончится). Удалённые файлы проход не вызывают.
func (w *InpxWatch) Poll() ([]string, error) {
	cur, err := scanInpx(w.root, w.only)
	if err != nil {
		return nil, err
	}
	changed := map[string]fileStamp{}
	for name, st := range cur {
		if prev, ok := w.imported[name]; !ok || prev != st {
			changed[name] = st
		}
	}
	if len(changed) == 0 {
		w.imported, w.pending = cur, nil
		return nil, nil
	}
	if w.pending == nil || !sameStamps(w.pending, cur) {
		w.pending = cur
		return nil, nil
	}
	w.imported, w.pending = cur, nil
	return sortedPaths(w.root, changed), nil
}

func scanInpx(root string, only []string) (map[string]fileStamp, error) {
	out := map[string]fileStamp{}
	if root == "" {
		return out, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return out, nil
		}
		return nil, err
	}
	allow := map[string]bool{}
	for _, name := range cleanNames(only) {
		allow[name] = true
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(name), ".inpx") {
			continue
		}
		if len(allow) > 0 && !allow[name] {
			continue
		}
		info, err := e.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue // удалили между ReadDir и Info
			}
			return nil, err
		}
		out[name] = fileStamp{size: info.Size(), mtime: info.ModTime().UnixNano()}
	}
	return out, nil
}

// cleanNames — имена из env без пробелов по краям и пустых элементов.
func cleanNames(names []string) []string {
	var out []string
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

func sameStamps(a, b map[string]fileStamp) bool {
	if len(a) != len(b) {
		return false
	}
	for name, st := range a {
		if other, ok := b[name]; !ok || other != st {
			return false
		}
	}
	return true
}

func sortedPaths(root string, stamps map[string]fileStamp) []string {
	out := make([]string, 0, len(stamps))
	for name := range stamps {
		out = append(out, filepath.Join(root, name))
	}
	sort.Strings(out)
	return out
}
