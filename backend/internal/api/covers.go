package api

import (
	"context"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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
		// Эндпоинт отдаёт три класса картинок (обложки книг / постеры
		// экранизаций / фото авторов) — они в разных бакетах. ResolveCachedFile
		// ищет файл по всем, touch'ит нужный LRU и сам защищает от traversal.
		full, ok := d.Service.ResolveCachedFile(name)
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=2592000, immutable") // 30 дней
		// gosec G304/G703 ложно-позитивны: name прошёл выше проверку на
		// "/", "\\", ".." и ResolveCachedFile сделал filepath.Clean+prefix-check.
		http.ServeFile(w, r, full) //nolint:gosec // path traversal guarded above
	}
}

// handleCoverByID — GET /api/covers/book/{id}. On-demand обложка книги:
// отдаёт из кэша если есть, иначе извлекает из fb2 на лету (под
// семафором). Так список книг показывает обложки без фонового прогрева и
// без неограниченного роста кэша. 404 → фронт рисует плейсхолдер.
func handleCoverByID(d MetadataDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Service == nil {
			http.NotFound(w, r)
			return
		}
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			http.NotFound(w, r)
			return
		}
		// Извлечение с сетевого диска может быть небыстрым — даём запас.
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		full, ok := d.Service.ServeCoverByID(ctx, id, d.BooksRoot)
		if !ok {
			http.NotFound(w, r)
			return
		}
		// Короче, чем content-addressable {name}: URL стабилен (по id), а
		// содержимое может смениться при позднем re-enrich (OL/GB).
		w.Header().Set("Cache-Control", "public, max-age=86400")
		http.ServeFile(w, r, full) //nolint:gosec // путь построен сервисом из cache.Path, не из user input
	}
}
