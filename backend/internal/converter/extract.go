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
//
// Пытаемся найти файл сначала по точному имени, потом по basename
// (некоторые архивы кладут файлы во вложенных директориях).
func ExtractFB2(archivePath, fb2Name string) (io.ReadCloser, int64, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, 0, fmt.Errorf("open archive %q: %w", archivePath, err)
	}
	for _, f := range zr.File {
		if f.Name == fb2Name {
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
	}
	// Fallback: ищем по basename (после последнего '/').
	for _, f := range zr.File {
		base := f.Name
		if i := lastSlash(base); i >= 0 {
			base = base[i+1:]
		}
		if base == fb2Name {
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
	}
	_ = zr.Close()
	return nil, 0, fmt.Errorf("%w: %s in %s", ErrFileNotInArchive, fb2Name, archivePath)
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
