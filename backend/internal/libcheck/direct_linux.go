//go:build linux

package libcheck

import (
	"errors"
	"os"
	"syscall"
)

// openDirect открывает файл с O_DIRECT — чтение мимо кэша страниц. ФС без
// O_DIRECT (tmpfs, часть overlay) отвечает EINVAL — тогда обычное открытие.
func openDirect(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_DIRECT, 0) //nolint:gosec // путь — booksRoot + имя архива из каталога
	if errors.Is(err, syscall.EINVAL) {
		return os.Open(path) //nolint:gosec // то же
	}
	return f, err
}
