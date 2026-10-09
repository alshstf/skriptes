package awards

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/skriptes/skriptes/backend/internal/metadata"
)

// Логотипы и статуэтки премий (#446). Решение владельца 2026-10-09: инстанс
// семейный, лицензия картинок не ограничение, но репозиторий и образы публичны —
// в git только адреса, картинку один раз скачивает бэкенд в кэш инстанса
// (<cache>/award-logos, не регенерируемый: источник может пропасть).
//
// Русские жанровые премии — картинка премии на Фантлабе (/images/awards/{id});
// международные — из Википедии (у Фантлаба мелкие), через Special:FilePath с
// шириной: и SVG приходит растровым PNG, своих SVG не отдаём. Где картинки нет
// или она не годится (фото лауреата с мечом у «Меча без имени»), — пусто, фронт
// рисует монограмму.
var logoOverrides = map[string]string{
	"dar":                wikiFile("ru", "Dar-telegram-lt.png"),
	"prosvetitel":        wikiFile("ru", "Prosvetitel_logo.jpg"),
	"bely":               wikiFile("ru", "Bely_Prize.jpg"),
	"sword-without-name": "",
	"nobel":              wikiFile("en", "Nobel_Prize.png"),
	"booker":             wikiFile("en", "Booker_Prize_Logo.svg"),
	"goncourt":           wikiFile("en", "Prix_Goncourt.jpg"),
	"pulitzer":           wikiFile("en", "Pulitzer_Prizes_(medal).png"),
	"nba":                wikiFile("en", "National_Book_Foundation_logo.svg"),
	"hugo":               wikiFile("en", "Hugo_Award_Logo.svg"),
	"nebula":             wikiFile("en", "Nebula_Award_logo.png"),
	"world-fantasy":      wikiFile("en", "World_Fantasy_Award_tree.jpeg"),
	"locus":              wikiFile("en", "Locus_award.png"),
	"clarke":             wikiFile("en", "Clarke_Award.png"),
	"oscar":              wikiFile("en", "Oscar-free-version.svg"),
	"cannes":             wikiFile("en", "Palme d'Or gold silhouette.svg"),
	"golden-globe":       wikiFile("en", "Golden_Globe_Trophy.jpg"),
	"bafta":              wikiFile("en", "BAFTA award.jpg"),
	"emmy":               wikiFile("en", "Emmy_Awards_logo.png"),
}

func wikiFile(lang, file string) string {
	return "https://" + lang + ".wikipedia.org/wiki/Special:FilePath/" + url.PathEscape(file) + "?width=240"
}

// LogoURL — адрес картинки премии у источника ("" — нет картинки).
func LogoURL(a Award) string {
	if u, ok := logoOverrides[a.Key]; ok {
		return u
	}
	if a.FantlabID > 0 {
		return fmt.Sprintf("https://fantlab.ru/images/awards/%d", a.FantlabID)
	}
	return ""
}

// ErrNoLogo — у премии нет картинки (или её не удалось скачать недавно).
var ErrNoLogo = errors.New("award has no logo")

const (
	logoMaxBytes   = 2 << 20
	logoRetryAfter = time.Hour // неудачную загрузку не повторяем чаще
)

// logoUserAgent — Википедия без User-Agent отвечает 403.
const logoUserAgent = "skriptes (https://github.com/alshstf/skriptes; award logos)"

// LogoCache — картинки премий в каталоге инстанса: скачиваются при первом запросе.
type LogoCache struct {
	dir    string
	client *http.Client
	source func(Award) string // LogoURL; тесты подменяют
	mu     sync.Mutex
	failed map[string]time.Time
}

// NewLogoCache — кэш в dir; HTTP-клиент с прерывателем по хосту (грабля №20).
func NewLogoCache(dir string) *LogoCache {
	return &LogoCache{dir: dir, client: metadata.SourceHTTPClient(20 * time.Second), source: LogoURL, failed: map[string]time.Time{}}
}

// Path — файл картинки премии key; при необходимости скачивает её.
func (c *LogoCache) Path(ctx context.Context, key string) (string, error) {
	a, ok := ByKey(key)
	if !ok {
		return "", ErrUnknownAward
	}
	src := c.source(a)
	if src == "" {
		return "", ErrNoLogo
	}
	path := filepath.Join(c.dir, key)
	if st, err := os.Stat(path); err == nil && st.Size() > 0 {
		return path, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if st, err := os.Stat(path); err == nil && st.Size() > 0 {
		return path, nil
	}
	if t, bad := c.failed[key]; bad && time.Since(t) < logoRetryAfter {
		return "", ErrNoLogo
	}
	if err := c.fetch(ctx, src, path); err != nil {
		c.failed[key] = time.Now()
		return "", fmt.Errorf("%w: %w", ErrNoLogo, err)
	}
	delete(c.failed, key)
	return path, nil
}

func (c *LogoCache) fetch(ctx context.Context, src, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", logoUserAgent)
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, logoMaxBytes+1))
	if err != nil {
		return err
	}
	if len(body) > logoMaxBytes {
		return fmt.Errorf("too large")
	}
	switch http.DetectContentType(body) {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		return fmt.Errorf("not a raster image")
	}
	if err := os.MkdirAll(c.dir, 0o750); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
