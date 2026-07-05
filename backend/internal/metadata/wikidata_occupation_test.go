package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// occSPARQLServer — httptest-мок /sparql, отдающий агрегат (total, writer)
// в формате sparql-results+json. Значения подставляются в canned-ответ.
func occSPARQLServer(t *testing.T, total, writer string) *httptest.Server {
	t.Helper()
	body := `{"results":{"bindings":[{"total":{"value":"` + total +
		`"},"writer":{"value":"` + writer + `"}}]}}`
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/sparql") {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/sparql-results+json")
		_, _ = w.Write([]byte(body))
	}))
}

func TestOccupationVerdict(t *testing.T) {
	cases := []struct {
		name         string
		total, write string
		want         OccupationVerdict
	}{
		{"writer among occupations", "3", "1", OccupationWriter},
		{"only writer", "1", "1", OccupationWriter},
		{"has occupations none writer", "2", "0", OccupationNonWriter},
		{"no occupations at all", "0", "0", OccupationUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv := occSPARQLServer(t, c.total, c.write)
			defer srv.Close()
			p := NewWikidataAdaptationsProvider(nil).WithEndpoints("", srv.URL+"/sparql", "")
			got, err := p.OccupationVerdict(context.Background(), "Q42")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("OccupationVerdict = %v, want %v", got, c.want)
			}
		})
	}
}

// Пустой QID — не ходим в сеть, сразу Unknown.
func TestOccupationVerdict_EmptyQID(t *testing.T) {
	p := NewWikidataAdaptationsProvider(nil) // endpoint не нужен — до сети не дойдём
	got, err := p.OccupationVerdict(context.Background(), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != OccupationUnknown {
		t.Errorf("empty QID = %v, want Unknown", got)
	}
}

// Ошибка апстрима (5xx) → Unknown + error проброшен: вызывающий (resolveTitle)
// не должен отвергать кандидата на транзиентной ошибке.
func TestOccupationVerdict_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	p := NewWikidataAdaptationsProvider(nil).WithEndpoints("", srv.URL+"/sparql", "")
	got, err := p.OccupationVerdict(context.Background(), "Q42")
	if err == nil {
		t.Fatal("expected error on 500")
	}
	if got != OccupationUnknown {
		t.Errorf("on error got %v, want Unknown", got)
	}
}
