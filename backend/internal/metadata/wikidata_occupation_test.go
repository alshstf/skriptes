package metadata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// factsSPARQLServer — мок /sparql для CandidateFacts: отвечает на три запроса
// (профессии и годы; классы профессий; работы P50) по тексту запроса.
func factsSPARQLServer(t *testing.T, occ, classes, works []map[string]string) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = r.ParseForm()
		q := r.PostForm.Get("query")
		var rows []map[string]string
		switch {
		case strings.Contains(q, "wdt:P569"):
			rows = occ
		case strings.Contains(q, "wdt:P279*"):
			rows = classes
		case strings.Contains(q, "wdt:P50"):
			rows = works
		default:
			http.Error(w, "unexpected query", http.StatusBadRequest)
			return
		}
		bindings := make([]map[string]map[string]string, 0, len(rows))
		for _, row := range rows {
			b := map[string]map[string]string{}
			for k, v := range row {
				b[k] = map[string]string{"value": v}
			}
			bindings = append(bindings, b)
		}
		w.Header().Set("Content-Type", "application/sparql-results+json")
		_ = json.NewEncoder(w).Encode(map[string]any{"results": map[string]any{"bindings": bindings}})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestCandidateFacts(t *testing.T) {
	const wd = "http://www.wikidata.org/entity/"
	srv, _ := factsSPARQLServer(t,
		[]map[string]string{
			{"occ": wd + "Q36180", "occLabel": "писатель", "born": "1931", "died": "2015"},
			{"occ": wd + "Q11631", "occLabel": "космонавт", "born": "1931", "died": "2015"},
		},
		[]map[string]string{{"base": wd + "Q36180"}},
		[]map[string]string{{"wLabel": "Прикосновение космоса"}},
	)
	p := NewWikidataAdaptationsProvider(nil).WithEndpoints("", srv.URL+"/sparql", "")
	f, err := p.CandidateFacts(context.Background(), "Q465748")
	require.NoError(t, err)
	require.Equal(t, []string{"космонавт", "писатель"}, f.Occupations)
	require.True(t, f.Writer)
	require.False(t, f.Adjacent)
	require.Equal(t, 1931, f.Born)
	require.Equal(t, 2015, f.Died)
	require.Equal(t, []string{"Прикосновение космоса"}, f.Works)
	require.Equal(t, "writer", f.occupationClass())

	// Только смежная профессия; нет профессий — unknown.
	srv2, _ := factsSPARQLServer(t,
		[]map[string]string{{"occ": wd + "Q169470", "occLabel": "физик"}},
		[]map[string]string{{"base": wd + "Q901"}}, nil)
	p2 := NewWikidataAdaptationsProvider(nil).WithEndpoints("", srv2.URL+"/sparql", "")
	f, err = p2.CandidateFacts(context.Background(), "Q1")
	require.NoError(t, err)
	require.Equal(t, "adjacent", f.occupationClass())

	srv3, calls := factsSPARQLServer(t, []map[string]string{{"born": "1950"}}, nil, nil)
	p3 := NewWikidataAdaptationsProvider(nil).WithEndpoints("", srv3.URL+"/sparql", "")
	f, err = p3.CandidateFacts(context.Background(), "Q2")
	require.NoError(t, err)
	require.Equal(t, "unknown", f.occupationClass())
	require.Equal(t, 1950, f.Born)
	require.Equal(t, 2, *calls, "без профессий классы не спрашиваем")
}

// Пустой QID — не ходим в сеть, пустые факты.
func TestCandidateFacts_EmptyQID(t *testing.T) {
	f, err := NewWikidataAdaptationsProvider(nil).CandidateFacts(context.Background(), "")
	require.NoError(t, err)
	require.Equal(t, "unknown", f.occupationClass())
}

// Ошибка апстрима — ошибка (вызывающий считает её временным сбоем, не «принять»).
func TestCandidateFacts_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	p := NewWikidataAdaptationsProvider(nil).WithEndpoints("", srv.URL+"/sparql", "")
	_, err := p.CandidateFacts(context.Background(), "Q42")
	require.Error(t, err)
}
