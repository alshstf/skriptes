package api

import (
	"context"
	"encoding/json"
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
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "split failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]int64{"work_id": newID})
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
