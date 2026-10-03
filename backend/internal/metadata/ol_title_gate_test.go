package metadata

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// #279: OpenLibrary по ISBN сборника или по названию одного рассказа находил
// работу сборника — теперь название найденного должно совпасть с книгой.
func TestOpenLibrary_ResolveWorkKeyChecksTitle(t *testing.T) {
	var searchTitle, isbnTitle string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/isbn/"):
			_, _ = io.WriteString(w, `{"title":`+jsonString(isbnTitle)+`,"works":[{"key":"/works/OLISBNW"}]}`)
		case r.URL.Path == "/search.json":
			_, _ = io.WriteString(w, `{"docs":[{"key":"/works/OLSEARCHW","author_name":["Иван Бунин"],"title":`+jsonString(searchTitle)+`}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p := NewOpenLibraryProvider(srv.Client()).WithEndpoints(srv.URL+"/search.json", "http://covers.example")
	q := WorkQuery{Title: "Танька", ISBN: "9785170000000", LastName: "Бунин", FirstName: "Иван", Authors: []string{"Бунин Иван"}}

	isbnTitle, searchTitle = "Тёмные аллеи", "Тёмные аллеи"
	_, err := p.ResolveWorkKey(context.Background(), q)
	require.ErrorIs(t, err, ErrNotFound, "ISBN и поиск нашли сборник, а не рассказ")

	isbnTitle = "Танька"
	key, err := p.ResolveWorkKey(context.Background(), q)
	require.NoError(t, err)
	require.Equal(t, "OLISBNW", key)

	isbnTitle, searchTitle = "Тёмные аллеи", "Танька"
	key, err = p.ResolveWorkKey(context.Background(), q)
	require.NoError(t, err)
	require.Equal(t, "OLSEARCHW", key, "ISBN мимо — поиск с подходящим названием")

	// Однословное название не совпадает с «продолжением» («Dune» ≠ «Dune Messiah»).
	searchTitle = "Танька и другие"
	_, err = p.ResolveWorkKey(context.Background(), q)
	require.ErrorIs(t, err, ErrNotFound)
}
