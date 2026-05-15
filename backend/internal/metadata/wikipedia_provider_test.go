package metadata

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// wikiMockServer — общий httptest-server, имитирующий Wikipedia API:
// /w/api.php (opensearch) и /api/rest_v1/page/summary/{title}.
//
// summary даётся как есть, opensearch отвечает первой записью из titles.
func wikiMockServer(t *testing.T, titles []string, summary wikiSummary) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/w/api.php":
			require.Equal(t, "opensearch", r.URL.Query().Get("action"))
			// формат opensearch: [query, [titles], [snippets], [urls]]
			arr := []any{r.URL.Query().Get("search"), titles, []string{}, []string{}}
			_ = json.NewEncoder(w).Encode(arr)
		case strings.HasPrefix(r.URL.Path, "/api/rest_v1/page/summary/"):
			_ = json.NewEncoder(w).Encode(summary)
		case r.URL.Path == "/img.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = io.WriteString(w, "fake-jpeg-bytes")
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestWikipedia_BioHappyPath(t *testing.T) {
	srv := wikiMockServer(t,
		[]string{"Достоевский,_Фёдор_Михайлович"},
		wikiSummary{Title: "Достоевский", Type: "standard", Extract: "Русский писатель..."},
	)
	defer srv.Close()

	p := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL)
	got, err := p.FetchAuthorBio(context.Background(), AuthorQuery{
		FullName: "Достоевский Фёдор Михайлович",
	})
	require.NoError(t, err)
	require.Equal(t, "Русский писатель...", got)
}

func TestWikipedia_BioDisambiguation_NotFound(t *testing.T) {
	srv := wikiMockServer(t,
		[]string{"Достоевский"},
		wikiSummary{Title: "Достоевский", Type: "disambiguation", Extract: "Может означать..."},
	)
	defer srv.Close()
	p := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL)
	_, err := p.FetchAuthorBio(context.Background(), AuthorQuery{FullName: "Достоевский"})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestWikipedia_BioEmptyOpensearch_NotFound(t *testing.T) {
	srv := wikiMockServer(t, []string{}, wikiSummary{})
	defer srv.Close()
	p := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL)
	_, err := p.FetchAuthorBio(context.Background(), AuthorQuery{FullName: "Несуществующий Автор"})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestWikipedia_PhotoHappyPath(t *testing.T) {
	srv := wikiMockServer(t,
		[]string{"X"},
		wikiSummary{
			Title:   "X",
			Type:    "standard",
			Extract: "x",
			Thumbnail: struct {
				Source string `json:"source"`
			}{Source: ""}, // заполним ниже
		},
	)
	defer srv.Close()
	// Подменим summary handler чтобы thumbnail.source ссылался на сам сервер.
	// Проще пересоздать сервер вручную:
	srv.Close()
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/w/api.php":
			_ = json.NewEncoder(w).Encode([]any{"q", []string{"X"}, []string{}, []string{}})
		case strings.HasPrefix(r.URL.Path, "/api/rest_v1/page/summary/"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"title": "X", "type": "standard", "extract": "x",
				"thumbnail": map[string]string{"source": "/img.jpg"},
			})
		case r.URL.Path == "/img.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = io.WriteString(w, "fake-jpeg-bytes")
		}
	}))
	defer srv2.Close()
	// Перепишем thumbnail на абсолютный URL текущего сервера.
	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/w/api.php":
			_ = json.NewEncoder(w).Encode([]any{"q", []string{"X"}, []string{}, []string{}})
		case strings.HasPrefix(r.URL.Path, "/api/rest_v1/page/summary/"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"title": "X", "type": "standard", "extract": "x",
				"thumbnail": map[string]string{"source": srv2.URL + "/img.jpg"},
			})
		}
	}))
	defer srv3.Close()

	p := NewWikipediaProvider(srv3.Client()).WithAPIRoot(srv3.URL)
	img, err := p.FetchAuthorPhoto(context.Background(), AuthorQuery{FullName: "X"})
	require.NoError(t, err)
	defer func() { _ = img.Reader.Close() }()
	body, err := io.ReadAll(img.Reader)
	require.NoError(t, err)
	require.Equal(t, "fake-jpeg-bytes", string(body))
	require.Equal(t, "image/jpeg", img.Mime)
}

func TestWikipedia_PhotoNoThumbnail_NotFound(t *testing.T) {
	srv := wikiMockServer(t,
		[]string{"X"},
		wikiSummary{Title: "X", Type: "standard", Extract: "x"},
	)
	defer srv.Close()
	p := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL)
	_, err := p.FetchAuthorPhoto(context.Background(), AuthorQuery{FullName: "X"})
	require.ErrorIs(t, err, ErrNotFound)
}
