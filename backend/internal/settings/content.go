package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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
//
// HideCompilations — скрывать сборники/антологии/тома собраний (works.kind ≠
// NULL) из выдачи целиком (opt-in, дефолт false): агрессивная персональная
// настройка поверх базового секционирования на карточке автора. Поле есть и в
// admin-конфиге (объединение admin ∪ user в Exclusions), но admin-UI его пока
// не выставляет — переключатель только в профиле.
type ContentConfig struct {
	HiddenGenres     []string `json:"hidden_genres"`
	HiddenLanguages  []string `json:"hidden_languages"`
	HideCompilations bool     `json:"hide_compilations,omitempty"`
	// LanguageMode — режим языков (только admin, #310): "" — скрыты
	// HiddenLanguages, остальные видны; LanguageModeOnly — видны только
	// ShownLanguages, остальные скрыты, в том числе новые языки следующих INPX
	// (чёрным списком на проде скрывали 52 кода, чтобы видны были ru и en).
	LanguageMode   string   `json:"language_mode,omitempty"`
	ShownLanguages []string `json:"shown_languages,omitempty"`
}

// LanguageModeOnly — «показывать только выбранные языки».
const LanguageModeOnly = "only"

// DefaultContentConfig — ничего не скрыто. Срезы не-nil, чтобы JSON-ответ
// был `[]`, а не `null` (фронту удобнее не проверять на null).
func DefaultContentConfig() ContentConfig {
	return ContentConfig{HiddenGenres: []string{}, HiddenLanguages: []string{}}
}

// normalize приводит срезы к каноничному виду: убирает пустые строки и
// дубли, сортирует (стабильный JSON в БД), гарантирует не-nil. Коды языков не в
// нормализованном виде («ru-RU», «en-GB», «RU») отбрасываются: языки книг
// нормализованы (грабля №14), такие коды ни с чем не совпадают и только
// путают список (прод: «en-GB», «ru-RU» в скрытых, #310). Не приводим их к
// канону — «ru-RU»→«ru» внезапно скрыл бы русский.
func (c *ContentConfig) normalize() {
	c.HiddenGenres = cleanCodes(c.HiddenGenres)
	c.HiddenLanguages = cleanCodes(normalizedLangCodes(c.HiddenLanguages))
	if c.LanguageMode != LanguageModeOnly {
		c.LanguageMode = ""
		c.ShownLanguages = nil
	} else {
		c.ShownLanguages = cleanCodes(normalizedLangCodes(c.ShownLanguages))
	}
}

func normalizedLangCodes(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == strings.ToLower(strings.TrimSpace(s)) && !strings.ContainsAny(s, "-_") {
			out = append(out, s)
		}
	}
	return out
}

// Hides сообщает, скрывает ли этот конфиг книгу с данными жанрами/языком.
// Книга скрыта, если её язык в списке скрытых ИЛИ хотя бы один её жанр
// скрыт (мульти-жанровая книга прячется, если хоть один жанр запрещён —
// этого и ждёшь от «не показывать эротику»).
func (c ContentConfig) Hides(genres []string, lang string) bool {
	if c.LanguageMode == LanguageModeOnly {
		if lang != "" && !slices.Contains(c.ShownLanguages, lang) {
			return true
		}
	} else if lang != "" && slices.Contains(c.HiddenLanguages, lang) {
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

	// Множество языков коллекции — для режима «только выбранные языки»: скрытые =
	// все языки коллекции минус показываемые. Кэш на langUniverseTTL.
	universe   func(context.Context) ([]string, error)
	uniMu      sync.Mutex
	uniCache   []string
	uniFetched time.Time
}

// langUniverseTTL — как долго держать множество языков коллекции (меняется
// только импортом).
const langUniverseTTL = 5 * time.Minute

// SetLanguageUniverse задаёт источник множества языков коллекции (обычно
// catalog.ListLanguages). Без него режим «только выбранные» скрывает в выдаче
// лишь то, что проверяется поштучно (AdminHides).
func (r *ContentResolver) SetLanguageUniverse(f func(context.Context) ([]string, error)) {
	r.universe = f
}

func (r *ContentResolver) languageUniverse(ctx context.Context) []string {
	if r.universe == nil {
		return nil
	}
	r.uniMu.Lock()
	defer r.uniMu.Unlock()
	if r.uniCache != nil && time.Since(r.uniFetched) < langUniverseTTL {
		return r.uniCache
	}
	langs, err := r.universe(ctx)
	if err != nil {
		return r.uniCache // прежнее значение лучше пустого
	}
	r.uniCache, r.uniFetched = langs, time.Now()
	return langs
}

// AdminHiddenLanguages — языки, скрытые глобально: список скрытых или, в режиме
// «только выбранные», все языки коллекции, кроме показываемых.
func (r *ContentResolver) AdminHiddenLanguages(ctx context.Context) []string {
	admin := r.Admin()
	if admin.LanguageMode != LanguageModeOnly {
		return admin.HiddenLanguages
	}
	var hidden []string
	for _, l := range r.languageUniverse(ctx) {
		if !slices.Contains(admin.ShownLanguages, l) {
			hidden = append(hidden, l)
		}
	}
	return cleanCodes(hidden)
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
// Третий результат — скрывать ли сборники (admin ∪ user, см. HideCompilations).
func (r *ContentResolver) Exclusions(ctx context.Context, userID int64) (genres, langs []string, hideCompilations bool) {
	admin := r.Admin()
	genres = admin.HiddenGenres
	langs = r.AdminHiddenLanguages(ctx)
	hideCompilations = admin.HideCompilations
	if userID > 0 {
		if u, err := r.store.UserContent(ctx, userID); err == nil {
			genres = unionCodes(genres, u.HiddenGenres)
			langs = unionCodes(langs, u.HiddenLanguages)
			hideCompilations = hideCompilations || u.HideCompilations
		}
	}
	return genres, langs, hideCompilations
}

// AdminHides — hard-block проверка: скрыт ли контент книги ГЛОБАЛЬНО
// (только admin-настройки; персональные сюда не входят — они лишь убирают
// книгу из выдачи, но не блокируют прямой доступ).
func (r *ContentResolver) AdminHides(genres []string, lang string) bool {
	return r.Admin().Hides(genres, lang)
}
