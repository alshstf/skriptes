package metadata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// wdMockServer — общий httptest-server, имитирующий два endpoint'а
// Wikidata: action API (wbsearchentities) и SPARQL endpoint.
//
// searchHits — что вернётся wbsearchentities (массив QID'ов).
// authorLabels — метки автора (P50) книги в ответе проверки кандидатов
// "VALUES ?book { wd:Q1 wd:Q2 } ?book wdt:P50 ?author"; authorQIDs — QID этого
// автора (без него — синтетический Q900…, чужой любому QID из каталога).
// adaptations — что отдаст SPARQL "?film wdt:P144 wd:QID" (список SPARQL-bindings).
type wdMockArgs struct {
	searchHits   []string                       // QID'ы кандидатов
	authorLabels map[string][]string            // QID книги → []label автора (для firstBookByAuthor)
	authorQIDs   map[string]string              // QID книги → QID автора
	nonBook      map[string]bool                // QID кандидата → его класс P31 — фильм/игра/альбом (ответ ?nonBook)
	editionOf    map[string][]string            // QID кандидата-издания → его произведения (P629, ответ ?work)
	adaptations  map[string][]map[string]string // QID → []row (поля film, filmLabel, year, directorLabel, imdbId, image, kindLabel)
	p50Calls     *atomic.Int32                  // сколько было запросов проверки кандидатов
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
				// firstBookByAuthor — все кандидаты одним запросом (VALUES ?book).
				if args.p50Calls != nil {
					args.p50Calls.Add(1)
				}
				_ = json.NewEncoder(w).Encode(buildBookAuthorsResponse(args, extractAllQIDs(q)))
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

// extractAllQIDs — все "wd:QN" из SPARQL-строки (кандидаты в VALUES).
func extractAllQIDs(q string) []string {
	var out []string
	for {
		qid := extractFirstQID(q)
		if qid == "" {
			return out
		}
		out = append(out, qid)
		q = q[strings.Index(q, "wd:"+qid)+3+len(qid):]
	}
}

// buildBookAuthorsResponse — ответ проверки кандидатов: строка (книга, автор,
// метка) на каждую метку; автор без меток — одна строка без метки; книга без
// автора (фильм, ландшафт) — ни одной.
func buildBookAuthorsResponse(args wdMockArgs, books []string) map[string]any {
	var bindings []map[string]map[string]string
	for _, book := range books {
		author, hasAuthor := args.authorQIDs[book]
		labels := args.authorLabels[book]
		if !hasAuthor && len(labels) == 0 {
			continue
		}
		if author == "" {
			author = "Q900" + book[1:]
		}
		row := func(label string) map[string]map[string]string {
			r := map[string]map[string]string{
				"book":    {"value": "http://www.wikidata.org/entity/" + book},
				"author":  {"value": "http://www.wikidata.org/entity/" + author},
				"nonBook": {"value": strconv.FormatBool(args.nonBook[book])},
			}
			if label != "" {
				r["authorLabel"] = map[string]string{"value": label}
			}
			return r
		}
		var rows []map[string]map[string]string
		if len(labels) == 0 {
			rows = append(rows, row(""))
		}
		for _, l := range labels {
			rows = append(rows, row(l))
		}
		// P629 — OPTIONAL: у издания строки множатся на каждое произведение.
		works := args.editionOf[book]
		if len(works) == 0 {
			bindings = append(bindings, rows...)
			continue
		}
		for _, w := range works {
			for _, r := range rows {
				withWork := map[string]map[string]string{"work": {"value": "http://www.wikidata.org/entity/" + w}}
				for k, v := range r {
					withWork[k] = v
				}
				bindings = append(bindings, withWork)
			}
		}
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
					"kinopoiskId":   "44567",
					"image":         "http://commons.wikimedia.org/wiki/Special:FilePath/Poster.jpg",
					"kindLabel":     "film",
					"sitelinks":     "47",
					"tmdbMovie":     "4584",
				},
				{
					"film":      "http://www.wikidata.org/entity/Q67890",
					"filmLabel": "Война и мир",
					"year":      "1965",
					"kindLabel": "film",
					"sitelinks": "82",
					"tmdbTv":    "13892",
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
	// Кинопоиск приоритетнее IMDb (см. pickExtURL).
	require.Equal(t, "https://www.kinopoisk.ru/film/44567/", got[0].ExtURL)
	require.Equal(t, 47, got[0].Popularity)
	// TMDB-id (P4947/P4983) пробрасываются в Adaptation — источник постеров.
	require.Equal(t, "4584", got[0].TMDBMovieID)
	require.Empty(t, got[0].TMDBTVID)

	require.Equal(t, "Q67890", got[1].ExtID)
	require.NotNil(t, got[1].Year)
	require.Equal(t, 1965, *got[1].Year)
	// Нет ни Кинопоиска, ни IMDb → fallback на Wikidata.
	require.Equal(t, "https://www.wikidata.org/wiki/Q67890", got[1].ExtURL)
	require.Equal(t, 82, got[1].Popularity)
	require.Equal(t, "13892", got[1].TMDBTVID)
}

func TestPickExtURL(t *testing.T) {
	// Кинопоиск > IMDb > Wikidata.
	require.Equal(t, "https://www.kinopoisk.ru/film/42664/",
		pickExtURL("42664", "tt0123456", "Q1"))
	require.Equal(t, "https://www.imdb.com/title/tt0123456/",
		pickExtURL("", "tt0123456", "Q1"))
	require.Equal(t, "https://www.wikidata.org/wiki/Q1",
		pickExtURL("", "", "Q1"))
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
		// #295: сериалы и телеспектакли под другими метками — экранизации,
		// оперы, песни, настольные игры — нет.
		"animated series":         "tv_series",
		"мультсериал":             "tv_series",
		"web series":              "tv_series",
		"television film":         "film",
		"телеспектакль":           "film",
		"мультфильм":              "film",
		"opera":                   "other",
		"song":                    "other",
		"board game":              "other",
		"literary work":           "other",
		"anime television series": "anime",
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

// #570: другая передача имени в каталоге и в Wikidata — тот же человек; одна
// совпавшая фамилия (или двойное имя, частица) — нет.
func TestMatchAuthor_Transliteration(t *testing.T) {
	herbert := []string{"Frank Herbert", "Фрэнк Герберт", "Герберт, Фрэнк"}
	require.True(t, matchAuthorAny("Херберт Фрэнк", herbert[1:2]), "Х/Г в одном алфавите")
	require.True(t, matchAuthorAny("Херберт Фрэнк", herbert[:1]), "кириллица против латиницы, Фрэнк ≈ Frank")
	require.True(t, matchAuthorAny("Герберт Франк", herbert[:1]), "Франк ≈ Frank между алфавитами")
	require.False(t, matchAuthorAny("Херберт Брайан", herbert), "сын — та же фамилия, другое имя")
	require.False(t, matchAuthorAny("Херберт Фрэнк", []string{"Brian Herbert", "Брайан Герберт"}))

	require.True(t, matchAuthorAny("Азимов Айзек", []string{"Isaac Asimov"}), "з/с и русская передача имени")
	require.True(t, matchAuthorAny("Гаррисон Гарри", []string{"Harry Harrison"}))
	require.True(t, matchAuthorAny("Толкиен Джон Рональд Руэл", []string{"J. R. R. Tolkien", "Джон Рональд Руэл Толкин"}))
	require.True(t, matchAuthorAny("Брэдбери Рэй", []string{"Рэй Брэдбери"}), "э/е транслит сводит сам")
	require.True(t, matchAuthorAny("Семёнов Юлиан", []string{"Юлиан Семенов"}), "ё/е")

	require.True(t, matchAuthorAny("Ле Гуин Урсула", []string{"Урсула Ле Гуин"}))
	require.False(t, matchAuthorAny("Ван Вогт Альфред", []string{"Э. ван Вогт"}), "частица — не второе совпадение")
	require.True(t, matchAuthorAny("Гранже Жан Кристоф", []string{"Жан-Кристоф Гранже"}), "двойное имя без дефиса")
	require.False(t, matchAuthorAny("Руфен Жан-Кристоф", []string{"Жан-Кристоф Гранже"}), "двойное имя — одно слово")
	require.False(t, matchAuthorAny("Иванов П.", []string{"Иван Иванов"}), "одна фамилия и чужой инициал")
	require.True(t, matchAuthorAny("Иванов И.", []string{"Иван Иванов"}), "инициал совпал с именем")
	require.False(t, matchAuthorAny("Иванов И.", []string{"Пётр Иванов"}), "инициал — не первая буква самой фамилии")
	require.True(t, matchAuthorAny("Гомер", []string{"Гомер"}), "имя из одного слова — как раньше")
}

// authorsConfirm: известный QID автора — единственный критерий для него; без
// QID — метки любого автора книги.
func TestAuthorsConfirm(t *testing.T) {
	dune := map[string][]string{"Q7934": {"Frank Herbert", "Фрэнк Герберт"}}
	require.True(t, authorsConfirm([]string{"Херберт Фрэнк"}, []string{"Q7934"}, dune))
	require.True(t, authorsConfirm([]string{"Совсем Другое Имя"}, []string{"Q7934"}, dune), "QID важнее букв")
	require.False(t, authorsConfirm([]string{"Герберт Фрэнк"}, []string{"Q1"}, dune), "QID известен и не совпал — метки тёзки не в счёт")
	require.True(t, authorsConfirm([]string{"Херберт Фрэнк"}, nil, dune), "без QID — по меткам")
	require.True(t, authorsConfirm([]string{"Херберт Фрэнк"}, []string{""}, dune))
	require.False(t, authorsConfirm([]string{"Херберт Фрэнк"}, []string{"Q7934"}, nil), "у кандидата нет P50")

	// Соавторы: у первого QID чужой, второй без QID — сверяется по меткам.
	prequel := map[string][]string{"Q5": {"Kevin J. Anderson", "Кевин Андерсон"}, "Q6": {"Brian Herbert"}}
	require.True(t, authorsConfirm([]string{"Херберт Брайан", "Андерсон Кевин"}, []string{"Q123", ""}, prequel))
	require.False(t, authorsConfirm([]string{"Херберт Брайан", "Андерсон Кевин"}, []string{"Q123", "Q456"}, prequel))
}

// #570: «Дюна» Херберта — кандидат подтверждается QID автора, хотя по буквам имя
// не сходится; все кандидаты одним запросом.
func TestWikidataAdaptations_AuthorQIDConfirmsBook(t *testing.T) {
	var calls atomic.Int32
	srv := wdMockServer(t, wdMockArgs{
		searchHits: []string{"Q25391", "Q190192", "Q60834962"}, // дюна (ландшафт), роман, фильм 2021
		authorLabels: map[string][]string{
			"Q190192": {"Frank Herbert", "Фрэнк Герберт"},
		},
		authorQIDs: map[string]string{"Q190192": "Q7934"},
		adaptations: map[string][]map[string]string{
			"Q190192": {
				{"film": "http://www.wikidata.org/entity/Q114819", "filmLabel": "Дюна", "year": "1984", "kindLabel": "film"},
				{"film": "http://www.wikidata.org/entity/Q60834962", "filmLabel": "Дюна", "year": "2021", "kindLabel": "film"},
			},
		},
		p50Calls: &calls,
	})
	defer srv.Close()
	p := NewWikidataAdaptationsProvider(nil).WithEndpoints(srv.URL+"/w/api.php", srv.URL+"/sparql", "")

	got, qid, err := p.FetchAdaptationsWithQID(context.Background(), BookQuery{
		Title: "Дюна", Authors: []string{"Херберт Фрэнк"}, AuthorQIDs: []string{"Q7934"},
	})
	require.NoError(t, err)
	require.Equal(t, "Q190192", qid)
	require.Len(t, got, 2)
	require.EqualValues(t, 1, calls.Load(), "три кандидата — один запрос проверки")
}

// Тёзка: метки автора кандидата совпадают с именем буква в букву, но QID нашего
// автора известен и другой — книга чужая. Отвергнутые кандидаты при поиске на
// втором языке не перепроверяются.
func TestWikidataAdaptations_AuthorQIDRejectsNamesake(t *testing.T) {
	var calls atomic.Int32
	srv := wdMockServer(t, wdMockArgs{
		searchHits:   []string{"Q11111"},
		authorLabels: map[string][]string{"Q11111": {"Иван Иванов"}},
		authorQIDs:   map[string]string{"Q11111": "Q2"},
		adaptations:  map[string][]map[string]string{"Q11111": {{"film": "http://www.wikidata.org/entity/Q9", "filmLabel": "Фильм"}}},
		p50Calls:     &calls,
	})
	defer srv.Close()
	p := NewWikidataAdaptationsProvider(nil).WithEndpoints(srv.URL+"/w/api.php", srv.URL+"/sparql", "")

	_, err := p.FetchAdaptations(context.Background(), BookQuery{
		Title: "Рассказы", Authors: []string{"Иванов Иван"}, AuthorQIDs: []string{"Q1"},
	})
	require.ErrorIs(t, err, ErrNotFound)
	require.EqualValues(t, 1, calls.Load(), "ru и en нашли того же кандидата — проверен один раз")

	// Без QID автора — по меткам, как раньше: совпали имя и фамилия.
	_, err = p.FetchAdaptations(context.Background(), BookQuery{Title: "Рассказы", Authors: []string{"Иванов Иван"}})
	require.NoError(t, err)
}

func TestExtractQID(t *testing.T) {
	require.Equal(t, "Q161531", extractQID("http://www.wikidata.org/entity/Q161531"))
	require.Equal(t, "Q1", extractQID("https://www.wikidata.org/entity/Q1"))
	require.Equal(t, "", extractQID("not-a-wikidata-uri"))
}

// Сбой проверки кандидата (429 от SPARQL под троттлингом) — не «не найдено»:
// иначе lookups писали not_found на 90 дней, а экранизации — «нет» навсегда (#281).
func TestWikidataAdaptations_ThrottledValidationIsUpstream(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sparql" {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"search":[{"id":"Q11111"}]}`))
	}))
	defer srv.Close()
	p := NewWikidataAdaptationsProvider(nil).WithEndpoints(srv.URL+"/w/api.php", srv.URL+"/sparql", "")

	_, err := p.FetchAdaptations(context.Background(), BookQuery{
		Title:   "Анна Каренина",
		Authors: []string{"Толстой Лев"},
	})
	require.ErrorIs(t, err, ErrUpstream)
	require.NotErrorIs(t, err, ErrNotFound)
}

