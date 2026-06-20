package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

const ratingPromptsKey = "rating_prompts"

// RatingPromptConfig — персональные настройки отложенных запросов оценки
// (раздел профиля). Хранится в user_settings (как appearance).
//
//	Enabled   — показывать ли блок «Оцените прочитанное» этому пользователю;
//	DelayDays — через сколько дней после приобретения (Send-to-Kindle /
//	            скачивание) книга считается вероятно прочитанной и попадает
//	            в запрос оценки. Тот же интервал — длительность «спросить позже».
type RatingPromptConfig struct {
	Enabled   bool `json:"enabled"`
	DelayDays int  `json:"delay_days"`
}

// DefaultRatingPromptConfig — включено, задержка 30 дней.
func DefaultRatingPromptConfig() RatingPromptConfig {
	return RatingPromptConfig{Enabled: true, DelayDays: 30}
}

// normalize зажимает задержку в разумные пределы (1..365 дней); мусор → дефолт.
func (c *RatingPromptConfig) normalize() {
	if c.DelayDays < 1 || c.DelayDays > 365 {
		c.DelayDays = 30
	}
}

// UserRatingPromptConfig читает настройки запросов оценки; нет строки → дефолт.
func (s *Store) UserRatingPromptConfig(ctx context.Context, userID int64) (RatingPromptConfig, error) {
	cfg := DefaultRatingPromptConfig()
	var raw []byte
	err := s.pool.QueryRow(ctx,
		`SELECT value FROM user_settings WHERE user_id = $1 AND key = $2`, userID, ratingPromptsKey,
	).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read rating-prompt settings: %w", err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return DefaultRatingPromptConfig(), fmt.Errorf("decode rating-prompt settings: %w", err)
	}
	cfg.normalize()
	return cfg, nil
}

// SetUserRatingPromptConfig сохраняет настройки запросов оценки (upsert).
func (s *Store) SetUserRatingPromptConfig(ctx context.Context, userID int64, cfg RatingPromptConfig) error {
	cfg.normalize()
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode rating-prompt settings: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO user_settings (user_id, key, value, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (user_id, key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, userID, ratingPromptsKey, raw)
	if err != nil {
		return fmt.Errorf("save rating-prompt settings: %w", err)
	}
	return nil
}
