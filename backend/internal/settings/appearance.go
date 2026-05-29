package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

const appearanceKey = "appearance"

// AppearanceConfig — персональные настройки внешнего вида (раздел «Внешний
// вид» в профиле). Чисто визуальные предпочтения пользователя; хранятся в
// user_settings (синхронно между устройствами), на фронте дублируются в
// localStorage для мгновенного рендера без вспышки.
//
//	GenreChipStyle — стиль жанровых меток в списках:
//	  "soft"    — приглушённые (по умолчанию);
//	  "classic" — контрастные плашки (как было раньше).
type AppearanceConfig struct {
	GenreChipStyle string `json:"genre_chip_style"`
}

// DefaultAppearanceConfig — дефолтный внешний вид (приглушённые чипы).
func DefaultAppearanceConfig() AppearanceConfig {
	return AppearanceConfig{GenreChipStyle: "soft"}
}

// normalize приводит к допустимому значению: всё, кроме "classic", → "soft"
// (не выдумываем семантику неизвестных значений — безопасный дефолт).
func (c *AppearanceConfig) normalize() {
	if c.GenreChipStyle != "classic" {
		c.GenreChipStyle = "soft"
	}
}

// UserAppearance читает персональные настройки внешнего вида; нет строки →
// дефолт.
func (s *Store) UserAppearance(ctx context.Context, userID int64) (AppearanceConfig, error) {
	cfg := DefaultAppearanceConfig()
	var raw []byte
	err := s.pool.QueryRow(ctx,
		`SELECT value FROM user_settings WHERE user_id = $1 AND key = $2`, userID, appearanceKey,
	).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read appearance settings: %w", err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return DefaultAppearanceConfig(), fmt.Errorf("decode appearance settings: %w", err)
	}
	cfg.normalize()
	return cfg, nil
}

// SetUserAppearance сохраняет персональные настройки внешнего вида (upsert).
func (s *Store) SetUserAppearance(ctx context.Context, userID int64, cfg AppearanceConfig) error {
	cfg.normalize()
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode appearance settings: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO user_settings (user_id, key, value, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (user_id, key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, userID, appearanceKey, raw)
	if err != nil {
		return fmt.Errorf("save appearance settings: %w", err)
	}
	return nil
}
