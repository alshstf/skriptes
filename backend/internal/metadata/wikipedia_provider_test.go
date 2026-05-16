package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// wikiMockServer — общий httptest-server, имитирующий Wikipedia API.
//
// /w/api.php обслуживает оба action'а — opensearch (для resolveTitle)
// и query (для extract'а полного intro-раздела). Для summary endpoint
// (используется только в FetchAuthorPhoto) — /api/rest_v1/page/summary/.
//
// fullExtract — текст intro-раздела; для биографических тестов это
// "Полный текст" (~> чем у summary).
func wikiMockServer(t *testing.T, titles []string, summary wikiSummary, fullExtract string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/w/api.php":
			switch r.URL.Query().Get("action") {
			case "opensearch":
				arr := []any{r.URL.Query().Get("search"), titles, []string{}, []string{}}
				_ = json.NewEncoder(w).Encode(arr)
			case "query":
				// formatversion=2 → pages как массив.
				_, _ = io.WriteString(w, `{"query":{"pages":[{"extract":`+jsonString(fullExtract)+`}]}}`)
			default:
				http.NotFound(w, r)
			}
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

// jsonString — кавычки + escape для inline JSON в test-сервере.
// Используем json.Marshal — он сам обработает кириллицу и спецсимволы.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestWikipedia_BioHappyPath(t *testing.T) {
	// extract — это уже не короткий summary, а полный intro раздел.
	const fullBio = "Фёдор Михайлович Достоевский (1821-1881) — русский писатель. " +
		"Родился в Москве. Учился в Военно-инженерном училище. " +
		"Написал «Преступление и наказание», «Идиот» и другие романы."
	srv := wikiMockServer(t,
		[]string{"Достоевский,_Фёдор_Михайлович"},
		wikiSummary{Title: "Достоевский", Type: "standard", Extract: "Краткое (не используется)"},
		fullBio,
	)
	defer srv.Close()

	p := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL)
	got, err := p.FetchAuthorBio(context.Background(), AuthorQuery{
		FullName: "Достоевский Фёдор Михайлович",
	})
	require.NoError(t, err)
	require.Equal(t, fullBio, got)
}

func TestWikipedia_BioEmptyOpensearch_NotFound(t *testing.T) {
	srv := wikiMockServer(t, []string{}, wikiSummary{}, "")
	defer srv.Close()
	p := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL)
	_, err := p.FetchAuthorBio(context.Background(), AuthorQuery{FullName: "Несуществующий Автор"})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestWikipedia_PhotoHappyPath(t *testing.T) {
	// Этот тест собирает сервер вручную (а не через wikiMockServer),
	// потому что thumbnail.source должен указывать абсолютно на сам же
	// тестовый сервер — то есть URL известен только после его старта.
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
		"",
	)
	defer srv.Close()
	p := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL)
	_, err := p.FetchAuthorPhoto(context.Background(), AuthorQuery{FullName: "X"})
	require.ErrorIs(t, err, ErrNotFound)
}

// TestOpenLibrary_AuthorBioHappyPath — поиск по author search, потом
// /authors/{OLID}.json возвращает bio. Поддерживаем bio как string
// и как object{value}, как у работ.
func TestOpenLibrary_AuthorBioHappyPath(t *testing.T) {
	const olid = "OL12345A"
	const bio = "Биография автора из Open Library."

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/search/authors.json"):
			_, _ = io.WriteString(w, `{"docs":[{"key":"/authors/`+olid+`","name":"Test"}]}`)
		case strings.HasSuffix(r.URL.Path, "/authors/"+olid+".json"):
			_, _ = io.WriteString(w, `{"bio":"`+bio+`","photos":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := NewOpenLibraryProvider(nil).WithEndpoints(srv.URL+"/search.json", srv.URL)
	got, err := p.FetchAuthorBio(context.Background(), AuthorQuery{FullName: "Test Author"})
	require.NoError(t, err)
	require.Equal(t, bio, got)
}

func TestOpenLibrary_AuthorBioObjectForm(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/search/authors.json"):
			_, _ = io.WriteString(w, `{"docs":[{"key":"OLxxxA","name":"X"}]}`)
		default:
			_, _ = io.WriteString(w, `{"bio":{"type":"/type/text","value":"From object."},"photos":[]}`)
		}
	}))
	defer srv.Close()
	p := NewOpenLibraryProvider(nil).WithEndpoints(srv.URL+"/search.json", srv.URL)
	got, err := p.FetchAuthorBio(context.Background(), AuthorQuery{FullName: "X"})
	require.NoError(t, err)
	require.Equal(t, "From object.", got)
}

func TestOpenLibrary_AuthorBioNoResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"docs":[]}`)
	}))
	defer srv.Close()
	p := NewOpenLibraryProvider(nil).WithEndpoints(srv.URL+"/search.json", srv.URL)
	_, err := p.FetchAuthorBio(context.Background(), AuthorQuery{FullName: "Unknown"})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestOpenLibrary_AuthorPhotoHappyPath(t *testing.T) {
	const photoID = 42
	const jpegBytes = "fake-author-jpeg"

	covers := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, fmt.Sprintf("/a/id/%d-L.jpg", photoID), r.URL.Path)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = io.WriteString(w, jpegBytes)
	}))
	defer covers.Close()

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/search/authors.json"):
			_, _ = io.WriteString(w, `{"docs":[{"key":"OLxxxA","name":"X"}]}`)
		default:
			_, _ = io.WriteString(w, fmt.Sprintf(`{"bio":"","photos":[-1, %d, 7]}`, photoID))
		}
	}))
	defer api.Close()

	p := NewOpenLibraryProvider(nil).WithEndpoints(api.URL+"/search.json", covers.URL)
	img, err := p.FetchAuthorPhoto(context.Background(), AuthorQuery{FullName: "X"})
	require.NoError(t, err)
	defer func() { _ = img.Reader.Close() }()
	body, err := io.ReadAll(img.Reader)
	require.NoError(t, err)
	require.Equal(t, jpegBytes, string(body))
}

func TestOpenLibrary_AuthorPhotoNoPositiveIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/search/authors.json"):
			_, _ = io.WriteString(w, `{"docs":[{"key":"OLxxxA"}]}`)
		default:
			_, _ = io.WriteString(w, `{"bio":"x","photos":[-1, -1]}`)
		}
	}))
	defer srv.Close()
	p := NewOpenLibraryProvider(nil).WithEndpoints(srv.URL+"/search.json", srv.URL)
	_, err := p.FetchAuthorPhoto(context.Background(), AuthorQuery{FullName: "X"})
	require.ErrorIs(t, err, ErrNotFound)
}
