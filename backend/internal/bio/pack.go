package bio

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

// Пакет датасета — то, что приложение получает вместо исходников. Исходники
// (по файлу на автора, с evidence) живут в отдельном репо alshstf/skriptes-bio;
// сборка образа скачивает закреплённый коммит (dataset.ref) и `biogen pack`
// пишет пакет: gzip JSONL — первая строка заголовок, дальше регионы фона, потом
// авторы, по строке на запись, без evidence (рантайм его не показывает, это
// четверть объёма). Порядок важен: при неизменной версии рантайм читает только
// заголовок и фон.

// PackFormat, PackFormatVersion — заголовок пакета; несовпадение — ошибка
// чтения (пакет от несовместимой версии biogen).
const (
	PackFormat        = "skriptes-bio-pack"
	PackFormatVersion = 1
)

type packHeader struct {
	Format  string `json:"format"`
	Version int    `json:"version"`
}

type packLine struct {
	Context *ContextRegion `json:"context,omitempty"`
	Author  *Author        `json:"author,omitempty"`
}

// WritePack — датасет → пакет: регионы фона и авторы по алфавиту ключей,
// evidence вырезан.
func WritePack(w io.Writer, ds *Dataset) error {
	gz, err := gzip.NewWriterLevel(w, gzip.BestCompression)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(gz)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(packHeader{Format: PackFormat, Version: PackFormatVersion}); err != nil {
		return err
	}
	regions := make([]string, 0, len(ds.Context))
	for r := range ds.Context {
		regions = append(regions, r)
	}
	sort.Strings(regions)
	for _, r := range regions {
		if err := enc.Encode(packLine{Context: ds.Context[r]}); err != nil {
			return err
		}
	}
	qids := make([]string, 0, len(ds.Authors))
	for q := range ds.Authors {
		qids = append(qids, q)
	}
	sort.Strings(qids)
	for _, q := range qids {
		if err := enc.Encode(packLine{Author: StripEvidence(ds.Authors[q])}); err != nil {
			return err
		}
	}
	return gz.Close()
}

// StripEvidence — копия автора без цитат-оснований (в рантайм они не уходят).
func StripEvidence(a *Author) *Author {
	c := *a
	c.Events = make([]Event, len(a.Events))
	for i, e := range a.Events {
		e.Evidence = nil
		c.Events[i] = e
	}
	return &c
}

// PackFile — пакет на диске. Version — sha256 файла (16 hex, как у датасета):
// считается без разбора, чтобы при неизменной версии не читать авторов.
type PackFile struct {
	Path    string
	Version string
}

// OpenPack — пакет по пути; os.ErrNotExist пробрасывается как есть (образ без
// пакета — таймлайн выключен, а не ошибка старта).
func OpenPack(path string) (*PackFile, error) {
	f, err := os.Open(path) //nolint:gosec // путь из конфига владельца инстанса
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, fmt.Errorf("bio: pack %s: %w", path, err)
	}
	return &PackFile{Path: path, Version: hex.EncodeToString(h.Sum(nil))[:16]}, nil
}

// Read — содержимое пакета; withAuthors=false — только регионы фона (авторы
// идут после них, чтение останавливается на первом). Каждый автор проходит
// Validate: пакет собирается из проверенных исходников, но битый файл образа
// не должен попасть в PG.
func (p *PackFile) Read(withAuthors bool) (*Dataset, error) {
	f, err := os.Open(p.Path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	ds, err := ReadPack(f, withAuthors)
	if err != nil {
		return nil, fmt.Errorf("bio: pack %s: %w", p.Path, err)
	}
	ds.Version = p.Version
	return ds, nil
}

// ReadPack — разбор пакета из потока (Version не заполняется: её знает PackFile).
func ReadPack(r io.Reader, withAuthors bool) (*Dataset, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}
	defer func() { _ = gz.Close() }()
	sc := bufio.NewScanner(gz)
	sc.Buffer(make([]byte, 0, 1<<20), 32<<20)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return nil, err
		}
		return nil, errors.New("пустой пакет")
	}
	var hdr packHeader
	if err := json.Unmarshal(sc.Bytes(), &hdr); err != nil || hdr.Format != PackFormat {
		return nil, fmt.Errorf("не пакет датасета (заголовок %q)", truncateBytes(sc.Bytes(), 80))
	}
	if hdr.Version != PackFormatVersion {
		return nil, fmt.Errorf("формат пакета %d, ожидается %d — пересоберите biogen pack", hdr.Version, PackFormatVersion)
	}
	ds := &Dataset{Authors: map[string]*Author{}, Context: map[string]*ContextRegion{}}
	var errs []string
	for sc.Scan() {
		var l packLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			return nil, fmt.Errorf("строка пакета: %w", err)
		}
		switch {
		case l.Context != nil:
			ds.Context[l.Context.Region] = l.Context
		case l.Author != nil:
			if !withAuthors {
				return ds, nil
			}
			if ps := Validate(l.Author).Errors(); len(ps) > 0 {
				errs = append(errs, l.Author.QID+": "+ps.Error())
				continue
			}
			ds.Authors[l.Author.QID] = l.Author
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("датасет невалиден:\n  %s", strings.Join(errs, "\n  "))
	}
	return ds, nil
}

func truncateBytes(b []byte, n int) string {
	if len(b) > n {
		b = b[:n]
	}
	return string(b)
}
