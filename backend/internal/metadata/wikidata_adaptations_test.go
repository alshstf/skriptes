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

// wdMockServer — общий httptest-server, имитирующий два endpoint'а
// Wikidata: action API (wbsearchentities) и SPARQL endpoint.
//
// searchHits — что вернётся wbsearchentities (массив QID'ов).
// authorLabels — что отдаст SPARQL "SELECT ?authorLabel WHERE { wd:QID wdt:P50 ... }".
// adaptations — что отдаст SPARQL "?film wdt:P144 wd:QID" (список SPARQL-bindings).
type wdMockArgs struct {
	searchHits   []string                       // QID'ы кандидатов
	authorLabels map[string][]string            // QID → []label (для validateBookQID)
	adaptations  map[string][]map[string]string // QID → []row (поля film, filmLabel, year, directorLabel, imdbId, image, kindLabel)
}

func wdMockServer(t *testing.T, args wdMockArgs) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/w/api.php"):
			// wbsearchentities
			items := make([]map[string]string, 0, len(args.searchHits))
			for _, id := range args.searchHits {
				items = append(items, map[string]string{"id": id})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"search": items})
		case strings.HasSuffix(r.URL.Path, "/sparql"):
			if err := r.ParseForm(); err != nil {
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}
			q := r.Form.Get("query")
			// Определяем тип запроса по наличию маркеров.
			switch {
			case strings.Contains(q, "wdt:P50"):
				// validateBookQID — найдём QID в запросе.
				qid := extractFirstQID(q)
				labels := args.authorLabels[qid]
				_ = json.NewEncoder(w).Encode(buildAuthorLabelsResponse(labels))
			case strings.Contains(q, "wdt:P144"):
				qid := extractFirstQID(q)
				rows := args.adaptations[qid]
				_ = json.NewEncoder(w).Encode(buildAdaptationsResponse(rows))
			default:
				http.Error(w, "unknown sparql", http.StatusBadRequest)
			}
		case strings.HasPrefix(r.URL.Path, "/commons/"):
			// Не используется в этих тестах — Enricher отдельно тестируется.
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
}

// extractFirstQID — выдёргивает "QN" из SPARQL-строки. Используется
// в моке: запрос валидации/экранизаций встраивает QID как "wd:QXXXX".
func extractFirstQID(q string) string {
	i := strings.Index(q, "wd:Q")
	if i < 0 {
		return ""
	}
	rest := q[i+3:] // после "wd:"
	end := 0
	for end < len(rest) {
		c := rest[end]
		if c == 'Q' || (c >= '0' && c <= '9') {
			end++
		} else {
			break
		}
	}
	return rest[:end]
}

func buildAuthorLabelsResponse(labels []string) map[string]any {
	bindings := make([]map[string]map[string]string, 0, len(labels))
	for _, l := range labels {
		bindings = append(bindings, map[string]map[string]string{
			"authorLabel": {"value": l},
		})
	}
	return map[string]any{"results": map[string]any{"bindings": bindings}}
}

func buildAdaptationsResponse(rows []map[string]string) map[string]any {
	bindings := make([]map[string]map[string]string, 0, len(rows))
	for _, row := range rows {
		entry := map[string]map[string]string{}
		for k, v := range row {
			if v != "" {
				entry[k] = map[string]string{"value": v}
			}
		}
		bindings = append(bindings, entry)
	}
	return map[string]any{"results": map[string]any{"bindings": bindings}}
}

func TestWikidataAdaptations_HappyPath(t *testing.T) {
	// "Война и мир" → Q161531 (книга), две экранизации.
	srv := wdMockServer(t, wdMockArgs{
		searchHits: []string{"Q161531"},
		authorLabels: map[string][]string{
			"Q161531": {"Лев Толстой", "Leo Tolstoy"},
		},
		adaptations: map[string][]map[string]string{
			"Q161531": {
				{
					"film":          "http://www.wikidata.org/entity/Q12345",
					"filmLabel":     "War and Peace",
					"year":          "1956",
					"directorLabel": "King Vidor",
					"imdbId":        "tt0049934",
					"image":         "http://commons.wikimedia.org/wiki/Special:FilePath/Poster.jpg",
					"kindLabel":     "film",
				},
				{
					"film":      "http://www.wikidata.org/entity/Q67890",
					"filmLabel": "Война и мир",
					"year":      "1965",
					"kindLabel": "film",
				},
			},
		},
	})
	defer srv.Close()

	p := NewWikidataAdaptationsProvider(nil).WithEndpoints(srv.URL+"/w/api.php", srv.URL+"/sparql", "")

	got, err := p.FetchAdaptations(context.Background(), BookQuery{
		Title:   "Война и мир",
		Authors: []string{"Толстой, Лев Николаевич"},
	})
	require.NoError(t, err)
	require.Len(t, got, 2)

	require.Equal(t, "Q12345", got[0].ExtID)
	require.Equal(t, "War and Peace", got[0].Title)
	require.NotNil(t, got[0].Year)
	require.Equal(t, 1956, *got[0].Year)
	require.Equal(t, "King Vidor", got[0].Director)
	require.Equal(t, "film", got[0].Kind)
	require.Contains(t, got[0].PosterURL, "Poster.jpg")
	require.Equal(t, "https://www.wikidata.org/wiki/Q12345", got[0].ExtURL)

	require.Equal(t, "Q67890", got[1].ExtID)
	require.NotNil(t, got[1].Year)
	require.Equal(t, 1965, *got[1].Year)
}