func TestScreenKind(t *testing.T) {
	require.Equal(t, "film", screenKind(nil), "без меток — как раньше, фильм")
	require.Equal(t, "tv_series", screenKind([]string{"work of art", "television series"}), "экранная метка не первой")
	require.Equal(t, "anime", screenKind([]string{"television series", "anime television series"}))
	require.Equal(t, "miniseries", screenKind([]string{"television series", "miniseries"}))
	require.Equal(t, kindOther, screenKind([]string{"video game", "opera"}))
}

func TestWikidataAdaptations_DropsNonScreenAndUntitled(t *testing.T) {
	srv := wdMockServer(t, wdMockArgs{
		searchHits:   []string{"Q100"},
		authorLabels: map[string][]string{"Q100": {"Толкин Джон"}},
		adaptations: map[string][]map[string]string{
			"Q100": {
				{"film": "http://www.wikidata.org/entity/Q1", "filmLabel": "Хоббит", "kindLabel": "film"},
				{"film": "http://www.wikidata.org/entity/Q2", "filmLabel": "Средиземье: Тень Мордора", "kindLabel": "video game"},
				{"film": "http://www.wikidata.org/entity/Q3", "filmLabel": "Q3", "kindLabel": "film"},
				{"film": "http://www.wikidata.org/entity/Q4", "filmLabel": "Кольца власти", "kindLabel": "work of art"},
				{"film": "http://www.wikidata.org/entity/Q4", "filmLabel": "Кольца власти", "kindLabel": "television series"},
			},
		},
	})
	defer srv.Close()
	p := NewWikidataAdaptationsProvider(nil).WithEndpoints(srv.URL+"/w/api.php", srv.URL+"/sparql", "")
	got, err := p.FetchAdaptations(context.Background(), BookQuery{Title: "Хоббит", Authors: []string{"Толкин Джон"}})
	require.NoError(t, err)
	titles := map[string]string{}
	for _, a := range got {
		titles[a.Title] = a.Kind
	}
	require.Equal(t, map[string]string{"Хоббит": "film", "Кольца власти": "tv_series"}, titles,
		"игра и запись без названия не сохраняются; сериал с меткой «произведение» не первой — сериал")
}

