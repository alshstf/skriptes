// Package settings — рантайм-настройки приложения, хранящиеся в таблице
// app_settings (generic key/value JSONB) и редактируемые из админки без
// рестарта. Дефолты живут в коде; в БД лежат только оверрайды.
package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const coverKey = "cover_cache"

// CoverConfig — рантайм-настройки кэша обложек.
//
//	CacheMaxMB     — бюджет дискового кэша (LRU-эвикция при превышении);
//	                 0 = без лимита («полный стор» под прогрев).
//	CacheMinFreeMB — пол свободного места: ниже него новые обложки не
//	                 пишутся (защита раздела с postgres). Рекомендуется
//	                 держать ≥ 100 МБ.
//	Prewarm        — фоновый прогрев обложек всей коллекции (full-режим).
type CoverConfig struct {
	CacheMaxMB     int  `json:"cache_max_mb"`
	CacheMinFreeMB int  `json:"cache_min_free_mb"`
	Prewarm        bool `json:"prewarm"`
}

// DefaultCoverConfig — безопасные дефолты (применяются если в БД нет
// оверрайда). Прогрев выключен; кэш ограничен, есть пол свободного места.
func DefaultCoverConfig() CoverConfig {
	return CoverConfig{CacheMaxMB: 8192, CacheMinFreeMB: 1024, Prewarm: false}
}

// Store — доступ к app_settings.
type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Cover читает настройки кэша обложек; если оверрайда в БД нет — отдаёт
// дефолты. Поля, отсутствующие в JSON, остаются дефолтными (мердж поверх
// DefaultCoverConfig через json.Unmarshal в уже-заполненную структуру).
func (s *Store) Cover(ctx context.Context) (CoverConfig, error) {
	cfg := DefaultCoverConfig()
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, coverKey).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read cover settings: %w", err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return DefaultCoverConfig(), fmt.Errorf("decode cover settings: %w", err)
	}
	return cfg, nil
}

// SetCover сохраняет настройки кэша обложек (upsert).
func (s *Store) SetCover(ctx context.Context, cfg CoverConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode cover settings: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, coverKey, raw)
	if err != nil {
		return fmt.Errorf("save cover settings: %w", err)
	}
	return nil
}
