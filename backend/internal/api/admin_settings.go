package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/skriptes/skriptes/backend/internal/metadata"
	"github.com/skriptes/skriptes/backend/internal/settings"
)

// SettingsDeps — зависимости admin-настроек. Store персистит конфиг в
// app_settings; Metadata (enricher) применяет лимиты кэша в рантайме и
// отдаёт статистику/очистку; Prewarm — контроллер фоновой джобы прогрева
// (вкл/выкл по тумблеру + разовый прогон).
type SettingsDeps struct {
	Store              *settings.Store
	Metadata           *metadata.Enricher
	Prewarm            *metadata.PrewarmController
	YearBackfill       *metadata.YearBackfillController
	CoverBackfill      *metadata.CoverBackfillController
	AuthorBackfill     *metadata.AuthorBackfillController
	AdaptationBackfill *metadata.AdaptationBackfillController
	WorkGroup          *metadata.WorkGroupController
}

// coverSettingsResponse — текущая конфигурация + статистика кэша +
// состояние прогрева (running/mode — для кнопок «Прогреть/Остановить»).
type coverSettingsResponse struct {
	settings.CoverConfig
	metadata.PrewarmStatus
	CacheSizeBytes       int64 `json:"cache_size_bytes"`
	PosterCacheSizeBytes int64 `json:"poster_cache_size_bytes"`
	PhotoCacheSizeBytes  int64 `json:"photo_cache_size_bytes"`
	FreeBytes            int64 `json:"free_bytes"`
}

func coverStats(d SettingsDeps, cfg settings.CoverConfig) coverSettingsResponse {
	resp := coverSettingsResponse{CoverConfig: cfg, FreeBytes: -1}
	if d.Metadata != nil {
		resp.CacheSizeBytes = d.Metadata.CoverCacheSize()
		resp.PosterCacheSizeBytes = d.Metadata.PosterCacheSize()
		resp.PhotoCacheSizeBytes = d.Metadata.PhotoCacheSize()
		resp.FreeBytes = d.Metadata.CoverCacheFree()
	}
	if d.Prewarm != nil {
		resp.PrewarmStatus = d.Prewarm.Status()
	}
	return resp
}

// handleGetCoverSettings — GET /api/admin/cover-cache. Конфиг + статистика.
func handleGetCoverSettings(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		cfg, err := d.Store.Cover(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read settings failed"})
			return
		}
		writeJSON(w, http.StatusOK, coverStats(d, cfg))
	}
}

// handleUpdateCoverSettings — PUT /api/admin/cover-cache. Сохраняет конфиг,
// применяет лимиты кэша и запускает/останавливает фоновую джобу прогрева
// в рантайме — всё без рестарта.
func handleUpdateCoverSettings(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var cfg settings.CoverConfig
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&cfg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		if cfg.CacheMaxMB < 0 || cfg.CacheMinFreeMB < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "values must be non-negative"})
			return
		}
		// Нормализуем интенсивность к известному пресету.
		switch cfg.Intensity {
		case settings.IntensityLow, settings.IntensityMedium, settings.IntensityHigh:
		default:
			cfg.Intensity = settings.IntensityMedium
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := d.Store.SetCover(ctx, cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save settings failed"})
			return
		}
		if d.Metadata != nil {
			// Прогрев ON → лимит 0 (full-store, без эвикции); иначе бюджет.
			d.Metadata.SetCoverLimits(cfg.EffectiveCacheMaxBytes(), cfg.MinFreeBytes())
			// Постеры/фото — свои бюджеты (общий пол свободного места).
			d.Metadata.SetPosterLimits(cfg.PosterCacheMaxBytes(), cfg.MinFreeBytes())
			d.Metadata.SetPhotoLimits(cfg.PhotoCacheMaxBytes(), cfg.MinFreeBytes())
		}
		// Живое применение под-настроек (тумблеры/интенсивность) + мастер вкл/выкл.
		if d.Prewarm != nil {
			d.Prewarm.SetConfig(metadata.PrewarmConfig{
				Covers:      cfg.SyncCovers,
				Annotations: cfg.SyncAnnotations,
				Years:       cfg.SyncYears,
				Workers:     cfg.IntensityWorkers(),
				Delay:       cfg.IntensityDelay(),
			})
			d.Prewarm.SetEnabled(cfg.Prewarm)
		}
		writeJSON(w, http.StatusOK, coverStats(d, cfg))
	}
}

// handlePrewarmNow — POST /api/admin/cover-cache/prewarm. Разовый прогон
// прогрева (кнопка «Прогреть сейчас»): извлечь обложки для книг, у
// которых их ещё нет. Запускается в фоне, отвечает сразу.
func handlePrewarmNow(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Prewarm == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "metadata disabled"})
			return
		}
		d.Prewarm.RunOnce()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
	}
}

// handlePrewarmStop — POST /api/admin/cover-cache/prewarm/stop. Отменяет
// идущий разовый прогон (между батчами).
func handlePrewarmStop(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Prewarm == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "metadata disabled"})
			return
		}
		d.Prewarm.StopOnce()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "stopping"})
	}
}

// handleClearCoverCache — POST /api/admin/cover-cache/clear. Удаляет все
// файлы кэша обложек (мгновенно освобождает место). cover_path становятся
// «висячими» — on-demand отдача само-восстановит при следующем запросе.
func handleClearCoverCache(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Metadata == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "metadata disabled"})
			return
		}
		removed, err := d.Metadata.ClearCoverCache()
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "clear failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"removed": removed})
	}
}

// handleClearPosterCache — POST /api/admin/cover-cache/clear-posters. Удаляет
// файлы постеров экранизаций + зануляет висячие poster_path. В отличие от
// обложек книг постеры не воссоздаются из fb2 (внешний источник) — вернутся
// при следующем дозаполнении экранизаций.
func handleClearPosterCache(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Metadata == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "metadata disabled"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		removed, err := d.Metadata.ClearPosterCache(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "clear failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"removed": removed})
	}
}

// handleClearPhotoCache — POST /api/admin/cover-cache/clear-photos. Удаляет
// файлы фото авторов + зануляет висячие photo_path.
func handleClearPhotoCache(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Metadata == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "metadata disabled"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		removed, err := d.Metadata.ClearPhotoCache(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "clear failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int{"removed": removed})
	}
}
