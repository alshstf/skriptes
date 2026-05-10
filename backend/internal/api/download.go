package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/converter"
)

// DownloadDeps — зависимости /api/books/{id}/download.
type DownloadDeps struct {
	Books     *books.Service
	Converter *converter.Converter
}

func handleDownload(d DownloadDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		format, err := converter.ParseFormat(r.URL.Query().Get("format"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()

		book, err := d.Books.Get(ctx, id)
		if err != nil {
			if errors.Is(err, books.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "book not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}

		src := converter.SourceBook{
			ID:       book.ID,
			Archive:  book.Archive,
			FileName: book.FileName,
			Ext:      book.Ext,
		}
		res, err := d.Converter.Convert(ctx, src, format)
		if err != nil {
			if errors.Is(err, converter.ErrSourceMissing) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "archive file not available"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("convert failed: %v", err)})
			return
		}

		// Имя файла: <author> - <title>.<ext>; пробелы валидны в RFC 5987 utf-8 form.
		filename := buildFilename(book, res.Filename)
		w.Header().Set("Content-Type", res.ContentType)
		w.Header().Set("Content-Disposition", contentDisposition(filename))
		w.Header().Set("Cache-Control", "private, max-age=3600")

		if format == converter.FormatFB2 {
			rc, size, err := converter.ExtractFB2(res.Path, src.FileName+"."+src.Ext)
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "extract failed"})
				return
			}
			defer func() { _ = rc.Close() }()
			if size > 0 {
				w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
			}
			_, _ = io.Copy(w, rc)
			return
		}

		f, err := os.Open(res.Path) // #nosec G304 — путь из cacheRoot, который мы сами строим
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "open cache failed"})
			return
		}
		defer func() { _ = f.Close() }()
		st, err := f.Stat()
		if err == nil {
			w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
		}
		_, _ = io.Copy(w, f)
	}
}

// buildFilename собирает дружелюбное имя файла "Author - Title.ext".
// Если автора или title нет — fallback к имени из converter.Result.
func buildFilename(b books.Book, fallback string) string {
	if b.Title == "" {
		return fallback
	}
	author := ""
	if len(b.Authors) > 0 {
		author = b.Authors[0].FullName
	}
	// Тот же ext что и в fallback (после последней точки).
	ext := ""
	for i := len(fallback) - 1; i >= 0; i-- {
		if fallback[i] == '.' {
			ext = fallback[i:]
			break
		}
	}
	name := b.Title
	if author != "" {
		name = author + " - " + b.Title
	}
	return sanitizeFilename(name) + ext
}

// sanitizeFilename убирает символы, проблемные в именах файлов
// на разных ОС, оставляя при этом utf-8 (кириллицу).
func sanitizeFilename(s string) string {
	const bad = `/\:*?"<>|`
	out := make([]byte, 0, len(s))
	for _, r := range s {
		if r < 32 {
			continue
		}
		if r > 127 {
			out = append(out, []byte(string(r))...)
			continue
		}
		isBad := false
		for _, b := range []byte(bad) {
			if byte(r) == b {
				isBad = true
				break
			}
		}
		if isBad {
			out = append(out, '_')
		} else {
			out = append(out, byte(r))
		}
	}
	return string(out)
}

// contentDisposition строит Content-Disposition с RFC 5987 (filename*) для
// корректной поддержки utf-8 в имени файла. Заодно даёт ASCII-fallback
// для древних клиентов.
func contentDisposition(filename string) string {
	encoded := url.PathEscape(filename)
	asciiSafe := make([]byte, 0, len(filename))
	for _, r := range filename {
		if r < 128 && r >= 32 && r != '"' && r != '\\' {
			asciiSafe = append(asciiSafe, byte(r))
		} else {
			asciiSafe = append(asciiSafe, '_')
		}
	}
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, string(asciiSafe), encoded)
}
