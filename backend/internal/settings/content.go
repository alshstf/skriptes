package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const contentKey = "content"

// ContentConfig — настройки видимости контента: какие жанры (по fb2-коду)
// и языки (по коду) скрыты из выдачи. Один и тот же тип используется и для
// глобальных (admin) настроек в app_settings, и для персональных настроек
// пользователя в user_settings.
//
// Пустые срезы = ничего не скрыто (всё видно) — безопасный дефолт: новый
// жанр/язык, появившийся в коллекции, по умолчанию виден.
type ContentConfig struct {
	HiddenGenres    []string `json:"hidden_genres"`
	HiddenLanguages []string `json:"hidden_languages"`
}

// DefaultContentConfig — ничего не скрыто. Срезы не-nil, чтобы JSON-ответ
// был `[]`, а не `null` (фронту удобнее не проверять на null).
func DefaultContentConfig() ContentConfig {
	return ContentConfig{HiddenGenres: []string{}, HiddenLanguages: []string{}}
}

// normalize приводит срезы к каноничному виду: убирает пустые строки и
// дубли, сортирует (стабильный JSON в БД), гарантирует не-nil.
func (c *ContentConfig) normalize() {
	c.HiddenGenres = cleanCodes(c.HiddenGenres)
	c.HiddenLanguages = cleanCodes(c.HiddenLanguages)
}

// Hides сообщает, скрывает ли этот конфиг книгу с данными жанрами/языком.
// Книга скрыта, если её язык в списке скрытых ИЛИ хотя бы один её жанр
// скрыт (мульти-жанровая книга прячется, если хоть один жанр запрещён —
// этого и ждёшь от «не показывать эротику»).
func (c ContentConfig) Hides(genres []string, lang string) bool {
	if lang != "" && slices.Contains(c.HiddenLanguages, lang) {
		return true
	}
	for _, g := range genres {
		if slices.Contains(c.HiddenGenres, g) {
			return true
		}
	}
	return false
}

func cleanCodes(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func unionCodes(a, b []string) []string {
	return cleanCodes(append(append([]string{}, a...), b...))
}

// Content читает глобальные (admin) настройки видимости контента. Нет
// оверрайда в БД → дефолт (ничего не скрыто).
func (s *Store) Content(ctx context.Context) (ContentConfig, error) {
	return scanContent(ctx, s.pool, `SELECT value FROM app_settings WHERE key = $1`, contentKey)
}

// SetContent сохраняет глобальные настройки видимости контента (upsert).
func (s *Store) SetContent(ctx context.Context, cfg ContentConfig) error {
	cfg.normalize()
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode content settings: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, contentKey, raw)
	if err != nil {
		return fmt.Errorf("save content settings: %w", err)
	}
	return nil
}

// UserContent читает персональные настройки видимости пользователя. Нет
// строки → дефолт (ничего лично не скрыто).
func (s *Store) UserContent(ctx context.Context, userID int64) (ContentConfig, error) {
	return scanContent(ctx, s.pool, `SELECT value FROM user_settings WHERE user_id = $1 AND key = $2`, userID, contentKey)
}

// SetUserContent сохраняет персональные настройки видимости (upsert).
func (s *Store) SetUserContent(ctx context.Context, userID int64, cfg ContentConfig) error {
	cfg.normalize()
	raw, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode user content settings: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO user_settings (user_id, key, value, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (user_id, key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
	`, userID, contentKey, raw)
	if err != nil {
		return fmt.Errorf("save user content settings: %w", err)
	}
	return nil
}

func scanContent(ctx context.Context, pool *pgxpool.Pool, query string, args ...any) (ContentConfig, error) {
	cfg := DefaultContentConfig()
	var raw []byte
	err := pool.QueryRow(ctx, query, args...).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read content settings: %w", err)
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return DefaultContentConfig(), fmt.Errorf("decode content settings: %w", err)
	}
	cfg.normalize()
	return cfg, nil
}

// ContentResolver — горячий доступ к настройкам видимости.
//
// Глобальный (admin) конфиг кэшируется в памяти (atomic): он читается на
// каждый запрос детальной книги/обложки/скачивания (hard-block), и держать
// его в БД-чтении на горячем пути дорого. Кэш обновляется на старте (Load)
// и при сохранении из админки (SetAdmin) — живо, без рестарта.
//
// Персональный конфиг пользователя НЕ кэшируется (per-user, читается по
// одному PK-запросу только на discovery-путях: список/поиск/фасеты).
type ContentResolver struct {
	store *Store
	admin atomic.Pointer[ContentConfig]
}

func NewContentResolver(store *Store) *ContentResolver {
	r := &ContentResolver{store: store}
	def := DefaultContentConfig()
	r.admin.Store(&def)
	return r
}

// Load загружает глобальный конфиг из БД в кэш. Вызывается на старте; при
// ошибке кэш остаётся дефолтным.
func (r *ContentResolver) Load(ctx context.Context) error {
	cfg, err := r.store.Content(ctx)
	if err != nil {
		return err
	}
	r.admin.Store(&cfg)
	return nil
}

// Admin возвращает закэшированный глобальный конфиг.
func (r *ContentResolver) Admin() ContentConfig {
	if p := r.admin.Load(); p != nil {
		return *p
	}
	return DefaultContentConfig()
}

// SetAdmin персистит глобальный конфиг и обновляет кэш.
func (r *ContentResolver) SetAdmin(ctx context.Context, cfg ContentConfig) error {
	cfg.normalize()
	if err := r.store.SetContent(ctx, cfg); err != nil {
		return err
	}
	r.admin.Store(&cfg)
	return nil
}

// User читает персональный конфиг пользователя из БД (без кэша).
func (r *ContentResolver) User(ctx context.Context, userID int64) (ContentConfig, error) {
	return r.store.UserContent(ctx, userID)
}

// SetUser персистит персональный конфиг пользователя.
func (r *ContentResolver) SetUser(ctx context.Context, userID int64, cfg ContentConfig) error {
	return r.store.SetUserContent(ctx, userID, cfg)
}

// Exclusions — объединение скрытых жанров/языков (admin ∪ user) для
// discovery (список/поиск/фасеты/панель фильтров). userID == 0 → только
// admin. Ошибка чтения персонального конфига не фатальна: admin-исключения
// всё равно применяются (деградируем мягко).
func (r *ContentResolver) Exclusions(ctx context.Context, userID int64) (genres, langs []string) {
	admin := r.Admin()
	genres = admin.HiddenGenres
	langs = admin.HiddenLanguages
	if userID > 0 {
		if u, err := r.store.UserContent(ctx, userID); err == nil {
			genres = unionCodes(genres, u.HiddenGenres)
			langs = unionCodes(langs, u.HiddenLanguages)
		}
	}
	return genres, langs
}

// AdminHides — hard-block проверка: скрыт ли контент книги ГЛОБАЛЬНО
// (только admin-настройки; персональные сюда не входят — они лишь убирают
// книгу из выдачи, но не блокируют прямой доступ).
func (r *ContentResolver) AdminHides(genres []string, lang string) bool {
	return r.Admin().Hides(genres, lang)
}
