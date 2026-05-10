// Package db оркестрирует подключение к PostgreSQL и применение миграций.
package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // register pgx5 driver
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// NewPool открывает пул соединений к PostgreSQL.
// Делает ping с retry, чтобы дать БД время подняться (актуально в docker compose).
func NewPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse dsn: %w", err)
	}
	cfg.MaxConns = 20
	cfg.MinConns = 2
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 10 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("new pool: %w", err)
	}
	if err := pingWithRetry(ctx, pool, 30, time.Second); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

func pingWithRetry(ctx context.Context, pool *pgxpool.Pool, attempts int, backoff time.Duration) error {
	var lastErr error
	for i := range attempts {
		if err := pool.Ping(ctx); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if i == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
	return fmt.Errorf("ping after %d attempts: %w", attempts, lastErr)
}

// Migrate применяет все pending up-миграции. На уже накатанной БД — no-op.
func Migrate(dsn string) error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("iofs source: %w", err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, toMigrateURL(dsn))
	if err != nil {
		return fmt.Errorf("migrate init: %w", err)
	}
	defer func() {
		_, _ = m.Close()
	}()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// toMigrateURL переписывает 'postgres://...' / 'postgresql://...' в 'pgx5://...',
// потому что golang-migrate v4 c pgx5-драйвером ждёт именно эту схему.
func toMigrateURL(dsn string) string {
	for _, p := range []string{"postgres://", "postgresql://"} {
		if strings.HasPrefix(dsn, p) {
			return "pgx5://" + dsn[len(p):]
		}
	}
	return dsn
}
