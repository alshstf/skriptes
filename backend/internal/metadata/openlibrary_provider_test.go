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

func TestOpenLibrary_HappyPath(t *testing.T) {
	// Search возвращает один doc с cover_i=42; cover-endpoint отдаёт JPEG.
	const coverID int64 = 42
	const jpegBytes = "fake-jpeg-data"

	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Бесы", r.URL.Query().Get("title"))
		require.Equal(t, "Достоевский", r.URL.Query().Get("author"))
		_ = json.NewEncoder(w).Encode(olSearchResponse{
			Docs: []struct {
				CoverI int64  `json:"cover_i"`
				Key    string `json:"key"`
			}{{CoverI: coverID, Key: "/works/OL12345W"}},
		})
	}))
	defer search.Close()

	covers := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, fmt.Sprintf("/b/id/%d-L.jpg", coverID), r.URL.Path)
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = io.WriteString(w, jpegBytes)
	}))
	defer covers.Close()

	p := NewOpenLibraryProvider(nil).WithEndpoints(search.URL+"/search.json", covers.URL)
	img, err := p.FetchCover(context.Background(), BookQuery{
		Title:   "Бесы",
		Authors: []string{"Достоевский"},
	})
	require.NoError(t, err)
	require.NotNil(t, img)
	defer func() { _ = img.Reader.Close() }()
	body, err := io.ReadAll(img.Reader)
	require.NoError(t, err)
	require.Equal(t, jpegBytes, string(body))
	require.Equal(t, "image/jpeg", img.Mime)
	require.True(t, strings.HasPrefix(img.SourceID, "ol:cover:"))
}

func TestOpenLibrary_NotFound(t *testing.T) {
	// Search возвращает пустые docs.
	search := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"docs":[]}`)
	}))
	defer search.Close()

	p := NewOpenLibraryProvider(nil).WithEndpoints(search.URL, "http://covers.example") // covers не должны вызываться
	_, err := p.FetchCover(context.Background(), BookQuery{Title: "Unknown"})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestOpenLibrary_EmptyTitle(t *testing.T) {
	p := NewOpenLibraryProvider(nil)
	_, err := p.FetchCover(context.Background(), BookQuery{})
	require.ErrorIs(t, err, ErrNotFound)
}
