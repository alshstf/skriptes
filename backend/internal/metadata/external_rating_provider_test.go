package metadata

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// ── Google Books FetchRating ────────────────────────────────────

func TestGoogleBooksFetchRating_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/volumes", r.URL.Path)
		require.Contains(t, r.URL.Query().Get("q"), "intitle:")
		_, _ = io.WriteString(w, `{"items":[{"id":"x","volumeInfo":{"averageRating":4.5,"ratingsCount":120}}]}`)
	}))
	defer srv.Close()

	p := NewGoogleBooksProvider(srv.Client()).WithEndpoint(srv.URL + "/volumes")
	res, err := p.FetchRating(context.Background(), WorkQuery{Title: "Бесы", Authors: []string{"Достоевский"}})
	require.NoError(t, err)
	require.InDelta(t, 4.5, res.Average, 0.001)
	require.Equal(t, 120, res.Count)
}

func TestGoogleBooksFetchRating_ISBNQuery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "isbn:9780140440355", r.URL.Query().Get("q"), "ISBN-first запрос")
		_, _ = io.WriteString(w, `{"items":[{"id":"x","volumeInfo":{"averageRating":4.0,"ratingsCount":7}}]}`)
	}))
	defer srv.Close()

	p := NewGoogleBooksProvider(srv.Client()).WithEndpoint(srv.URL)
	res, err := p.FetchRating(context.Background(), WorkQuery{Title: "ignored", ISBN: "978-0-14-044035-5"})
	require.NoError(t, err)
	require.InDelta(t, 4.0, res.Average, 0.001)
	require.Equal(t, 7, res.Count)
}

func TestGoogleBooksFetchRating_NoRating(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// item без averageRating → ErrNotFound.
		_, _ = io.WriteString(w, `{"items":[{"id":"x","volumeInfo":{"description":"d"}}]}`)
	}))
	defer srv.Close()

	p := NewGoogleBooksProvider(srv.Client()).WithEndpoint(srv.URL)
	_, err := p.FetchRating(context.Background(), WorkQuery{Title: "T"})
	require.ErrorIs(t, err, ErrNotFound)
}

// ── OpenLibrary FetchRating ─────────────────────────────────────

func TestOpenLibraryFetchRating_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/search.json"):
			_, _ = io.WriteString(w, `{"docs":[{"key":"/works/OL123W","author_name":["Leo Tolstoy"]}]}`)
		case r.URL.Path == "/works/OL123W/ratings.json":
			_, _ = io.WriteString(w, `{"summary":{"average":4.2,"count":99}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := NewOpenLibraryProvider(srv.Client()).WithEndpoints(srv.URL+"/search.json", "http://covers.example")
	res, err := p.FetchRating(context.Background(), WorkQuery{
		Title: "War and Peace", Authors: []string{"Leo Tolstoy"}, LastName: "Tolstoy",
	})
	require.NoError(t, err)
	require.InDelta(t, 4.2, res.Average, 0.001)
	require.Equal(t, 99, res.Count)
}

func TestOpenLibraryFetchRating_NoRatings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/search.json"):
			_, _ = io.WriteString(w, `{"docs":[{"key":"/works/OL9W","author_name":["Leo Tolstoy"]}]}`)
		case r.URL.Path == "/works/OL9W/ratings.json":
			_, _ = io.WriteString(w, `{"summary":{"average":null,"count":0}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := NewOpenLibraryProvider(srv.Client()).WithEndpoints(srv.URL+"/search.json", "http://covers.example")
	_, err := p.FetchRating(context.Background(), WorkQuery{Title: "X", Authors: []string{"Leo Tolstoy"}, LastName: "Tolstoy"})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestOpenLibraryFetchRating_WorkNotResolved(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"docs":[]}`) // работа не нашлась
	}))
	defer srv.Close()

	p := NewOpenLibraryProvider(srv.Client()).WithEndpoints(srv.URL+"/search.json", "http://covers.example")
	_, err := p.FetchRating(context.Background(), WorkQuery{Title: "X", LastName: "Y"})
	require.True(t, errors.Is(err, ErrNotFound))
}
