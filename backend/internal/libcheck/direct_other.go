//go:build !linux

package libcheck

import "os"

// openDirect — без O_DIRECT (macOS, разработка): чтение может прийти из кэша.
func openDirect(path string) (*os.File, error) {
	return os.Open(path) //nolint:gosec // путь — booksRoot + имя архива из каталога, не ввод пользователя
}
