package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

const homeFeedKey = "home_feed"

// HomeFeedConfig — персональные настройки ленты Главной (план recsys/02,
// задача 2.9). Collapsed — свёрнутые секции (recsys.SectionKind строкой;
// пустой список — все развёрнуты). Хранится в user_settings, как appearance:
// синхронно между устройствами. Допустимые значения проверяет API
// (recsys.IsSectionKind) — пакет settings секций не знает.
type HomeFeedConfig struct {
	Collapsed []string `json:"collapsed"`
}

// DefaultHomeFeedConfig — всё развёрнуто.
func DefaultHomeFeedConfig() HomeFeedConfig { return HomeFeedConfig{Collapsed: []string{}} }

// normalize — без пустых и повторов, не больше 32 записей по 64 символа;
// nil → пустой список (в JSON — [], не null).
func (c *HomeFeedConfig) normalize() {
	seen := make(map[string]struct{}, len(c.Collapsed))
	out := make([]string, 0, len(c.Collapsed))
	for _, s := range c.Collapsed {
		if s == "" || len(s) > 64 {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
		if len(out) == 32 {
			break
		}
	}
	c.Collapsed = out
}

// UserHomeFeed читает настройки ленты Главной; нет строки → дефолт.
func (s *Store) UserHomeFeed(ctx context.Context, userID int64) (HomeFeedConfig, error) {
	cfg := DefaultHomeFeedConfig()
	var raw []byte
	err := s.pool.QueryRow(ctx,
		`SELECT value FROM user_settings WHERE user_id = $1 AND key = $2`, userID, homeFeedKey,
	).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read home feed settings: %w", err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return DefaultHomeFeedConfig(), fmt.Errorf("decode home feed settings: %w", err)
	}
	cfg.normalize()
	return cfg, nil
}

// SetUserHomeFeed сохраняет настройки ленты Главной (upsert).
func (s *Store) SetUserHomeFeed(ctx context.Context, userID int64, cfg HomeFeedConfig) error {
	cfg.normalize()
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode home feed settings: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO user_settings (user_id, key, value, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (user_id, key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, userID, homeFeedKey, raw)
	if err != nil {
		return fmt.Errorf("write home feed settings: %w", err)
	}
	return nil
}
