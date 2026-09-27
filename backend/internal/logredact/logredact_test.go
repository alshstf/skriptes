package logredact

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"testing"
)

func TestString(t *testing.T) {
	for in, want := range map[string]string{
		`Get "https://api.themoviedb.org/3/movie/1645?api_key=abc123def": dial tcp [::1]:443`: `Get "https://api.themoviedb.org/3/movie/1645?api_key=REDACTED": dial tcp [::1]:443`,
		"https://www.googleapis.com/books/v1/volumes?q=x&key=AIzaSyXX&country=US":             "https://www.googleapis.com/books/v1/volumes?q=x&key=REDACTED&country=US",
		"https://x.test/a?access_token=eyJhbGci.x.y":                                          "https://x.test/a?access_token=REDACTED",
		// Поля, которые лишь заканчиваются на key, не трогаем.
		"work_key=OL123W source=openlibrary": "work_key=OL123W source=openlibrary",
		"без секретов":                       "без секретов",
	} {
		if got := String(in); got != want {
			t.Errorf("String(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestReplaceAttr_RedactsErrorsAndStrings(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{ReplaceAttr: ReplaceAttr}))

	ue := &url.Error{Op: "Get", URL: "https://api.themoviedb.org/3/movie/1?api_key=SECRET42", Err: errors.New("connection refused")}
	log.Warn("tmdb poster lookup failed", "err", fmt.Errorf("upstream error: tmdb: %w", ue), "book_id", 1)
	log.Info("request https://x.test/?key=SECRET43 done", "url", "https://x.test/?key=SECRET44")

	out := buf.String()
	for _, secret := range []string{"SECRET42", "SECRET43", "SECRET44"} {
		if strings.Contains(out, secret) {
			t.Errorf("секрет %s попал в лог: %s", secret, out)
		}
	}
	if !strings.Contains(out, "api_key=REDACTED") || !strings.Contains(out, `"book_id":1`) {
		t.Errorf("лог испорчен: %s", out)
	}
}
