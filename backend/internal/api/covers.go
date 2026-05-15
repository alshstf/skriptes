package api

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/metadata"
)

// MetadataDeps — обогащение карточек книг (обложки на этом этапе).
// Service может быть nil — тогда триггер enrichment'а не подключается,
// маршрут /api/covers/* всё равно работает (отдаст уже сохранённые
// файлы из cacheRoot, если они есть).
type MetadataDeps struct {
	Service   *metadata.Enricher
	BooksRoot string // корень read-only volume с zip-архивами; нужен для fb2-провайдера
}

// triggerBookEnrichmentAsync — запускает fire-and-forget goroutine,
// которая обогащает книгу обложкой и/или аннотацией если этих данных
// ещё нет. Каждый Enrich* сам быстро выходит, если данные уже на месте.
//
// Контекст — собственный с таймаутом EnrichDeadline, а не унаследованный
// от HTTP: HTTP-handler вернётся клиенту немедленно, нам нельзя дать
// клиенту отменить фоновое обогащение.
func triggerBookEnrichmentAsync(d MetadataDeps, b books.Book) {
	if d.Service == nil {
		return
	}
	if b.CoverPath != "" && b.Annotation != "" {
		return // ничего обогащать
	}

	authors := make([]string, 0, len(b.Authors))
	for _, a := range b.Authors {
		authors = append(authors, a.FullName)
	}
	q := metadata.BookQuery{
		ID:          b.ID,
		Title:       b.Title,
		Authors:     authors,
		Lang:        b.Lang,
		ArchivePath: filepath.Join(d.BooksRoot, b.Archive),
		FB2Name:     b.FileName + "." + b.Ext,
	}
	if b.CoverPath == "" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), metadata.EnrichDeadline)
			defer cancel()
			d.Service.EnsureCover(ctx, q)
		}()
	}
	if b.Annotation == "" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), metadata.EnrichDeadline)
			defer cancel()
			d.Service.EnsureAnnotation(ctx, q)
		}()
	}
}

// handleCover — GET /api/covers/{name}. Отдаёт файл из coverRoot.
//
// Имя файла строго один токен без слешей (sha256.ext); это защищает
// от path traversal даже если кто-то подсунет ".." в URL. Дополнительно
// проверяем что не выходим из coverRoot после Join.
//
// Cache-Control: long-lived — имя файла content-addressable (sha256),
// так что любое изменение даёт новое имя.
func handleCover(d MetadataDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := chi.URLParam(r, "name")
		if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
			http.NotFound(w, r)
			return
		}
		if d.Service == nil {
			http.NotFound(w, r)
			return
		}
		full := d.Service.CoverFile(name)
		// Защита от symlink/traversal: проверим, что итоговый путь
		// действительно внутри CoverRoot.
		if !strings.HasPrefix(filepath.Clean(full), filepath.Clean(d.Service.CoverRoot())) {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=2592000, immutable") // 30 дней
		// gosec G304/G703 ложно-позитивны: name прошёл выше проверку на
		// "/", "\\", ".." и filepath.Clean+prefix-check на побег из coverRoot.
		http.ServeFile(w, r, full) //nolint:gosec // path traversal guarded above
	}
}
