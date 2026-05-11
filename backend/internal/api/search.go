package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/catalog"
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
func handleSuggest(bd BooksDeps, cat CatalogDeps) http.HandlerFunc {
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
			items, err := bd.Service.Suggest(ctx, q, limit)
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