// TestWikidataAdaptations_NonBookCandidateSkipped — кандидат-фильм с P50 автора
// (у «Дюны» 2021 в Wikidata P50 = Фрэнк Герберт, #575) не проходит как книга:
// гейт по классу P31 (nonBook) пропускает его, и подтверждается следующий
// кандидат — роман того же автора; кандидат без P31 проходит по автору.
func TestWikidataAdaptations_NonBookCandidateSkipped(t *testing.T) {
	srv := wdMockServer(t, wdMockArgs{
		searchHits: []string{"Q60834962", "Q190192"}, // фильм первым, роман вторым
		authorQIDs: map[string]string{"Q60834962": "Q7934", "Q190192": "Q7934"},
		authorLabels: map[string][]string{
			"Q60834962": {"Frank Herbert", "Фрэнк Герберт"},
			"Q190192":   {"Frank Herbert", "Фрэнк Герберт"},
		},
		nonBook: map[string]bool{"Q60834962": true},
		adaptations: map[string][]map[string]string{
			"Q190192": {{"film": "http://www.wikidata.org/entity/Q114819", "filmLabel": "Дюна", "year": "1984", "kindLabel": "фильм"}},
		},
	})
	defer srv.Close()
	p := NewWikidataAdaptationsProvider(nil).WithEndpoints(srv.URL+"/w/api.php", srv.URL+"/sparql", "")

	items, qid, err := p.FetchAdaptationsWithQID(context.Background(), BookQuery{
		ID: 1, Title: "Дюна", Authors: []string{"Херберт Фрэнк"}, AuthorQIDs: []string{"Q7934"}, Lang: "ru",
	})
	require.NoError(t, err)
	require.Equal(t, "Q190192", qid, "фильм с P50 автора отвергнут, роман принят")
	require.Len(t, items, 1)

	// Единственный кандидат — фильм: не найдено, не ошибка.
	srv2 := wdMockServer(t, wdMockArgs{
		searchHits:   []string{"Q60834962"},
		authorQIDs:   map[string]string{"Q60834962": "Q7934"},
		authorLabels: map[string][]string{"Q60834962": {"Frank Herbert"}},
		nonBook:      map[string]bool{"Q60834962": true},
	})
	defer srv2.Close()
	p2 := NewWikidataAdaptationsProvider(nil).WithEndpoints(srv2.URL+"/w/api.php", srv2.URL+"/sparql", "")
	_, _, err = p2.FetchAdaptationsWithQID(context.Background(), BookQuery{
		ID: 1, Title: "Дюна", Authors: []string{"Херберт Фрэнк"}, AuthorQIDs: []string{"Q7934"}, Lang: "ru",
	})
	require.ErrorIs(t, err, ErrNotFound)
}

