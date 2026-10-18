package gen

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

func TestTrimToBiographyOtherLangs(t *testing.T) {
	text := "Lead.\n== Leben ==\nGeboren.\n== Werk ==\nSchrieb nachts.\n== Werke (Auswahl) ==\nListe.\n== Rezeption ==\nKritik.\n== Einzelnachweise ==\nFußnote.\n== Œuvres ==\nLivres.\n== Obras ==\nLibros.\n== Przypisy ==\nPrzypis.\n== 脚注 ==\n注."
	got := TrimToBiography(text, 0)
	for _, want := range []string{"Geboren.", "Schrieb nachts."} {
		if !strings.Contains(got, want) {
			t.Errorf("пропало %q", want)
		}
	}
	for _, drop := range []string{"Liste.", "Kritik.", "Fußnote.", "Livres.", "Libros.", "Przypis.", "注."} {
		if strings.Contains(got, drop) {
			t.Errorf("не вырезано %q", drop)
		}
	}
}

func TestOrigLang(t *testing.T) {
	for _, c := range []struct {
		written, native []string
		want            string
	}{
		{[]string{"de"}, []string{"de"}, "de"}, // Кафка, Гессе
		{nil, []string{"pl"}, "pl"},            // нет P6886 — родной
		{[]string{"ru", "en"}, nil, ""},        // Набоков: ru/en берутся и так
		{[]string{"en"}, []string{"pl"}, ""},   // Конрад: пишет по-английски
		{[]string{"fr", "cs"}, nil, "cs"},      // Кундера: первый по алфавиту
		{[]string{"nb"}, nil, "no"},            // букмол → no.wikipedia.org
		{[]string{"en-gb"}, nil, ""},           // британский английский — en берётся и так (Диккенс)
		{[]string{"pt-br"}, nil, "pt"},         // регион → основной раздел
		{[]string{"be-tarask"}, nil, "be-tarask"},
		{nil, nil, ""},
	} {
		if got := origLang(c.written, c.native); got != c.want {
			t.Errorf("origLang(%v, %v) = %q, want %q", c.written, c.native, got, c.want)
		}
	}
}

func TestFetchOrigLangArticle(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/sparql", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"results": map[string]any{"bindings": []map[string]map[string]string{
			{"prop": {"value": "P569"}, "date": {"value": "1883-07-03T00:00:00Z"}},
			{"prop": {"value": "P6886"}, "v": {"value": "http://www.wikidata.org/entity/Q188"}, "vLabel": {"value": "немецкий"}, "code": {"value": "de"}},
			{"prop": {"value": "P103"}, "v": {"value": "http://www.wikidata.org/entity/Q188"}, "vLabel": {"value": "немецкий"}, "code": {"value": "de"}},
		}}})
	})
	mux.HandleFunc("/wd", func(w http.ResponseWriter, r *http.Request) {
		site := r.URL.Query().Get("sitefilter")
		_ = json.NewEncoder(w).Encode(map[string]any{"entities": map[string]any{"Q905": map[string]any{"sitelinks": map[string]any{
			site: map[string]any{"title": "Kafka " + site},
		}}}})
	})
	page := func(extract string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"query": map[string]any{"pages": []map[string]any{{
				"title": r.URL.Query().Get("titles"), "extract": extract, "revisions": []map[string]any{{"revid": 1}},
			}}}})
		}
	}
	mux.HandleFunc("/ru", page(strings.Repeat("Биография. ", 1500)))
	mux.HandleFunc("/en", page(strings.Repeat("Biography. ", 1000)))
	mux.HandleFunc("/de", page(strings.Repeat("Leben. ", 1000)+"\n== Einzelnachweise ==\nFußnote."))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	f := NewFetcher(srv.Client())
	f.SPARQLURL = srv.URL + "/sparql"
	f.WikidataAPI = func() string { return srv.URL + "/wd" }
	f.WikiAPI = func(lang string) string { return srv.URL + "/" + lang }

	src, err := f.Fetch(context.Background(), "Q905")
	if err != nil {
		t.Fatal(err)
	}
	if src.OrigLang != "de" || len(src.Facts) != 1 {
		t.Fatalf("orig_lang=%q facts=%d (языки в факты не идут)", src.OrigLang, len(src.Facts))
	}
	var langs []string
	for _, a := range src.Articles {
		langs = append(langs, a.Lang)
	}
	if strings.Join(langs, ",") != "ru,en,de" {
		t.Fatalf("статьи: %v", langs)
	}
	if strings.Contains(src.Articles[2].Text, "Fußnote") || !strings.HasPrefix(src.Articles[2].URL, "https://de.wikipedia.org/") {
		t.Fatalf("de: %+v", src.Articles[2].URL)
	}

	// Короткая статья на языке оригинала ничего не добавит — не берётся.
	f.WikiAPI = func(lang string) string {
		if lang == "de" {
			return srv.URL + "/de-short"
		}
		return srv.URL + "/" + lang
	}
	mux.HandleFunc("/de-short", page("Kurz."))
	if src, err = f.Fetch(context.Background(), "Q905"); err != nil || len(src.Articles) != 2 {
		t.Fatalf("короткая de: err=%v статей=%d", err, len(src.Articles))
	}
}

func TestFetchRetriesTransientErrors(t *testing.T) {
	calls := 0
	failAlways := false
	mux := http.NewServeMux()
	mux.HandleFunc("/sparql", func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch {
		case failAlways || calls == 1:
			w.WriteHeader(http.StatusBadGateway) // разовый сбой Wikidata
			return
		case calls == 2:
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": map[string]any{"bindings": []map[string]map[string]string{
			{"prop": {"value": "P569"}, "date": {"value": "1821-11-11T00:00:00Z"}},
		}}})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	f := NewFetcher(srv.Client())
	f.SPARQLURL = srv.URL + "/sparql"
	f.RetryWait = time.Millisecond

	src := &Sources{QID: "Q991"}
	if err := f.fetchFacts(context.Background(), src); err != nil || src.Born != "1821-11-11" || calls != 3 {
		t.Fatalf("err=%v born=%q calls=%d", err, src.Born, calls)
	}
	// Постоянный сбой — после Retries повторов ошибка со статусом.
	calls, failAlways, f.Retries = 0, true, 1
	if err := f.fetchFacts(context.Background(), &Sources{QID: "Q991"}); err == nil || !strings.Contains(err.Error(), "502") || calls != 2 {
		t.Fatalf("ожидалась ошибка 502 после 2 попыток: err=%v calls=%d", err, calls)
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
