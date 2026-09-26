package db

import (
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
)

// MigrateTo — для тестов миграций: привести схему к версии version (вверх или вниз).
func MigrateTo(dsn string, version uint) error {
	m, err := newMigrate(dsn)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = m.Close()
	}()
	if err := m.Migrate(version); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate to %d: %w", version, err)
	}
	return nil
}
