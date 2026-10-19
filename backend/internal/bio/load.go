package bio

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Dataset — загруженный и провалидированный датасет.
type Dataset struct {
	// Authors — по QID.
	Authors map[string]*Author
	// Context — по региону.
	Context map[string]*ContextRegion
	// Version — у исходников sha256 всех файлов, у пакета — sha256 пакета
	// (16 hex); меняется при любой правке датасета → триггер пересева в PG.
	Version string
}

// LoadFS — датасет из исходников: <root>/authors/<QID>.json и
// <root>/context/<region>.json (чекаут alshstf/skriptes-bio, testdata).
// Ошибка валидации любого файла — ошибка загрузки: `biogen check` и
// `biogen pack` не пропускают битый датасет в образ. Рантайм читает не
// исходники, а пакет (pack.go).
func LoadFS(fsys fs.FS, root string) (*Dataset, error) {
	ds := &Dataset{Authors: map[string]*Author{}, Context: map[string]*ContextRegion{}}
	h := sha256.New()
	var files []string
	for _, sub := range []string{"authors", "context"} {
		entries, err := fs.ReadDir(fsys, path.Join(root, sub))
		if err != nil {
			return nil, fmt.Errorf("bio: read %s: %w", sub, err)
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			files = append(files, path.Join(root, sub, e.Name()))
		}
	}
	sort.Strings(files)
	var errs []string
	for _, f := range files {
		raw, err := fs.ReadFile(fsys, f)
		if err != nil {
			return nil, fmt.Errorf("bio: read %s: %w", f, err)
		}
		h.Write([]byte(f))
		h.Write([]byte{0})
		h.Write(raw)
		h.Write([]byte{0})
		switch {
		case strings.Contains(f, "/authors/"):
			var a Author
			if err := json.Unmarshal(raw, &a); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", f, err))
				continue
			}
			if want := strings.TrimSuffix(path.Base(f), ".json"); want != a.QID {
				errs = append(errs, fmt.Sprintf("%s: имя файла ≠ qid %q", f, a.QID))
			}
			if ps := Validate(&a).Errors(); len(ps) > 0 {
				errs = append(errs, fmt.Sprintf("%s: %s", f, ps.Error()))
				continue
			}
			if _, dup := ds.Authors[a.QID]; dup {
				errs = append(errs, fmt.Sprintf("%s: дубль qid %s", f, a.QID))
				continue
			}
			ds.Authors[a.QID] = &a
		case strings.Contains(f, "/context/"):
			var c ContextRegion
			if err := json.Unmarshal(raw, &c); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", f, err))
				continue
			}
			if want := strings.TrimSuffix(path.Base(f), ".json"); want != c.Region {
				errs = append(errs, fmt.Sprintf("%s: имя файла ≠ region %q", f, c.Region))
			}
			ds.Context[c.Region] = &c
		}
	}
	// Регионы фона у авторов должны существовать.
	for qid, a := range ds.Authors {
		for _, r := range a.Context {
			if _, ok := ds.Context[r]; !ok {
				errs = append(errs, fmt.Sprintf("%s: нет региона фона %q", qid, r))
			}
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("bio: датасет невалиден:\n  %s", strings.Join(errs, "\n  "))
	}
	ds.Version = hex.EncodeToString(h.Sum(nil))[:16]
	return ds, nil
}
