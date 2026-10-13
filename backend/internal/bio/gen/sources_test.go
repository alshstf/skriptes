package gen

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTrimToBiography(t *testing.T) {
	text := "Лид.\n== Биография ==\nРодился.\n=== Детство ===\nРос.\n== Библиография ==\nКниги.\n=== Романы ===\nСписок.\n== Память ==\nПамятник.\n== Семья ==\nЖена."
	got := TrimToBiography(text, 0)
	for _, want := range []string{"Лид.", "Родился.", "Рос.", "Жена."} {
		if !strings.Contains(got, want) {
			t.Errorf("пропало %q", want)
		}
	}
	for _, drop := range []string{"Книги.", "Список.", "Памятник."} {
		if strings.Contains(got, drop) {
			t.Errorf("не вырезано %q", drop)
		}
	}
	if r := []rune(TrimToBiography(text, 10)); len(r) != 10 {
		t.Errorf("кап не сработал: %d", len(r))
	}
}

func TestIsoDate(t *testing.T) {
	if isoDate("1868-05-24T00:00:00Z") != "1868-05-24" || isoDate("") != "" {
		t.Fatal("isoDate")
	}
	if qidFromURI("http://www.wikidata.org/entity/Q991") != "Q991" || qidFromURI("x") != "" {
		t.Fatal("qidFromURI")
	}
}

func TestFetchArticleAndFacts(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/sparql", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Query().Get("query"), "wd:Q991") {
			t.Errorf("нет QID в запросе")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": map[string]any{"bindings": []map[string]map[string]string{
			{"prop": {"value": "P569"}, "date": {"value": "1821-11-11T00:00:00Z"}, "v": {"value": "http://www.wikidata.org/entity/Q649"}, "vLabel": {"value": "Москва"}},
			{"prop": {"value": "P40"}, "v": {"value": "http://www.wikidata.org/entity/Q1"}, "vLabel": {"value": "Софья"}, "vBorn": {"value": "1868-03-05T00:00:00Z"}, "vDied": {"value": "1868-05-24T00:00:00Z"}},
			{"prop": {"value": "P40"}, "v": {"value": "http://www.wikidata.org/entity/Q1"}, "vLabel": {"value": "Софья"}, "vBorn": {"value": "1868-03-05T00:00:00Z"}, "vDied": {"value": "1868-05-24T00:00:00Z"}}, // дубль
		}}})
	})
	mux.HandleFunc("/wd", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"entities": map[string]any{"Q991": map[string]any{"sitelinks": map[string]any{
			"ruwiki": map[string]any{"title": "Достоевский, Фёдор"}, "enwiki": map[string]any{"title": "Dostoevsky"},
		}}}})
	})
	mux.HandleFunc("/ru", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("titles") != "Достоевский, Фёдор" {
			t.Errorf("titles=%q", r.URL.Query().Get("titles"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"query": map[string]any{"pages": []map[string]any{{
			"title": "Достоевский, Фёдор", "extract": strings.Repeat("Биография. ", 2000) + "\n== Библиография ==\nКниги.",
			"revisions": []map[string]any{{"revid": 123}},
		}}}})
	})
	mux.HandleFunc("/en", func(w http.ResponseWriter, r *http.Request) {
		// Короткая en при длинной ru — запрашивается, но в источники не попадает.
		_ = json.NewEncoder(w).Encode(map[string]any{"query": map[string]any{"pages": []map[string]any{{
			"title": "Dostoevsky", "extract": "Short English article.", "revisions": []map[string]any{{"revid": 7}},
		}}}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	f := NewFetcher(srv.Client())
	f.SPARQLURL = srv.URL + "/sparql"
	f.WikidataAPI = func() string { return srv.URL + "/wd" }
	f.WikiAPI = func(lang string) string { return srv.URL + "/" + lang }

	src, err := f.Fetch(context.Background(), "Q991")
	if err != nil {
		t.Fatal(err)
	}
	if src.Born != "1821-11-11" || len(src.Facts) != 2 {
		t.Fatalf("born=%q facts=%d", src.Born, len(src.Facts))
	}
	if len(src.Articles) != 1 || src.Articles[0].Rev != 123 || strings.Contains(src.Articles[0].Text, "Книги.") {
		t.Fatalf("article: %+v", src.Articles)
	}
	// Явный список языков — обе статьи, в заданном порядке.
	f.Langs = []string{"ru", "en"}
	src, err = f.Fetch(context.Background(), "Q991")
	if err != nil {
		t.Fatal(err)
	}
	if len(src.Articles) != 2 || src.Articles[1].Lang != "en" {
		t.Fatalf("langs: %+v", src.Articles)
	}
}
