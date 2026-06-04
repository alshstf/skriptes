package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/skriptes/skriptes/backend/internal/catalog"
	"github.com/skriptes/skriptes/backend/internal/metadata"
	"github.com/skriptes/skriptes/backend/internal/settings"
)

// CatalogDeps — зависимости /api/authors/:id, /api/series/:id, /api/genres.
type CatalogDeps struct {
	Service *catalog.Service
}

// handleListGenres — GET /api/genres. Возвращает плоский список всех
// fb2-жанров с локализованным display-именем и info о parent-категории
// (через LEFT JOIN на pseudo-родителей `cat:*`). Фронт использует это
// чтобы построить tri-state grouped фильтр в FiltersSidebar.
//
// Pseudo-родители (`fb2_code LIKE 'cat:%'`) исключены из ответа на
// уровне SQL — они нужны только как FK target, не как самостоятельные
// жанры. Сами book_genres на них не ссылаются, фильтр по ним пустой.
//
// Сортировка по display — стабильная (по алфавиту RU-имён). Кэширование
// фронтом на 5 минут: каталог жанров меняется только когда добавляется
// новый INPX с неизвестным кодом, что редко.
func handleListGenres(d CatalogDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		items, err := d.Service.ListGenres(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// handleListLanguages — GET /api/languages. Полный список языков коллекции
// (код + display-имя + число книг), отсортированный по популярности.
//
// Список НЕ фильтруется по скрытым языкам: его потребляют разделы «Контент»
// в админке/профиле, где скрытые языки как раз надо показать (чтобы их
// можно было включить обратно). Панель фильтров прячет скрытые на клиенте
// через /api/content/effective.
func handleListLanguages(d CatalogDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		items, err := d.Service.ListLanguages(ctx)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

// authorResponse / seriesResponse — обёртки над catalog-DTO с
// user-specific is_favorite. Как и bookResponse, держим в api-слое
// чтобы не тащить user-концепт в catalog.
type authorResponse struct {
	catalog.Author
	IsFavorite bool `json:"is_favorite"`
}

type seriesResponse struct {
	catalog.Series
	IsFavorite bool `json:"is_favorite"`
}

func handleGetAuthor(d CatalogDeps, hist HistoryDeps, meta MetadataDeps, content ContentDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// userID нужен сервису для ReadCount; и параллельно для is_favorite.
		var userID int64
		if u, ok := UserFromContext(r.Context()); ok {
			userID = u.ID
		}

		// Видимость контента: на карточке автора не показываем книги со скрытыми
		// жанрами/языками (admin ∪ персональные), как и в /books.
		var exGenres, exLangs []string
		if content.Resolver != nil {
			exGenres, exLangs = content.Resolver.Exclusions(ctx, userID)
		}

		a, err := d.Service.GetAuthor(ctx, id, userID, exGenres, exLangs)
		if err != nil {
			if errors.Is(err, catalog.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "author not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		var isFav bool
		if userID > 0 && hist.Service != nil {
			if v, err := hist.Service.IsFavoriteAuthor(ctx, userID, id); err == nil {
				isFav = v
			}
		}

		// Lazy enrichment: bio/фото подтягиваем из Wikipedia. На первом
		// запросе клиент получит карточку без них; polling в useAuthor
		// подменит без перезагрузки.
		triggerAuthorEnrichmentAsync(meta, a)

		writeJSON(w, http.StatusOK, authorResponse{Author: a, IsFavorite: isFav})
	}
}

// authorEnrichWanted решает, нужно ли инициировать ленивое обогащение био/фото
// автора. Чистая функция (вся логика гейта в одном месте):
//   - тип «Био+фото» выключен в админке («Выкл») → нет;
//   - попытка уже была (EnrichmentFetched / metadata_fetched_at) → нет
//     (single-shot, как у экранизаций; иначе долбили бы Wikipedia/OL на каждый
//     GET у авторов без биографии);
//   - оба поля уже на месте → нет;
//   - иначе да (не хватает фото или био).
func authorEnrichWanted(g settings.EnrichmentGates, a catalog.Author) bool {
	if g.AuthorDisabled || a.EnrichmentFetched {
		return false
	}
	return a.PhotoPath == "" || a.Bio == ""
}

// triggerAuthorEnrichmentAsync — параллельно EnsureAuthorPhoto/Bio в
// отдельных goroutines. Каждый сам выходит мгновенно, если поле уже на
// месте. Контекст собственный с EnrichDeadline (HTTP может вернуться
// клиенту раньше).
func triggerAuthorEnrichmentAsync(d MetadataDeps, a catalog.Author) {
	if d.Service == nil {
		return
	}
	// Гейт «Выкл» + single-shot + «всё уже на месте» — вся логика в чистом
	// authorEnrichWanted (Gates() nil-safe). Если она говорит «не нужно» —
	// не дёргаем внешние API на каждый GET/поллинг карточки автора.
	if !authorEnrichWanted(d.Gates.Gates(), a) {
		return
	}
	q := metadata.AuthorQuery{
		ID:         a.ID,
		LastName:   a.LastName,
		FirstName:  a.FirstName,
		MiddleName: a.MiddleName,
		FullName:   a.FullName,
		// Lang заполнить из книг автора пришлось бы дополнительным
		// запросом; пока оставим пустой — WikipediaProvider попробует
		// ru-first, потом en, что покрывает наш каталог.
	}
	if a.PhotoPath == "" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), metadata.EnrichDeadline)
			defer cancel()
			d.Service.EnsureAuthorPhoto(ctx, q)
		}()
	}
	if a.Bio == "" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), metadata.EnrichDeadline)
			defer cancel()
			d.Service.EnsureAuthorBio(ctx, q)
		}()
	}
}

func handleGetSeries(d CatalogDeps, hist HistoryDeps, content ContentDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		var userID int64
		if u, ok := UserFromContext(r.Context()); ok {
			userID = u.ID
		}

		// Видимость контента: книги серии со скрытыми жанрами/языками не показываем.
		var exGenres, exLangs []string
		if content.Resolver != nil {
			exGenres, exLangs = content.Resolver.Exclusions(ctx, userID)
		}

		s, err := d.Service.GetSeries(ctx, id, userID, exGenres, exLangs)
		if err != nil {
			if errors.Is(err, catalog.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "series not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		var isFav bool
		if userID > 0 && hist.Service != nil {
			if v, err := hist.Service.IsFavoriteSeries(ctx, userID, id); err == nil {
				isFav = v
			}
		}
		writeJSON(w, http.StatusOK, seriesResponse{Series: s, IsFavorite: isFav})
	}
}
