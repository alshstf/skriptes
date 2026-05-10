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

// Series — детальная карточка серии (GET /api/series/:id).
type Series struct {
	ID         int64            `json:"id"`
	Title      string           `json:"title"`
	AuthorID   *int64           `json:"author_id,omitempty"`
	AuthorName string           `json:"author_name,omitempty"`
	BookCount  int              `json:"book_count"`
	Books      []books.ListItem `json:"books"` // отсортированы по ser_no, deleted скрыты
}
