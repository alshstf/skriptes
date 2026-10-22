package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/skriptes/skriptes/backend/internal/recsys"
	"github.com/skriptes/skriptes/backend/internal/settings"
)

// Лента рекомендаций (план recsys/01, задача 1.3): один контракт для всех
// секций Главной — GET /api/me/recommendations?section=<kind>&limit=N →
// recsys.Section {section, items: [recsys.Item{…, reasons}]}. Секции —
// закрытый список recsys.SectionKind; секции правил фазы 2 (recsys.Rules)
// монтируются из RecsDeps.Feed, фронт собирает порядок сам (pages/HomePage.tsx).

// sectionOpts — параметры секции из query: minutes — «у меня есть N минут»
// (fits_tonight; 0 — по умолчанию); mood — настроение сессии (recsys/03, 3.4;
// секции правил и «На вечер», "" — нет; неизвестное — 400).
type sectionOpts struct {
	Minutes int
	Mood    recsys.Mood
}

// handleRecommendations — GET /api/me/recommendations?section=&limit=[&minutes=][&mood=]
func handleRecommendations(hist HistoryDeps, set SettingsDeps, content ContentDeps, recs RecsDeps) http.HandlerFunc {
	type provider func(ctx context.Context, userID int64, limit int, opts sectionOpts) ([]recsys.Item, error)
	plain := func(f func(ctx context.Context, userID int64, limit int) ([]recsys.Item, error)) provider {
		return func(ctx context.Context, userID int64, limit int, _ sectionOpts) ([]recsys.Item, error) {
			return f(ctx, userID, limit)
		}
	}
	exclusions := func(ctx context.Context, userID int64) recsys.Exclusions {
		var ex recsys.Exclusions
		if content.Resolver != nil {
			ex.Genres, ex.Langs, ex.HideCompilations = content.Resolver.Exclusions(ctx, userID)
		}
		return ex
	}
	providers := map[recsys.SectionKind]provider{
		recsys.SectionContinueReading:  plain(hist.Service.ContinueReading),
		recsys.SectionSubscriptions:    plain(hist.Service.SubscriptionFeed),
		recsys.SectionSearchedAppeared: plain(hist.Service.SearchedAppeared),
		// «Оцените прочитанное»: если запросы оценки выключены в профиле —
		// секция пуста (фронт её не рендерит).
		recsys.SectionRatePrompts: func(ctx context.Context, userID int64, limit int, _ sectionOpts) ([]recsys.Item, error) {
			cfg, err := set.Store.UserRatingPromptConfig(ctx, userID)
			if err != nil {
				return nil, err
			}
			if !cfg.Enabled {
				return []recsys.Item{}, nil
			}
			return hist.Service.RateableWorks(ctx, userID, cfg.DelayDays, limit)
		},
	}
	// Секции правил (recsys/02): кандидаты правила → карточки с учётом
	// скрытого у пользователя (жанры/языки/сборники — те же исключения, что
	// в каталоге, грабля 14).
	if recs.Feed != nil {
		for _, rule := range recsys.Rules {
			kind := rule.Section()
			providers[kind] = func(ctx context.Context, userID int64, limit int, opts sectionOpts) ([]recsys.Item, error) {
				return recs.Feed.Section(ctx, kind, userID, exclusions(ctx, userID), limit, opts.Mood)
			}
		}
		// «На вечер» (2.6): минуты из query, скорость — личная из журнала позиций.
		providers[recsys.SectionFitsTonight] = func(ctx context.Context, userID int64, limit int, opts sectionOpts) ([]recsys.Item, error) {
			speed, err := hist.Service.ReadingSpeed(ctx, userID)
			if err != nil {
				return nil, err
			}
			return recs.Feed.Tonight(ctx, userID, opts.Minutes, speed, exclusions(ctx, userID), limit, opts.Mood)
		}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		kind := recsys.SectionKind(r.URL.Query().Get("section"))
		p, ok := providers[kind]
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown section"})
			return
		}
		limit := parseIntOr(r.URL.Query().Get("limit"), 12)
		mood := recsys.Mood(r.URL.Query().Get("mood"))
		if mood != "" && !recsys.IsMood(string(mood)) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown mood"})
			return
		}
		opts := sectionOpts{Minutes: parseIntOr(r.URL.Query().Get("minutes"), 0), Mood: mood}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		items, err := p(ctx, u.ID, limit, opts)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, recsys.Section{Kind: kind, Items: items})
	}
}

// handleRelatedRecommendations — GET /api/me/recommendations/related?work_id=&limit=:
// «раз понравилось — вот следующая» (recsys/02, 2.8): до limit (по умолчанию 3)
// карточек правил, основание которых — работа work_id. Без Feed — пусто.
func handleRelatedRecommendations(content ContentDeps, recs RecsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		workID := int64(parseIntOr(r.URL.Query().Get("work_id"), 0))
		if workID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid work_id"})
			return
		}
		limit := parseIntOr(r.URL.Query().Get("limit"), 3)
		items := []recsys.Item{}
		if recs.Feed != nil {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			defer cancel()
			var ex recsys.Exclusions
			if content.Resolver != nil {
				ex.Genres, ex.Langs, ex.HideCompilations = content.Resolver.Exclusions(ctx, u.ID)
			}
			var err error
			if items, err = recs.Feed.Related(ctx, u.ID, workID, ex, limit); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"work_id": workID, "items": items})
	}
}

// handleDismissFeedItem — POST /api/me/feed/dismiss {work_id, reason?}: скрыть
// работу из ленты рекомендаций. reason — not_interested (по умолчанию) |
// already_read | later; неизвестная — 400.
func handleDismissFeedItem(d HistoryDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		var body struct {
			WorkID int64  `json:"work_id"`
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.WorkID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid work_id"})
			return
		}
		reason, ok := recsys.ParseDismissReason(body.Reason)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid reason"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if err := d.Service.DismissFeedItem(ctx, u.ID, body.WorkID, reason); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "dismiss failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"dismissed": true})
	}
}

// handleGetMeHomeFeed — GET /api/me/home-feed: настройки ленты Главной
// (свёрнутые секции, recsys/02 2.9).
func handleGetMeHomeFeed(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		cfg, err := d.Store.UserHomeFeed(ctx, u.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read settings failed"})
			return
		}
		writeJSON(w, http.StatusOK, cfg)
	}
}

// handleUpdateMeHomeFeed — PUT /api/me/home-feed {collapsed: [kind…]}.
// Неизвестная секция — 400 (список секций — recsys.AllSections).
func handleUpdateMeHomeFeed(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		var cfg settings.HomeFeedConfig
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&cfg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		for _, s := range cfg.Collapsed {
			if !recsys.IsSectionKind(s) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown section"})
				return
			}
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := d.Store.SetUserHomeFeed(ctx, u.ID, cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
			return
		}
		saved, err := d.Store.UserHomeFeed(ctx, u.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read settings failed"})
			return
		}
		writeJSON(w, http.StatusOK, saved)
	}
}
