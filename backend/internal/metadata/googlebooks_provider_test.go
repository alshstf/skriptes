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

func TestGoogleBooks_HappyPath(t *testing.T) {
	const jpegBytes = "fake-gb-jpeg"

	// Сначала отвечаем на search, потом на cover.
	var coverHits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/volumes":
			q := r.URL.Query().Get("q")
			require.Contains(t, q, "intitle:")
			require.Contains(t, q, "Бесы")
			// Возвращаем item с http-thumbnail (провайдер должен поправить
			// его в https; внутри test сервер слушает http, поэтому
			// возвращаем URL который сам провайдер потом перепишет).
			// Чтобы тест прошёл — используем http URL обратно на этот сервер.
			thumb := strings.Replace(srv2URL(t), "http://", "http://", 1) + "/cover.jpg"
			_, _ = io.WriteString(w, `{"items":[{"id":"abc","volumeInfo":{"imageLinks":{"thumbnail":"`+thumb+`"}}}]}`)
		case "/cover.jpg":
			coverHits++
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = io.WriteString(w, jpegBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// Чтобы провайдер не делал http->https замену поверх тестового URL,
	// тестовый сервер слушает http и мы передаём http-URL как apiURL.
	p := NewGoogleBooksProvider(srv.Client()).WithEndpoint(srv.URL + "/volumes")
	// Заполняем srv2URL через простой helper — фактически URL текущего сервера.
	storeServerURL(t, srv.URL)

	img, err := p.FetchCover(context.Background(), BookQuery{
		Title: "Бесы", Authors: []string{"Достоевский"},
	})
	require.NoError(t, err)
	defer func() { _ = img.Reader.Close() }()
	body, err := io.ReadAll(img.Reader)
	require.NoError(t, err)
	require.Equal(t, jpegBytes, string(body))
	require.Equal(t, 1, coverHits)
}

func TestGoogleBooks_NoItems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"items":[]}`)
	}))
	defer srv.Close()
	p := NewGoogleBooksProvider(srv.Client()).WithEndpoint(srv.URL)
	_, err := p.FetchCover(context.Background(), BookQuery{Title: "X"})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestGoogleBooks_NoImageLinks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"items":[{"id":"abc","volumeInfo":{}}]}`)
	}))
	defer srv.Close()
	p := NewGoogleBooksProvider(srv.Client()).WithEndpoint(srv.URL)
	_, err := p.FetchCover(context.Background(), BookQuery{Title: "X"})
	require.ErrorIs(t, err, ErrNotFound)
}

// TestGoogleBooks_APIKey — ключ из WithAPIKey добавляется параметром key= ко
// всем запросам; без ключа параметра нет.
func TestGoogleBooks_APIKey(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.URL.Query().Get("key")
		_, _ = io.WriteString(w, `{"items":[]}`) // 0 items → ErrNotFound, key уже зафиксирован
	}))
	defer srv.Close()

	p := NewGoogleBooksProvider(srv.Client()).WithEndpoint(srv.URL).WithAPIKey("test-key-123")
	_, _ = p.FetchCover(context.Background(), BookQuery{Title: "X", Authors: []string{"A"}})
	require.Equal(t, "test-key-123", gotKey)

	gotKey = "sentinel"
	p2 := NewGoogleBooksProvider(srv.Client()).WithEndpoint(srv.URL)
	_, _ = p2.FetchCover(context.Background(), BookQuery{Title: "X"})
	require.Empty(t, gotKey)
}

// ─── helpers ────────────────────────────────────────────────────────
//
// Нам нужно знать URL httptest-сервера, чтобы вернуть из mock-search
// thumbnail на тот же сервер. Делаем простой in-memory storage.

var lastServerURL string

func storeServerURL(_ *testing.T, u string) { lastServerURL = u }
func srv2URL(_ *testing.T) string           { return lastServerURL }
