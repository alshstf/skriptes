// Package catalog содержит read-only сервисы для авторов и серий.
// Книги — в internal/books (там Meilisearch); здесь упор на связи и
// агрегации, которые быстрее берутся из Postgres.
package catalog

import "github.com/skriptes/skriptes/backend/internal/books"

// Author — детальная карточка автора (GET /api/authors/:id).
type Author struct {
	ID         int64  `json:"id"`
	LastName   string `json:"last_name"`
	FirstName  string `json:"first_name,omitempty"`
	MiddleName string `json:"middle_name,omitempty"`
	FullName   string `json:"full_name"`

	BookCount  int               `json:"book_count"`
	TopGenres  []GenreCount      `json:"top_genres,omitempty"`
	Series     []SeriesWithCount `json:"series,omitempty"`
	BooksTotal int               `json:"books_total"`
	Books      []books.ListItem  `json:"books"`

	// YearStats — распределение книг автора по году добавления в коллекцию.
	// Используется для гистограммы на странице автора (recharts).
	// Сортировка по году по возрастанию; книги без date_added не попадают.
	YearStats []YearCount `json:"year_stats,omitempty"`

	// ReadCount — сколько книг автора есть в reads текущего пользователя
	// (read = "скачана хотя бы раз" до сборки in-browser reader'а).
	// Заполняется только если в запрос пробрасывается user (см. GetAuthor),
	// иначе 0 и фронт скрывает прогресс-блок.
	ReadCount int `json:"read_count,omitempty"`
}

// YearCount — пара (год, число книг). nil-Year не отправляем.
type YearCount struct {
	Year  int `json:"year"`
	Count int `json:"count"`
}

// GenreCount — пара (genre, books_in_this_genre_for_this_author).
type GenreCount struct {
	Code    string `json:"code"`
	Display string `json:"display"`
	Count   int    `json:"count"`
}

// SeriesWithCount — серия + сколько книг этого автора в ней.
type SeriesWithCount struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Count int    `json:"count"`
}

// AuthorSuggest — строка в typeahead-выдаче авторов.
// IsFavorite — заполняется api-handler'ом, не сервисом catalog
// (он не знает про user-сессию).
type AuthorSuggest struct {
	ID         int64  `json:"id"`
	FullName   string `json:"full_name"`
	BookCount  int    `json:"book_count"`
	IsFavorite bool   `json:"is_favorite,omitempty"`
}

// SeriesSuggest — строка в typeahead-выдаче серий.
// AuthorName заполнено только если у серии один автор (привязан в схеме).
type SeriesSuggest struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	AuthorName string `json:"author_name,omitempty"`
	BookCount  int    `json:"book_count"`
	IsFavorite bool   `json:"is_favorite,omitempty"`
}

// Series — детальная карточка серии (GET /api/series/:id).
type Series struct {
	ID         int64            `json:"id"`
	Title      string           `json:"title"`
	AuthorID   *int64           `json:"author_id,omitempty"`
	AuthorName string           `json:"author_name,omitempty"`
	BookCount  int              `json:"book_count"`
	Books      []books.ListItem `json:"books"` // отсортированы по ser_no, deleted скрыты

	// Аналогично Author: гистограмма по годам и прогресс чтения.
	YearStats []YearCount `json:"year_stats,omitempty"`
	ReadCount int         `json:"read_count,omitempty"`
}
