package opds

import (
	"strings"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/catalog"
	"github.com/stretchr/testify/require"
)

// BookToEntry — ключевая мапилка: книга → Atom entry с acquisition links.
// Проверяем что мы НИЧЕГО важного не теряем по пути (авторов, жанры,
// аннотацию, обложку) и что link'и собраны корректно.
func TestBookToEntry_FullBook(t *testing.T) {
	dateAdded := time.Date(2024, 5, 15, 0, 0, 0, 0, time.UTC)
	serNo := 2
	b := books.Book{
		ID:    123,
		Title: "Кадетский корпус. Книга 2",
		Authors: []books.AuthorRef{
			{ID: 17, FullName: "Алексеев Евгений Артёмович"},
		},
		Series:     &books.SeriesRef{ID: 7, Title: "Петля [Алексеев]"},
		SerNo:      &serNo,
		Genres:     []books.GenreRef{{ID: 1, Code: "sf_action", Display: "Боевая фантастика"}},
		Lang:       "ru",
		DateAdded:  &dateAdded,
		Annotation: "Длинная аннотация.\n\nС параграфами.",
		CoverPath:  "abc123.jpg",
		Archive:    "fb2.zip",
		FileName:   "1",
		Ext:        "fb2",
		SizeBytes:  100000,
	}
	formats := []FormatLink{
		{HrefPath: "/opds/books/123/download?format=epub3", MIME: "application/epub+zip", Title: "Скачать EPUB"},
	}
	e := BookToEntry(b, "https://example.com", formats)

	require.Equal(t, "urn:skriptes:book:123", e.ID)
	require.Equal(t, "Кадетский корпус. Книга 2", e.Title)
	require.Len(t, e.Authors, 1)
	require.Equal(t, "Алексеев Евгений Артёмович", e.Authors[0].Name)
	require.Equal(t, "ru", e.Language)
	require.Equal(t, "2024-05-15T00:00:00Z", e.Updated)
	require.Len(t, e.Categories, 1)
	require.Equal(t, "sf_action", e.Categories[0].Term)
	require.Equal(t, "Боевая фантастика", e.Categories[0].Label)
	require.NotNil(t, e.Content)
	require.Equal(t, "Длинная аннотация.\n\nС параграфами.", e.Content.Body)
	require.NotNil(t, e.Summary)
	require.Contains(t, e.Summary.Body, "Петля [Алексеев]")
	require.Contains(t, e.Summary.Body, "#2")
	require.Contains(t, e.Summary.Body, "KiB")

	// Links: ожидаем image + image/thumbnail + acquisition.
	rels := map[string]string{}
	for _, l := range e.Links {
		rels[l.Rel] = l.Href
	}
	require.Contains(t, rels[RelImage], "/opds/covers/abc123.jpg")
	require.Contains(t, rels[RelImage], "https://example.com")
	require.Contains(t, rels[RelImageThumbnail], "/opds/covers/abc123.jpg")
	require.Contains(t, rels[RelAcquisition], "/opds/books/123/download?format=epub3")
	require.True(t, strings.HasPrefix(rels[RelAcquisition], "https://example.com"))
}

// Книга без обложки и аннотации — image-link'ов не должно быть,
// content тоже.
func TestBookToEntry_NoCoverNoAnnotation(t *testing.T) {
	b := books.Book{
		ID:       1,
		Title:    "Title only",
		Authors:  []books.AuthorRef{{ID: 1, FullName: "Foo Bar"}},
		Archive:  "a.zip",
		FileName: "1",
		Ext:      "fb2",
	}
	e := BookToEntry(b, "https://x", nil)
	require.Nil(t, e.Content)
	for _, l := range e.Links {
		require.NotEqual(t, RelImage, l.Rel, "no cover → no image link")
		require.NotEqual(t, RelImageThumbnail, l.Rel)
	}
	require.NotEmpty(t, e.Updated) // Updated должен быть всегда (fallback на now)
}

// ListItemToEntry — версия для Meili-результатов, без аннотации/обложки.
func TestListItemToEntry(t *testing.T) {
	year := 2023
	serNo := 3
	seriesID := int64(7)
	it := books.ListItem{
		ID:       456,
		Title:    "Книга из списка",
		Authors:  []string{"Автор 1", "Автор 2"},
		Series:   "Моя серия",
		SeriesID: &seriesID,
		SerNo:    &serNo,
		Genres:   []string{"sf_action"},
		Year:     &year,
		Lang:     "ru",
	}
	makeFormats := func(id int64) []FormatLink {
		require.Equal(t, int64(456), id)
		return []FormatLink{{HrefPath: "/opds/books/456/download?format=epub3", MIME: "application/epub+zip", Title: "EPUB"}}
	}
	e := ListItemToEntry(it, "https://x", makeFormats)

	require.Equal(t, "urn:skriptes:book:456", e.ID)
	require.Equal(t, "Книга из списка", e.Title)
	require.Len(t, e.Authors, 2)
	require.Equal(t, "2023", e.Issued)
	require.Contains(t, e.Summary.Body, "Моя серия")
	require.Contains(t, e.Summary.Body, "#3")

	require.Len(t, e.Links, 1)
	require.Equal(t, RelAcquisition, e.Links[0].Rel)
	require.Contains(t, e.Links[0].Href, "/opds/books/456/download")
}

// Навигационные конвертеры — короткие, проверяем что href ведёт на
// правильный sub-feed.
func TestAuthorEntryToEntry(t *testing.T) {
	a := catalog.AuthorEntry{ID: 42, FullName: "Тест Тестов", BookCount: 5}
	e := AuthorEntryToEntry(a, "https://x")
	require.Equal(t, "urn:skriptes:author:42", e.ID)
	require.Equal(t, "Тест Тестов", e.Title)
	require.Contains(t, e.Summary.Body, "5 книг")
	require.Len(t, e.Links, 1)
	require.Equal(t, "https://x/opds/authors/42", e.Links[0].Href)
	require.Equal(t, MIMEFeedAcquisition, e.Links[0].Type)
}

func TestSeriesEntryToEntry_WithAuthor(t *testing.T) {
	s := catalog.SeriesEntry{ID: 7, Title: "Петля", AuthorName: "Алексеев Е. А.", BookCount: 3}
	e := SeriesEntryToEntry(s, "https://x")
	require.Contains(t, e.Title, "Петля")
	require.Contains(t, e.Title, "Алексеев Е. А.")
	require.Contains(t, e.Links[0].Href, "/opds/series/7")
}

func TestSeriesEntryToEntry_NoAuthor(t *testing.T) {
	s := catalog.SeriesEntry{ID: 99, Title: "Серия без автора", BookCount: 1}
	e := SeriesEntryToEntry(s, "https://x")
	require.Equal(t, "Серия без автора", e.Title) // без " — Автор" если AuthorName пуст
}

func TestGenreEntryToEntry(t *testing.T) {
	g := catalog.GenreEntry{ID: 5, Code: "sf_action", Display: "Боевая фантастика", BookCount: 1000}
	e := GenreEntryToEntry(g, "https://x")
	require.Equal(t, "Боевая фантастика", e.Title)
	require.Contains(t, e.Summary.Body, "1000")
	require.Contains(t, e.Links[0].Href, "/opds/genres/5")
}
