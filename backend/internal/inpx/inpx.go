package inpx

import (
	"archive/zip"
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"path"
	"strconv"
	"strings"
)

// CollectionInfo — содержимое collection.info внутри INPX.
// Формат: 5 строк (Name, Prefix, Version, Description, URL).
// Пропущенные строки = "" / 0.
type CollectionInfo struct {
	Name        string
	Prefix      string
	Version     int
	Description string
	URL         string
}

// InpFile описывает один .inp в INPX и связанный с ним архив книг.
type InpFile struct {
	Name    string // "fb2-749080-749080.inp"
	Archive string // "fb2-749080-749080.zip"
	Lost    bool   // имя содержит "_lost" — соответствующий .zip отсутствует
	Size    int64  // размер .inp в байтах
}

// Inpx — открытый дескриптор INPX-файла.
// Закрывайте через Close после использования.
type Inpx struct {
	Path       string
	Collection CollectionInfo
	Version    string // содержимое version.info как есть
	Schema     Schema
	Files      []InpFile

	zr     *zip.ReadCloser
	byName map[string]*zip.File
}

// Open открывает INPX, читает version.info / collection.info / structure.info
// и регистрирует список .inp.
// Сами .inp не читает — для этого используйте Each.
func Open(p string) (*Inpx, error) {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return nil, fmt.Errorf("open inpx: %w", err)
	}
	i := &Inpx{
		Path:   p,
		zr:     zr,
		byName: make(map[string]*zip.File, len(zr.File)),
	}
	for _, f := range zr.File {
		base := path.Base(f.Name)
		i.byName[base] = f
	}

	if v, ok := i.byName["version.info"]; ok {
		s, err := readZipString(v)
		if err != nil {
			_ = zr.Close()
			return nil, fmt.Errorf("read version.info: %w", err)
		}
		i.Version = strings.TrimSpace(s)
	}

	if c, ok := i.byName["collection.info"]; ok {
		ci, err := readCollectionInfo(c)
		if err != nil {
			_ = zr.Close()
			return nil, fmt.Errorf("read collection.info: %w", err)
		}
		i.Collection = ci
	}

	i.Schema = DefaultSchema
	if s, ok := i.byName["structure.info"]; ok {
		rc, err := s.Open()
		if err != nil {
			_ = zr.Close()
			return nil, fmt.Errorf("open structure.info: %w", err)
		}
		schema, err := ParseSchema(rc)
		_ = rc.Close()
		if err != nil {
			_ = zr.Close()
			return nil, err
		}
		i.Schema = schema
	}

	for _, f := range zr.File {
		base := path.Base(f.Name)
		if !strings.HasSuffix(strings.ToLower(base), ".inp") {
			continue
		}
		stem := strings.TrimSuffix(base, ".inp")
		lost := strings.HasSuffix(stem, "_lost")
		archiveStem := strings.TrimSuffix(stem, "_lost")
		size := f.UncompressedSize64
		if size > math.MaxInt64 {
			size = math.MaxInt64
		}
		i.Files = append(i.Files, InpFile{
			Name:    base,
			Archive: archiveStem + ".zip",
			Lost:    lost,
			Size:    int64(size),
		})
	}
	return i, nil
}

// Close освобождает zip.ReadCloser.
func (i *Inpx) Close() error {
	if i.zr == nil {
		return nil
	}
	err := i.zr.Close()
	i.zr = nil
	return err
}

// Each вызывает fn для каждой записи в каждом .inp по порядку.
// Если fn возвращает ошибку — итерация прерывается.
func (i *Inpx) Each(fn func(file InpFile, rec Record) error) error {
	for _, inp := range i.Files {
		f := i.byName[inp.Name]
		if f == nil {
			return fmt.Errorf("inp not found in zip: %s", inp.Name)
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("open %s: %w", inp.Name, err)
		}
		err = ParseInp(rc, i.Schema, func(rec Record) error {
			return fn(inp, rec)
		})
		if cerr := rc.Close(); cerr != nil && err == nil {
			err = cerr
		}
		if err != nil {
			return fmt.Errorf("parse %s: %w", inp.Name, err)
		}
	}
	return nil
}

// readCollectionInfo читает 5-строчный формат с возможным UTF-8 BOM.
func readCollectionInfo(f *zip.File) (CollectionInfo, error) {
	rc, err := f.Open()
	if err != nil {
		return CollectionInfo{}, err
	}
	defer func() { _ = rc.Close() }()

	br := bufio.NewReader(rc)
	var lines []string
	for len(lines) < 5 {
		line, err := br.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		// убираем UTF-8 BOM из первой строки если есть
		if len(lines) == 0 {
			line = strings.TrimPrefix(line, "\ufeff")
		}
		lines = append(lines, line)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return CollectionInfo{}, err
		}
	}
	for len(lines) < 5 {
		lines = append(lines, "")
	}
	ci := CollectionInfo{
		Name:        strings.TrimSpace(lines[0]),
		Prefix:      strings.TrimSpace(lines[1]),
		Description: strings.TrimSpace(lines[3]),
		URL:         strings.TrimSpace(lines[4]),
	}
	if v, err := strconv.Atoi(strings.TrimSpace(lines[2])); err == nil {
		ci.Version = v
	}
	return ci, nil
}

func readZipString(f *zip.File) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
