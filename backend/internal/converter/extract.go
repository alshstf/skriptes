package converter

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"math"
)

// ErrFileNotInArchive — внутри zip нет файла с ожидаемым именем.
// Скорее всего INPX устарел или коллекция изменилась — зовите импорт.
var ErrFileNotInArchive = errors.New("file not found in archive")

// ExtractFB2 открывает zip и возвращает поток данных fb2-файла.
// Caller должен закрыть и Reader, и returned ReadCloser.
func ExtractFB2(archivePath, fb2Name string) (io.ReadCloser, int64, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, 0, fmt.Errorf("open archive %q: %w", archivePath, err)
	}
	f := findFB2(zr, fb2Name)
	if f == nil {
		_ = zr.Close()
		return nil, 0, fmt.Errorf("%w: %s in %s", ErrFileNotInArchive, fb2Name, archivePath)
	}
	rc, err := f.Open()
	if err != nil {
		_ = zr.Close()
		return nil, 0, fmt.Errorf("open inner file: %w", err)
	}
	size := f.UncompressedSize64
	if size > math.MaxInt64 {
		size = math.MaxInt64
	}
	return &readCloserWithParent{rc: rc, parent: zr}, int64(size), nil
}

// CheckFB2 — есть ли fb2-файл в архиве (читается только оглавление zip,
// сам файл не распаковывается). ErrFileNotInArchive — архив есть, файла нет.
func CheckFB2(archivePath, fb2Name string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open archive %q: %w", archivePath, err)
	}
	defer func() { _ = zr.Close() }()
	if findFB2(zr, fb2Name) == nil {
		return fmt.Errorf("%w: %s in %s", ErrFileNotInArchive, fb2Name, archivePath)
	}
	return nil
}

// findFB2 ищет файл сначала по точному имени, потом по basename (некоторые
// архивы кладут файлы во вложенных директориях).
func findFB2(zr *zip.ReadCloser, fb2Name string) *zip.File {
	for _, f := range zr.File {
		if f.Name == fb2Name {
			return f
		}
	}
	for _, f := range zr.File {
		base := f.Name
		if i := lastSlash(base); i >= 0 {
			base = base[i+1:]
		}
		if base == fb2Name {
			return f
		}
	}
	return nil
}

// readCloserWithParent объединяет inner ReadCloser и parent zip ReadCloser
// чтобы Close закрыл оба.
type readCloserWithParent struct {
	rc     io.ReadCloser
	parent *zip.ReadCloser
}

func (r *readCloserWithParent) Read(p []byte) (int, error) { return r.rc.Read(p) }

func (r *readCloserWithParent) Close() error {
	innerErr := r.rc.Close()
	parentErr := r.parent.Close()
	if innerErr != nil {
		return innerErr
	}
	return parentErr
}

func lastSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' || s[i] == '\\' {
			return i
		}
	}
	return -1
}
