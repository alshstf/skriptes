package importer

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// InpxWatch следит за INPX в каталоге без рестарта backend (#247): сравнивает
// размер и mtime файлов с отметками последнего удачного импорта. Сам импорт
// решает по хэшу (collections.last_inpx_hash), что менять; здесь — только
// дешёвый сигнал «пора пройти».
//
// Каждый файл отдаётся, когда он сам не менялся между двумя проверками:
// торрент-клиент или копирование пишут INPX постепенно, и полузаписанный zip
// импорт бы не открыл (или открыл бы не целиком). Соседние файлы на это не
// влияют.
//
// Обработанным файл становится только после MarkDone — удачного импорта или
// осознанного пропуска. Импорт упал (БД, Meili, битый zip) → файл отдаётся
// снова на каждой следующей проверке, пока не получится или не изменится.
type InpxWatch struct {
	root    string
	only    []string
	done    map[string]fileStamp // отметка на последнем удачном импорте
	pending map[string]fileStamp // отметка прошлой проверки у ещё не обработанных
	offered map[string]fileStamp // отметка, с которой файл отдан в импорт (для MarkDone)
}

// fileStamp — размер и mtime (в наносекундах: сравнение через ==).
type fileStamp struct {
	size  int64
	mtime int64
}

// NewInpxWatch — root: каталог с INPX (нерекурсивно); only — явный список имён
// файлов (SKRIPTES_INPX_FILES): непустой → смотрим только на них.
func NewInpxWatch(root string, only []string) *InpxWatch {
	return &InpxWatch{
		root: root, only: only,
		done: map[string]fileStamp{}, pending: map[string]fileStamp{}, offered: map[string]fileStamp{},
	}
}

// Baseline — файлы для стартового импорта (все, по алфавиту) и имена из only,
// которых в каталоге нет. Нет каталога → пусто. Файл, который на старте не
// импортировался (и не изменился), вернёт уже первая проверка Poll.
func (w *InpxWatch) Baseline() (files, missing []string, err error) {
	cur, err := scanInpx(w.root, w.only)
	if err != nil {
		return nil, nil, err
	}
	for name, st := range cur {
		w.pending[name] = st
		w.offered[name] = st
	}
	for _, name := range cleanNames(w.only) {
		if _, ok := cur[name]; !ok {
			missing = append(missing, name)
		}
	}
	return sortedPaths(w.root, cur), missing, nil
}

// Poll возвращает файлы, которые пора импортировать: новые, изменённые с
// последнего удачного импорта или не импортировавшиеся из-за ошибки — если
// с прошлой проверки они не менялись. Удалённые файлы проход не вызывают.
func (w *InpxWatch) Poll() ([]string, error) {
	cur, err := scanInpx(w.root, w.only)
	if err != nil {
		return nil, err
	}
	for _, m := range []map[string]fileStamp{w.done, w.pending, w.offered} {
		for name := range m {
			if _, ok := cur[name]; !ok {
				delete(m, name)
			}
		}
	}
	ready := map[string]fileStamp{}
	for name, st := range cur {
		if prev, ok := w.done[name]; ok && prev == st {
			delete(w.pending, name)
			continue
		}
		if prev, ok := w.pending[name]; ok && prev == st {
			ready[name] = st
			w.offered[name] = st
			continue
		}
		w.pending[name] = st // новый или ещё пишется — ждём следующей проверки
	}
	return sortedPaths(w.root, ready), nil
}

// MarkDone — файл импортирован или осознанно пропущен (не изменился по хэшу,
// дубль коллекции): до следующего изменения его не отдаём. Отметка — та, с
// которой файл был отдан: изменился за время импорта → Poll увидит.
func (w *InpxWatch) MarkDone(path string) {
	name := filepath.Base(path)
	st, ok := w.offered[name]
	if !ok {
		return
	}
	w.done[name] = st
	delete(w.offered, name)
	if w.pending[name] == st {
		delete(w.pending, name)
	}
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

func sortedPaths(root string, stamps map[string]fileStamp) []string {
	out := make([]string, 0, len(stamps))
	for name := range stamps {
		out = append(out, filepath.Join(root, name))
	}
	sort.Strings(out)
	return out
}
