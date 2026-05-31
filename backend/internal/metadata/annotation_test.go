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

// TestFb2Annotation_Paragraphs — основной happy path: несколько <p>
// внутри <annotation>, склеиваются \n\n. Inline-теги (<emphasis>)
// внутри <p> сохраняют только текст, без HTML.
func TestFb2Annotation_Paragraphs(t *testing.T) {
	fb2 := []byte(`<?xml version="1.0" encoding="utf-8"?>
<FictionBook>
  <description>
    <title-info>
      <annotation>
        <p>Первый параграф аннотации.</p>
        <p>Второй параграф с <emphasis>выделением</emphasis> внутри.</p>
      </annotation>
    </title-info>
  </description>
</FictionBook>`)

	zipPath := makeFB2Archive(t, fb2)
	p := NewFb2Provider()
	got, err := p.FetchAnnotation(context.Background(), BookQuery{
		ArchivePath: zipPath, FB2Name: "book.fb2",
	})
	require.NoError(t, err)
	require.Equal(t, "Первый параграф аннотации.\n\nВторой параграф с выделением внутри.", got)
}

func TestFb2Annotation_NoAnnotation(t *testing.T) {
	fb2 := []byte(`<?xml version="1.0"?><FictionBook><body/></FictionBook>`)
	zipPath := makeFB2Archive(t, fb2)
	p := NewFb2Provider()
	_, err := p.FetchAnnotation(context.Background(), BookQuery{
		ArchivePath: zipPath, FB2Name: "book.fb2",
	})
	require.ErrorIs(t, err, ErrNotFound)
}

// TestFb2Annotation_PlainText — fallback: текст внутри annotation
// БЕЗ оборачивания в <p>. Должны отдать как один параграф.
func TestFb2Annotation_PlainText(t *testing.T) {
	fb2 := []byte(`<?xml version="1.0"?>
<FictionBook>
  <description><title-info>
    <annotation>Просто текст без параграфов.</annotation>
  </title-info></description>
</FictionBook>`)
	zipPath := makeFB2Archive(t, fb2)
	p := NewFb2Provider()
	got, err := p.FetchAnnotation(context.Background(), BookQuery{
		ArchivePath: zipPath, FB2Name: "book.fb2",
	})
	require.NoError(t, err)
	require.Equal(t, "Просто текст без параграфов.", got)
}

func TestOpenLibrary_Annotation_HappyPath(t *testing.T) {
	const olid = "/works/OL12345W"
	const description = "Описание из Open Library, два параграфа."

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/search.json"):
			require.Equal(t, "Бесы", r.URL.Query().Get("title"))
			_ = json.NewEncoder(w).Encode(olSearchResponse{
				Docs: []olSearchDoc{{Key: olid}},
			})
		case strings.HasSuffix(r.URL.Path, ".json") && strings.Contains(r.URL.Path, "/works/"):
			_, _ = io.WriteString(w, `{"description":"`+description+`"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := NewOpenLibraryProvider(nil).WithEndpoints(srv.URL+"/search.json", srv.URL)
	got, err := p.FetchAnnotation(context.Background(), BookQuery{
		Title: "Бесы", Authors: []string{"Достоевский"},
	})
	require.NoError(t, err)
	require.Equal(t, description, got)
}

// OL иногда отдаёт description как object {type, value}, а не string.
func TestOpenLibrary_Annotation_DescriptionObject(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/search.json") {
			_ = json.NewEncoder(w).Encode(olSearchResponse{
				Docs: []olSearchDoc{{Key: "/works/OLxxxW"}},
			})
			return
		}
		_, _ = io.WriteString(w, `{"description":{"type":"/type/text","value":"From object form."}}`)
	}))
	defer srv.Close()

	p := NewOpenLibraryProvider(nil).WithEndpoints(srv.URL+"/search.json", srv.URL)
	got, err := p.FetchAnnotation(context.Background(), BookQuery{Title: "X"})
	require.NoError(t, err)
	require.Equal(t, "From object form.", got)
}

func TestGoogleBooks_Annotation_HappyPath(t *testing.T) {
	const description = "Description from Google Books."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"items":[{"id":"abc","volumeInfo":{"description":"`+description+`"}}]}`)
	}))
	defer srv.Close()
	p := NewGoogleBooksProvider(nil).WithEndpoint(srv.URL)
	got, err := p.FetchAnnotation(context.Background(), BookQuery{Title: "Title"})
	require.NoError(t, err)
	require.Equal(t, description, got)
}

func TestGoogleBooks_Annotation_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"items":[{"id":"abc","volumeInfo":{}}]}`)
	}))
	defer srv.Close()
	p := NewGoogleBooksProvider(nil).WithEndpoint(srv.URL)
	_, err := p.FetchAnnotation(context.Background(), BookQuery{Title: "Title"})
	require.ErrorIs(t, err, ErrNotFound)
}
