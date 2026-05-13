package history

import "time"

// FavoriteItem — строка в /api/me/favorites.
type FavoriteItem struct {
	ID      int64     `json:"id"`
	Title   string    `json:"title"`
	Authors []string  `json:"authors"`
	Series  string    `json:"series,omitempty"`
	Lang    string    `json:"lang,omitempty"`
	LibID   string    `json:"lib_id"`
	AddedAt time.Time `json:"added_at"`
}

// ViewedItem — строка в /api/me/recent.
type ViewedItem struct {
	ID           int64     `json:"id"`
	Title        string    `json:"title"`
	Authors      []string  `json:"authors"`
	LastViewedAt time.Time `json:"last_viewed_at"`
}

// FavoritesListResponse — обёртка для пагинированного списка.
type FavoritesListResponse struct {
	Items  []FavoriteItem `json:"items"`
	Total  int            `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}

// FavoriteAuthorItem — строка в /api/me/favorites/authors.
type FavoriteAuthorItem struct {
	ID        int64     `json:"id"`
	FullName  string    `json:"full_name"`
	BookCount int       `json:"book_count"`
	AddedAt   time.Time `json:"added_at"`
}

// FavoriteSeriesItem — строка в /api/me/favorites/series.
type FavoriteSeriesItem struct {
	ID         int64     `json:"id"`
	Title      string    `json:"title"`
	AuthorName string    `json:"author_name,omitempty"`
	BookCount  int       `json:"book_count"`
	AddedAt    time.Time `json:"added_at"`
}

// AllFavoritesResponse — единая выдача "Моё избранное": книги, авторы,
// серии вместе. Используется одним эндпоинтом GET /api/me/favorites для
// фронтенда, чтобы не тратить три запроса при загрузке страницы.
type AllFavoritesResponse struct {
	Books   []FavoriteItem       `json:"books"`
	Authors []FavoriteAuthorItem `json:"authors"`
	Series  []FavoriteSeriesItem `json:"series"`
}
