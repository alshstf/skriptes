package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/skriptes/skriptes/backend/internal/metadata"
	"github.com/skriptes/skriptes/backend/internal/settings"
)

// workGroupingResponse — конфиг группировки + состояние воркера + покрытие.
type workGroupingResponse struct {
	settings.WorkGroupingConfig
	metadata.WorkGroupStatus
	Coverage metadata.WorkGroupCoverage `json:"coverage"`
}

func toWorkGroupConfig(c settings.WorkGroupingConfig) metadata.WorkGroupConfig {
	return metadata.WorkGroupConfig{
		OpenLibrary:       c.OpenLibrary,
		Wikidata:          c.Wikidata,
		WholeCollection:   c.WholeCollection,
		OpenLibraryRPM:    c.OpenLibraryRPM,
		WikidataRPM:       c.WikidataRPM,
		NotFoundRetryDays: c.NotFoundRetryDays,
		ErrorRetryHours:   c.ErrorRetryHours,
	}
}

func workGroupingState(ctx context.Context, d SettingsDeps, cfg settings.WorkGroupingConfig) workGroupingResponse {
	resp := workGroupingResponse{WorkGroupingConfig: cfg}
	if d.WorkGroup != nil {
		resp.WorkGroupStatus = d.WorkGroup.Status()
		if cov, err := d.WorkGroup.Coverage(ctx); err == nil {
			resp.Coverage = cov
		}
	}
	return resp
}

// handleGetWorkGrouping — GET /api/admin/work-grouping.
func handleGetWorkGrouping(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		cfg, err := d.Store.WorkGrouping(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "read settings failed"})
			return
		}
		writeJSON(w, http.StatusOK, workGroupingState(ctx, d, cfg))
	}
}

// handleUpdateWorkGrouping — PUT /api/admin/work-grouping. Сохраняет конфиг и
// применяет в рантайме (SetConfig + SetEnabled) без рестарта.
func handleUpdateWorkGrouping(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var cfg settings.WorkGroupingConfig
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&cfg); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		if cfg.OpenLibraryRPM < 0 || cfg.WikidataRPM < 0 || cfg.NotFoundRetryDays < 0 || cfg.ErrorRetryHours < 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "values must be non-negative"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if err := d.Store.SetWorkGrouping(ctx, cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save settings failed"})
			return
		}
		if d.WorkGroup != nil {
			d.WorkGroup.SetConfig(toWorkGroupConfig(cfg))
			d.WorkGroup.SetEnabled(cfg.Enabled)
		}
		writeJSON(w, http.StatusOK, workGroupingState(ctx, d, cfg))
	}
}

// handleWorkGroupingNow — POST /api/admin/work-grouping/run. Разовый проход.
func handleWorkGroupingNow(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.WorkGroup == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "work grouping disabled"})
			return
		}
		d.WorkGroup.RunOnce()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
	}
}

// handleWorkGroupingStop — POST /api/admin/work-grouping/stop.
func handleWorkGroupingStop(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.WorkGroup == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "work grouping disabled"})
			return
		}
		d.WorkGroup.StopOnce()
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "stopping"})
	}
}

// handleWorkSplit — POST /api/admin/works/split. Ручное разъединение изданий в
// новую работу (починка ложного слияния). Body: {"book_ids":[...]}.
func handleWorkSplit(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.WorkGroup == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "work grouping disabled"})
			return
		}
		var body struct {
			BookIDs []int64 `json:"book_ids"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&body); err != nil || len(body.BookIDs) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "book_ids required"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		newID, err := d.WorkGroup.SplitEditions(ctx, body.BookIDs)
		if err != nil {
			if errors.Is(err, metadata.ErrSplitAnchor) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "нельзя вынести якорное издание"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "split failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int64{"work_id": newID})
	}
}

// handleWorksRegroup — POST /api/admin/works/regroup. Массовый разбор ошибочно
// слитых работ (recovery после Tier-2-без-SrcTitle): не-якорные издания →
// синглтоны, purge found-lookups, сброс ext_ids, синхронный Tier-1 re-group по
// автору. Body: {"work_ids":[...], "dry_run":bool}. dry_run — только прогноз
// (сколько Tier-1-кластеров дадут издания каждой работы), без записей.
func handleWorksRegroup(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.WorkGroup == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "work grouping disabled"})
			return
		}
		var body struct {
			WorkIDs []int64 `json:"work_ids"`
			DryRun  bool    `json:"dry_run"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&body); err != nil || len(body.WorkIDs) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "work_ids required"})
			return
		}
		// Батчи ограничены: recovery идёт порциями (по detection-отчёту), а
		// хендлер держит запрос синхронно.
		if len(body.WorkIDs) > 500 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "too many work_ids (max 500 per call)"})
			return
		}
		timeout := 30 * time.Second
		if !body.DryRun {
			timeout = 5 * time.Minute // split+re-group сотен работ + пересчёты агрегатов
		}
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()
		out, err := d.WorkGroup.RegroupWorks(ctx, body.WorkIDs, body.DryRun)
		if err != nil {
			if errors.Is(err, metadata.ErrRegroupBusy) {
				writeJSON(w, http.StatusConflict, map[string]string{"error": "остановите фоновую группировку перед разбором"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "regroup failed"})
			return
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// handleWorkMerge — POST /api/admin/works/merge. Ручное объединение работ.
// Body: {"work_ids":[...], "target": <optional>}.
func handleWorkMerge(d SettingsDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.WorkGroup == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "work grouping disabled"})
			return
		}
		var body struct {
			WorkIDs []int64 `json:"work_ids"`
			Target  int64   `json:"target"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&body); err != nil || len(body.WorkIDs) < 2 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "work_ids (>=2) required"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		target, err := d.WorkGroup.MergeWorks(ctx, body.WorkIDs, body.Target)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "merge failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int64{"work_id": target})
	}
}
