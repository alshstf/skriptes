package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/catalog"
	"github.com/skriptes/skriptes/backend/internal/history"
)

// SuggestResponse — три параллельные группы для command-palette.
// Семантика "пустая группа = нет совпадений", не nil — фронту удобнее.
type SuggestResponse struct {
	Query   string                  `json:"query"`
	Books   []books.ListItem        `json:"books"`
	Authors []catalog.AuthorSuggest `json:"authors"`
	Series  []catalog.SeriesSuggest `json:"series"`
}

// handleSuggest — GET /api/search/suggest?q=...&limit=5.
//
// Параллельно опрашивает:
//   - Meili books index (через Books.Service.Suggest)
//   - PG authors с trigram-индексом (Catalog.Service.SuggestAuthors)
//   - PG series с trigram-индексом (Catalog.Service.SuggestSeries)
//
// Если q пустой или слишком короткий (<2 символа) — отдаём пустые группы
// (200 OK), не 400: это естественное состояние "пользователь только начал
// печатать".
//
// Limit ограничен сверху 20 на каждую группу — палитра должна оставаться
// компактной.
func handleSuggest(bd BooksDeps, cat CatalogDeps, hist HistoryDeps, content ContentDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		limit := parseIntOr(r.URL.Query().Get("limit"), 5)
		if limit < 1 {
			limit = 5
		}
		if limit > 20 {
			limit = 20
		}
		resp := SuggestResponse{
			Query:   q,
			Books:   []books.ListItem{},
			Authors: []catalog.AuthorSuggest{},
			Series:  []catalog.SeriesSuggest{},
		}
		if len([]rune(q)) < 2 {
			writeJSON(w, http.StatusOK, resp)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		// userID — для персонализации книг в палитре поиска.
		// Если запрос пришёл от незалогиненного клиента, userID останется
		// нулевым и Suggest вернёт результаты в стандартном meili-порядке.
		var userID int64
		if u, ok := UserFromContext(r.Context()); ok {
			userID = u.ID
		}
		// Скрытые из выдачи жанры/языки (admin ∪ персональные) — палитра
		// поиска не должна подсказывать книги, которых нет в основном списке.
		var exGenres, exLangs []string
		var hideComps bool
		if content.Resolver != nil {
			exGenres, exLangs, hideComps = content.Resolver.Exclusions(r.Context(), userID)
		}

		var (
			bookItems   []books.ListItem
			authorItems []catalog.AuthorSuggest
			seriesItems []catalog.SeriesSuggest
			wg          sync.WaitGroup
		)
		wg.Add(3)

		go func() {
			defer wg.Done()
			if bd.Service == nil {
				return
			}
			// SuggestWorks: палитра отдаёт работы (id = works.id), ссылки ведут
			// на /works/{id} — как и веб-список. OPDS-поиск остаётся на Suggest.
			items, err := bd.Service.SuggestWorks(ctx, q, limit, userID, exGenres, exLangs, hideComps)
			if err == nil {
				bookItems = items
			}
		}()
		go func() {
			defer wg.Done()
			if cat.Service == nil {
				return
			}
			items, err := cat.Service.SuggestAuthors(ctx, q, limit)
			if err == nil {
				authorItems = items
			}
		}()
		go func() {
			defer wg.Done()
			if cat.Service == nil {
				return
			}
			items, err := cat.Service.SuggestSeries(ctx, q, limit)
			if err == nil {
				seriesItems = items
			}
		}()

		wg.Wait()

		// Доразметим is_favorite — отдельным запросом за множествами
		// favorite_{books,authors,series} пользователя. Это один
		// PersonaProfile-запрос (4 SELECT'а), но нам нужны только три
		// маленьких set'а; делаем их прямо тут, чтобы не тянуть лишнее.
		if userID > 0 && hist.Service != nil {
			_, favAuthors, favSeries := loadFavoriteSets(ctx, hist.Service, userID)
			// bookItems — это РАБОТЫ (id = works.id), поэтому is_favorite берём
			// work-level: работа избрана, если избрано любое её издание.
			workIDs := make([]int64, 0, len(bookItems))
			for i := range bookItems {
				workIDs = append(workIDs, bookItems[i].ID)
			}
			if favWorks, err := hist.Service.FavoriteWorkSet(ctx, userID, workIDs); err == nil {
				for i := range bookItems {
					if _, ok := favWorks[bookItems[i].ID]; ok {
						bookItems[i].IsFavorite = true
					}
				}
			}
			for i := range authorItems {
				if _, ok := favAuthors[authorItems[i].ID]; ok {
					authorItems[i].IsFavorite = true
				}
			}
			for i := range seriesItems {
				if _, ok := favSeries[seriesItems[i].ID]; ok {
					seriesItems[i].IsFavorite = true
				}
			}
		}

		if bookItems != nil {
			resp.Books = bookItems
		}
		if authorItems != nil {
			resp.Authors = authorItems
		}
		if seriesItems != nil {
			resp.Series = seriesItems
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// loadFavoriteSets — три set'а IDs (book/author/series) из истории
// пользователя. Используем PersonaProfile, чтобы не дублировать SQL:
// он уже делает ровно эти 3 SELECT'а. AuthorActivity и пр. в этом
// контексте отбрасываем.
//
// Возвращает три nil-safe map'а: ошибка в DB не должна ломать UI
// палитры — без is_favorite-флагов выдача всё равно полезна.
func loadFavoriteSets(ctx context.Context, hist *history.Service, userID int64) (
	books map[int64]struct{},
	authors map[int64]struct{},
	series map[int64]struct{},
) {
	profile, err := hist.PersonaProfile(ctx, userID)
	if err != nil {
		return nil, nil, nil
	}
	return profile.FavoriteBooks, profile.FavoriteAuthors, profile.FavoriteSeries
}
