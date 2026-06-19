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

// ContinueItem — строка в /api/me/continue-reading: книга, которую
// пользователь начал, но не дочитал (reads.fraction > 0, completed_at IS NULL).
//
// ID — это id ИЗДАНИЯ (reads.book_id): прогресс/CFI привязаны к конкретному
// fb2-файлу, поэтому «продолжить» ведёт именно к нему. WorkID — логическая
// книга для ссылки на карточку (фронт: /works/{work_id ?? id}). CoverPath
// догидрачивается из Postgres (в Meili-индексе обложек нет).
type ContinueItem struct {
	ID        int64     `json:"id"`
	WorkID    int64     `json:"work_id,omitempty"`
	Title     string    `json:"title"`
	Authors   []string  `json:"authors"`
	Series    string    `json:"series,omitempty"`
	LibID     string    `json:"lib_id"`
	CoverPath string    `json:"cover_path,omitempty"`
	Fraction  float64   `json:"fraction"`
	UpdatedAt time.Time `json:"updated_at"`
}

// FeedItem — строка в /api/me/feed/subscriptions: свежая книга автора, на
// которого подписан пользователь. Свежесть = books.date_added (когда книга
// появилась в библиотеке; см. граблю про date_added ≠ год написания).
//
// ID — представительное издание работы (для on-demand-обложки), WorkID — id
// работы для ссылки на карточку. AddedAt — date_added представителя.
type FeedItem struct {
	ID        int64      `json:"id"`
	WorkID    int64      `json:"work_id,omitempty"`
	Title     string     `json:"title"`
	Authors   []string   `json:"authors"`
	Series    string     `json:"series,omitempty"`
	LibID     string     `json:"lib_id"`
	CoverPath string     `json:"cover_path,omitempty"`
	AddedAt   *time.Time `json:"added_at,omitempty"`
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