func TestWikidataAdaptations_AuthorMismatchSkipsCandidate(t *testing.T) {
	// Два кандидата на один title: первый — книга другого автора, второй — наш.
	srv := wdMockServer(t, wdMockArgs{
		searchHits: []string{"Q11111", "Q22222"},
		authorLabels: map[string][]string{
			"Q11111": {"Charles Dickens"},        // не наш автор
			"Q22222": {"Лев Николаевич Толстой"}, // наш автор
		},
		adaptations: map[string][]map[string]string{
			"Q22222": {
				{
					"film":      "http://www.wikidata.org/entity/Q999",
					"filmLabel": "Анна Каренина (1997)",
					"year":      "1997",
				},
			},
		},
	})
	defer srv.Close()

	p := NewWikidataAdaptationsProvider(nil).WithEndpoints(srv.URL+"/w/api.php", srv.URL+"/sparql", "")

	got, err := p.FetchAdaptations(context.Background(), BookQuery{
		Title:   "Анна Каренина",
		Authors: []string{"Толстой Лев"},
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "Q999", got[0].ExtID)
}

func TestWikidataAdaptations_NoBookQIDFound(t *testing.T) {
	srv := wdMockServer(t, wdMockArgs{
		searchHits: []string{}, // ничего не нашли в wbsearchentities
	})
	defer srv.Close()
	p := NewWikidataAdaptationsProvider(nil).WithEndpoints(srv.URL+"/w/api.php", srv.URL+"/sparql", "")

	_, err := p.FetchAdaptations(context.Background(), BookQuery{
		Title:   "Какая-то выдуманная книга",
		Authors: []string{"Никто Никто"},
	})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestWikidataAdaptations_BookFoundButNoAdaptations(t *testing.T) {
	srv := wdMockServer(t, wdMockArgs{
		searchHits: []string{"Q12345"},
		authorLabels: map[string][]string{
			"Q12345": {"Иван Бунин"},
		},
		adaptations: map[string][]map[string]string{
			"Q12345": {}, // ничего не сняли
		},
	})
	defer srv.Close()
	p := NewWikidataAdaptationsProvider(nil).WithEndpoints(srv.URL+"/w/api.php", srv.URL+"/sparql", "")

	got, err := p.FetchAdaptations(context.Background(), BookQuery{
		Title:   "Жизнь Арсеньева",
		Authors: []string{"Бунин Иван Алексеевич"},
	})
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestWikidataAdaptations_DedupesCartesianProduct(t *testing.T) {
	// SPARQL OPTIONAL'ы дают декартово произведение: один фильм с двумя
	// директорами и двумя kind-метками → 4 строки. Должны схлопнуться
	// в одну запись с director="A, B" и kind из mapWikidataKind.
	srv := wdMockServer(t, wdMockArgs{
		searchHits: []string{"Q100"},
		authorLabels: map[string][]string{
			"Q100": {"Достоевский Фёдор Михайлович"},
		},
		adaptations: map[string][]map[string]string{
			"Q100": {
				{"film": "http://www.wikidata.org/entity/Q9", "filmLabel": "Идиот", "year": "1958", "directorLabel": "Иван Пырьев", "kindLabel": "film"},
				{"film": "http://www.wikidata.org/entity/Q9", "filmLabel": "Идиот", "year": "1958", "directorLabel": "Иван Пырьев", "kindLabel": "feature film"},
				{"film": "http://www.wikidata.org/entity/Q9", "filmLabel": "Идиот", "year": "1958", "directorLabel": "А. Иванов", "kindLabel": "film"},
				{"film": "http://www.wikidata.org/entity/Q9", "filmLabel": "Идиот", "year": "1958", "directorLabel": "А. Иванов", "kindLabel": "feature film"},
			},
		},
	})
	defer srv.Close()
	p := NewWikidataAdaptationsProvider(nil).WithEndpoints(srv.URL+"/w/api.php", srv.URL+"/sparql", "")

	got, err := p.FetchAdaptations(context.Background(), BookQuery{
		Title:   "Идиот",
		Authors: []string{"Достоевский Фёдор"},
	})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "Иван Пырьев, А. Иванов", got[0].Director)
	require.Equal(t, "film", got[0].Kind)
}

func TestMapWikidataKind(t *testing.T) {
	cases := map[string]string{
		"":                  "film",
		"film":              "film",
		"feature film":      "film",
		"фильм":             "film",
		"television series": "tv_series",
		"телесериал":        "tv_series",
		"телевизионный сериал": "tv_series",
		"miniseries":  "miniseries",
		"мини-сериал": "miniseries",
		"anime":       "anime",
		"аниме":       "anime",
		"video game":  "other",
	}
	for input, want := range cases {
		got := mapWikidataKind(input)
		require.Equalf(t, want, got, "input=%q", input)
	}
}

func TestMatchAuthor(t *testing.T) {
	// Проверяем что разные представления имени совпадают.
	require.True(t, matchAuthorAny("Толстой, Лев Николаевич", []string{"Лев Николаевич Толстой"}))
	require.True(t, matchAuthorAny("Толстой Лев Николаевич", []string{"Leo Tolstoy", "Лев Николаевич Толстой"}))
	require.True(t, matchAuthorAny("Tolstoy, Leo", []string{"Leo Tolstoy"}))
	// Несовпадающий автор отвергается.
	require.False(t, matchAuthorAny("Толстой Лев", []string{"Чарльз Диккенс"}))
	// Не валится на пустом name.
	require.False(t, matchAuthorAny("", []string{"X"}))
}

func TestExtractQID(t *testing.T) {
	require.Equal(t, "Q161531", extractQID("http://www.wikidata.org/entity/Q161531"))
	require.Equal(t, "Q1", extractQID("https://www.wikidata.org/entity/Q1"))
	require.Equal(t, "", extractQID("not-a-wikidata-uri"))
}
