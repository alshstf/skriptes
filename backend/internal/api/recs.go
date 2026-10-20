package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/skriptes/skriptes/backend/internal/recsys"
)

// Лента рекомендаций (план recsys/01, задача 1.3): один контракт для всех
// секций Главной — GET /api/me/recommendations?section=<kind>&limit=N →
// recsys.Section {section, items: [recsys.Item{…, reasons}]}. Секции —
// закрытый список recsys.SectionKind; новые секции (фаза 2) добавляются
// в sectionProviders, фронт собирает порядок сам (pages/HomePage.tsx).

// handleRecommendations — GET /api/me/recommendations?section=&limit=
func handleRecommendations(hist HistoryDeps, set SettingsDeps) http.HandlerFunc {
	type provider func(ctx context.Context, userID int64, limit int) ([]recsys.Item, error)
	providers := map[recsys.SectionKind]provider{
		recsys.SectionContinueReading: hist.Service.ContinueReading,
		recsys.SectionSubscriptions:   hist.Service.SubscriptionFeed,
		// «Оцените прочитанное»: если запросы оценки выключены в профиле —
		// секция пуста (фронт её не рендерит).
		recsys.SectionRatePrompts: func(ctx context.Context, userID int64, limit int) ([]recsys.Item, error) {
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
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		items, err := p(ctx, u.ID, limit)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, recsys.Section{Kind: kind, Items: items})
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
