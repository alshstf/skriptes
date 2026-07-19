package metadata

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTMDBTestServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") == "" {
			t.Errorf("api_key не передан: %s", r.URL)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestTMDBPosterURL_Movie(t *testing.T) {
	srv := newTMDBTestServer(t, http.StatusOK, `{"id":4584,"poster_path":"/poster.jpg"}`)
	defer srv.Close()
	p := NewTMDBPosterProvider("k").WithBaseURLs(srv.URL, "https://img.example")

	u, err := p.PosterURL(context.Background(), "4584", "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if want := "https://img.example/t/p/w342/poster.jpg"; u != want {
		t.Fatalf("url = %q, want %q", u, want)
	}
}

func TestTMDBPosterURL_TVFallbackID(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"poster_path":"/tv.jpg"}`))
	}))
	defer srv.Close()
	p := NewTMDBPosterProvider("k").WithBaseURLs(srv.URL, "https://img.example")

	// movieID пуст → идём в /3/tv/{id}.
	if _, err := p.PosterURL(context.Background(), "", "13892"); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if gotPath != "/3/tv/13892" {
		t.Fatalf("path = %q, want /3/tv/13892", gotPath)
	}
}

// v4 «API Read Access Token» (JWT, "eyJ…") принимается наравне с v3-ключом:
// уходит Bearer-заголовком, api_key в query не передаётся.
func TestTMDBPosterURL_V4ReadToken(t *testing.T) {
	var gotAuth, gotQueryKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotQueryKey = r.URL.Query().Get("api_key")
		_, _ = w.Write([]byte(`{"poster_path":"/p.jpg"}`))
	}))
	defer srv.Close()
	p := NewTMDBPosterProvider("eyJhbGciOiJIUzI1NiJ9.token").WithBaseURLs(srv.URL, "https://img.example")

	u, err := p.PosterURL(context.Background(), "1", "")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if u == "" {
		t.Fatal("expected poster url")
	}
	if want := "Bearer eyJhbGciOiJIUzI1NiJ9.token"; gotAuth != want {
		t.Fatalf("Authorization = %q, want %q", gotAuth, want)
	}
	if gotQueryKey != "" {
		t.Fatalf("api_key в query не должен передаваться с v4-токеном, got %q", gotQueryKey)
	}
}

func TestTMDBPosterURL_NoIDs(t *testing.T) {
	p := NewTMDBPosterProvider("k")
	u, err := p.PosterURL(context.Background(), "", "")
	if err != nil || u != "" {
		t.Fatalf("без id ожидаем (\"\", nil), got (%q, %v)", u, err)
	}
}

// Честное отсутствие (нет постера / фильм неизвестен) → ErrNotFound;
// транзиент (429/5xx/битый ключ 401) → ErrUpstream (грабля №20 — попытка не
// окончательная, книга не хоронится).
func TestTMDBPosterURL_ErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"нет постера", http.StatusOK, `{"poster_path":null}`, ErrNotFound},
		{"404 неизвестный фильм", http.StatusNotFound, `{}`, ErrNotFound},
		{"429 rate limit", http.StatusTooManyRequests, `{}`, ErrUpstream},
		{"401 битый ключ", http.StatusUnauthorized, `{}`, ErrUpstream},
		{"500", http.StatusInternalServerError, `{}`, ErrUpstream},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTMDBTestServer(t, tc.status, tc.body)
			defer srv.Close()
			p := NewTMDBPosterProvider("k").WithBaseURLs(srv.URL, "https://img.example")
			_, err := p.PosterURL(context.Background(), "1", "")
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}
