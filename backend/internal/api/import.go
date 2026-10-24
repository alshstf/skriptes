package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/recsys"
	"github.com/skriptes/skriptes/backend/internal/recsys/histimport"
)

// Импорт истории чтения из файла (план recsys/04, 4.4). Двухшаговый и без
// хранения файла: preview — разбор и сопоставление, apply — тот же файл ещё раз
// и выбор пользователя (сервер разбирает заново — данным клиента не верим).
// Файл читается потоком из multipart в память (не во временный файл) и после
// ответа не остаётся нигде.
//
//	POST   /api/me/import/preview            multipart: file, source? → preview
//	POST   /api/me/import/apply              multipart: file, source, choices (JSON [{i, work_id}]) → Report
//	GET    /api/me/import/batches            пакеты пользователя
//	DELETE /api/me/import/batches/{id}       откат пакета
//	GET    /api/me/import/template.csv       универсальный шаблон

// ImportDeps — зависимости импорта; Pool == nil → ручки не монтируются.
type ImportDeps struct {
	Pool *pgxpool.Pool
	// Search — запасной поиск по названию (books.Service); nil — без него.
	Search histimport.TitleSearcher
	// MaxBytes — предел файла (SKRIPTES_IMPORT_MAX_MB).
	MaxBytes int64
}

// maxImportItems — книг в одном файле (больше — разбить файл): сопоставление
// идёт в пределах таймаута запроса.
const maxImportItems = 20000

type importForm struct {
	file     []byte
	fileName string
	source   histimport.Source
	choices  string
}

var errImportTooLarge = errors.New("too large")

// readImportForm — поля формы потоком; файл — в память с пределом.
func readImportForm(w http.ResponseWriter, r *http.Request, maxBytes int64) (importForm, error) {
	var f importForm
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+1<<20) // + поля формы
	mr, err := r.MultipartReader()
	if err != nil {
		return f, err
	}
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				return f, errImportTooLarge
			}
			return f, err
		}
		switch p.FormName() {
		case "file":
			f.fileName = p.FileName()
			var buf bytes.Buffer
			n, err := io.Copy(&buf, io.LimitReader(p, maxBytes+1))
			if err != nil {
				var mbe *http.MaxBytesError
				if errors.As(err, &mbe) {
					return f, errImportTooLarge
				}
				return f, err
			}
			if n > maxBytes {
				return f, errImportTooLarge
			}
			f.file = buf.Bytes()
		case "source":
			v, _ := io.ReadAll(io.LimitReader(p, 64))
			f.source = histimport.Source(strings.TrimSpace(string(v)))
		case "choices":
			v, err := io.ReadAll(io.LimitReader(p, 4<<20))
			if err != nil {
				return f, err
			}
			f.choices = string(v)
		}
		_ = p.Close()
	}
	switch f.source {
	case "", histimport.SourceKindle, histimport.SourceCalibre, histimport.SourceGoodreads, histimport.SourceCSV:
	default:
		return f, errors.New("unknown source")
	}
	if len(f.file) == 0 {
		return f, errors.New("no file")
	}
	return f, nil
}

// parseImport — разбор файла формы; ошибка уже отвечена клиенту (ok=false).
func parseImport(w http.ResponseWriter, r *http.Request, d ImportDeps) (importForm, *histimport.Result, bool) {
	f, err := readImportForm(w, r, d.MaxBytes)
	if errors.Is(err, errImportTooLarge) {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
			"error": "файл больше " + strconv.FormatInt(d.MaxBytes>>20, 10) + " МБ"})
		return f, nil, false
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "нужен файл (поле file)"})
		return f, nil, false
	}
	res, err := histimport.Parse(bytes.NewReader(f.file), f.source)
	f.file = nil
	if errors.Is(err, histimport.ErrUnknownFormat) || errors.Is(err, histimport.ErrNoBooks) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return f, nil, false
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "не удалось прочитать файл"})
		return f, nil, false
	}
	if len(res.Items) > maxImportItems {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{
			"error": "в файле больше " + strconv.Itoa(maxImportItems) + " книг — разбейте его на части"})
		return f, nil, false
	}
	return f, res, true
}

type importPreviewItem struct {
	Index      int              `json:"i"`
	Title      string           `json:"title"`
	Authors    []string         `json:"authors"`
	Rating     int              `json:"rating,omitempty"`
	Read       bool             `json:"read"`
	ReadAt     *time.Time       `json:"read_at,omitempty"`
	Shelf      histimport.Shelf `json:"shelf,omitempty"`
	Highlights int              `json:"highlights"`
	Line       int              `json:"line"`
	Match      histimport.Match `json:"match"`
}

// handleImportPreview — POST /api/me/import/preview.
func handleImportPreview(d ImportDeps, content ContentDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		f, res, ok := parseImport(w, r, d)
		if !ok {
			return
		}
		var ex recsys.Exclusions
		if content.Resolver != nil {
			ex.Genres, ex.Langs, ex.HideCompilations = content.Resolver.Exclusions(r.Context(), u.ID)
		}
		m := &histimport.Matcher{DB: d.Pool, Search: d.Search}
		matches, err := m.Match(r.Context(), res.Items, ex)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "не удалось сопоставить с библиотекой"})
			return
		}
		items := make([]importPreviewItem, len(res.Items))
		summary := map[histimport.Confidence]int{histimport.ConfidenceHigh: 0, histimport.ConfidenceMedium: 0, histimport.ConfidenceNone: 0}
		for i, it := range res.Items {
			authors := it.Authors
			if authors == nil {
				authors = []string{}
			}
			items[i] = importPreviewItem{
				Index: i, Title: it.Title, Authors: authors, Rating: it.Rating, Read: it.Read(), ReadAt: it.ReadAt,
				Shelf: it.Shelf, Highlights: len(it.Highlights), Line: it.Line, Match: matches[i],
			}
			summary[matches[i].Confidence]++
		}
		skipped := res.Skipped
		if skipped == nil {
			skipped = []histimport.Skip{}
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"source": res.Source, "file_name": f.fileName, "items": items, "summary": summary,
			"skipped": skipped, "skipped_total": res.SkippedTotal,
		})
	}
}

// handleImportApply — POST /api/me/import/apply.
func handleImportApply(d ImportDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		f, res, ok := parseImport(w, r, d)
		if !ok {
			return
		}
		var choices []histimport.Choice
		if err := json.Unmarshal([]byte(f.choices), &choices); err != nil || len(choices) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "нечего применять: выберите книги"})
			return
		}
		rep, err := histimport.Apply(r.Context(), d.Pool, u.ID, res, f.fileName, choices)
		if errors.Is(err, histimport.ErrBadChoice) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "не удалось применить импорт"})
			return
		}
		writeJSON(w, http.StatusOK, rep)
	}
}

// handleImportBatches — GET /api/me/import/batches.
func handleImportBatches(d ImportDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		out, err := histimport.Batches(r.Context(), d.Pool, u.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": out})
	}
}

// handleImportUndo — DELETE /api/me/import/batches/{id}.
func handleImportUndo(d ImportDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		err = histimport.Undo(r.Context(), d.Pool, u.ID, id)
		if errors.Is(err, histimport.ErrBatchNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleImportTemplate — GET /api/me/import/template.csv: шаблон с BOM, чтобы
// Excel открыл кириллицу.
func handleImportTemplate() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="skriptes-import-template.csv"`)
		_, _ = io.WriteString(w, "\ufeff"+histimport.CSVTemplate)
	}
}
