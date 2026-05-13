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