// #574: wbsearchentities ставит элемент-издание («2001 edition of The Godfather»,
// P629 = роман) выше произведения. Подтверждённое по автору издание заменяется
// произведением — экранизации ссылаются на него; издание нескольких
// произведений (омнибус) пропускается.
func TestWikidataAdaptations_EditionCandidateResolvesToWork(t *testing.T) {
	srv := wdMockServer(t, wdMockArgs{
		searchHits: []string{"Q100287822"}, // только издание: произведение в выдаче поиска не всплыло
		authorQIDs: map[string]string{"Q100287822": "Q312720"},
		authorLabels: map[string][]string{
			"Q100287822": {"Mario Puzo"},
		},
		editionOf: map[string][]string{"Q100287822": {"Q1744399"}},
		adaptations: map[string][]map[string]string{
			"Q1744399": {{"film": "http://www.wikidata.org/entity/Q47703", "filmLabel": "Крёстный отец", "year": "1972", "kindLabel": "фильм"}},
		},
	})
	defer srv.Close()
	p := NewWikidataAdaptationsProvider(nil).WithEndpoints(srv.URL+"/w/api.php", srv.URL+"/sparql", "")
	items, qid, err := p.FetchAdaptationsWithQID(context.Background(), BookQuery{
		ID: 1, Title: "The Godfather", Authors: []string{"Пьюзо Марио"}, AuthorQIDs: []string{"Q312720"}, Lang: "en",
	})
	require.NoError(t, err)
	require.Equal(t, "Q1744399", qid, "QID произведения, а не издания")
	require.Len(t, items, 1)

	// Омнибус (издание двух произведений) пропускается, следующий кандидат — роман.
	srv2 := wdMockServer(t, wdMockArgs{
		searchHits:   []string{"Q500", "Q1744399"},
		authorQIDs:   map[string]string{"Q500": "Q312720", "Q1744399": "Q312720"},
		authorLabels: map[string][]string{"Q500": {"Mario Puzo"}, "Q1744399": {"Mario Puzo"}},
		editionOf:    map[string][]string{"Q500": {"Q1744399", "Q501"}},
	})
	defer srv2.Close()
	p2 := NewWikidataAdaptationsProvider(nil).WithEndpoints(srv2.URL+"/w/api.php", srv2.URL+"/sparql", "")
	key, err := p2.ResolveWorkKey(context.Background(), WorkQuery{
		BookID: 1, Title: "The Godfather", Authors: []string{"Пьюзо Марио"}, AuthorQIDs: []string{"Q312720"}, Lang: "en",
	})
	require.NoError(t, err)
	require.Equal(t, "Q1744399", key)
}
