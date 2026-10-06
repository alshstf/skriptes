package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// wikiWorksServer — Википедия для строгого пути по работам: opensearch → titles,
// полнотекстовый поиск ничего не находит, pageprops → QID статьи.
func wikiWorksServer(t *testing.T, titles []string, qids map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qv := r.URL.Query()
		switch {
		case qv.Get("action") == "opensearch":
			_ = json.NewEncoder(w).Encode([]any{qv.Get("search"), titles, []string{}, []string{}})
		case qv.Get("list") == "search":
			_ = json.NewEncoder(w).Encode(map[string]any{"query": map[string]any{"search": []any{}}})
		case qv.Get("ppprop") == "wikibase_item":
			page := map[string]any{"title": qv.Get("titles")}
			if q := qids[qv.Get("titles")]; q != "" {
				page["pageprops"] = map[string]string{"wikibase_item": q}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"query": map[string]any{"pages": []any{page}}})
		case qv.Get("ppprop") == "disambiguation":
			_ = json.NewEncoder(w).Encode(map[string]any{"query": map[string]any{"pages": []any{
				map[string]any{"title": qv.Get("titles"), "missing": true}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestWikipedia_StrictByWorks — #410: тёзка подтверждается книгой среди его работ
// в Wikidata (P50), когда полнотекстовый поиск находит статью о книге, а не о
// человеке («Книга Мормона»), или переведённого названия в статье нет («Бабочкин
// язычок» у Риваса — совпадает оригинал «Todo es silencio»).
func TestWikipedia_StrictByWorks(t *testing.T) {
	ctx := context.Background()
	facts := func(works map[string][]string) CandidateFactsFunc {
		return func(_ context.Context, qid string) (CandidateFacts, error) {
			return CandidateFacts{QID: qid, Human: true, Works: works[qid]}, nil
		}
	}
	smith := AuthorQuery{LastName: "Смит", FirstName: "Джозеф", FullName: "Смит Джозеф", Namesakes: true,
		BookTitles: []string{"Книга Мормона", "Учения и заветы"}}
	titles := []string{"Смит, Джозеф", "Смит, Джозеф Линдон", "Смит, Джозеф (коллекционер)"}
	qids := map[string]string{"Смит, Джозеф": "Q1", "Смит, Джозеф Линдон": "Q2", "Смит, Джозеф (коллекционер)": "Q3"}

	t.Run("книга у одного кандидата — он", func(t *testing.T) {
		srv := wikiWorksServer(t, titles, qids)
		p := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL).
			WithCandidateFacts(facts(map[string][]string{"Q1": {"Книга Мормона"}, "Q2": {"Физика плазмы"}}))
		got, kind, err := p.resolveStrictTitle(ctx, "ru", smith)
		require.NoError(t, err)
		require.Equal(t, "Смит, Джозеф", got)
		require.Equal(t, MatchConfirmed, kind)
	})

	t.Run("книга у двух кандидатов — не угадываем", func(t *testing.T) {
		srv := wikiWorksServer(t, titles, qids)
		p := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL).
			WithCandidateFacts(facts(map[string][]string{"Q1": {"Книга Мормона"}, "Q3": {"Книга Мормона"}}))
		_, _, err := p.resolveStrictTitle(ctx, "ru", smith)
		require.True(t, errors.Is(err, ErrNotFound))
	})

	t.Run("без фактов — как раньше", func(t *testing.T) {
		srv := wikiWorksServer(t, titles, qids)
		p := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL)
		_, _, err := p.resolveStrictTitle(ctx, "ru", smith)
		require.True(t, errors.Is(err, ErrNotFound))
	})

	t.Run("оригинальное название работы", func(t *testing.T) {
		srv := wikiWorksServer(t, []string{"Ривас, Мануэль", "Ривас, Эмануэль Бенито"},
			map[string]string{"Ривас, Мануэль": "Q10", "Ривас, Эмануэль Бенито": "Q11"})
		p := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL).
			WithCandidateFacts(facts(map[string][]string{"Q10": {"Всё это молчание", "Todo es silencio"}}))
		got, kind, err := p.resolveStrictTitle(ctx, "ru", AuthorQuery{
			LastName: "Ривас", FirstName: "Мануэль", FullName: "Ривас Мануэль", Namesakes: true,
			BookTitles: []string{"Todo es silencio", "Карандаш плотника", "Бабочкин язычок"},
		})
		require.NoError(t, err)
		require.Equal(t, "Ривас, Мануэль", got)
		require.Equal(t, MatchConfirmed, kind)
	})

	t.Run("не человек — не кандидат", func(t *testing.T) {
		srv := wikiWorksServer(t, []string{"Руссо, Джон"}, map[string]string{"Руссо, Джон": "Q20"})
		p := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL).
			WithCandidateFacts(func(_ context.Context, qid string) (CandidateFacts, error) {
				return CandidateFacts{QID: qid, Human: false, Works: []string{"Ночь живых мертвецов"}}, nil
			})
		_, _, err := p.resolveStrictTitle(ctx, "ru", AuthorQuery{LastName: "Руссо", FirstName: "Джон",
			FullName: "Руссо Джон", Namesakes: true, BookTitles: []string{"Ночь живых мертвецов"}})
		require.True(t, errors.Is(err, ErrNotFound), "страница-неоднозначность — не автор")
	})
}

// TestFirstNameEquivalents — #259: устойчивая русская передача западного имени.
func TestFirstNameEquivalents(t *testing.T) {
	gibson := AuthorQuery{LastName: "Гибсон", FirstName: "Уильям"}
	require.True(t, authorNameMatches(gibson, "William Gibson"))
	require.True(t, authorNameMatches(gibson, "Гибсон, Уильям"))
	require.False(t, authorNameMatches(gibson, "Mel Gibson"), "фамилия есть, имя — другое")
	require.False(t, authorNameMatches(AuthorQuery{LastName: "Кинг", FirstName: "Стивен"}, "Stephen Fry"),
		"имя совпало, фамилия — нет")
	require.True(t, authorNameMatches(AuthorQuery{LastName: "Кинг", FirstName: "Стивен"}, "Stephen King"))
	require.True(t, authorNameMatches(AuthorQuery{LastName: "Кинг", FirstName: "Стивен"}, "Steven King"))

	// Обратно: запрос латиницей, кандидат кириллицей (издания OpenLibrary на русском).
	require.True(t, authorNameMatches(AuthorQuery{LastName: "gibson", FirstName: "william"}, "Уильям Гибсон"))
	require.False(t, authorNameMatches(AuthorQuery{LastName: "gibson", FirstName: "william"}, "Мел Гибсон"))

	l, ok := gibson.latinQuery()
	require.True(t, ok, "латинского имени нет — из словаря и транслита")
	require.Equal(t, "gibson", l.LastName)
	require.Equal(t, "william", l.FirstName)
	_, ok = AuthorQuery{LastName: "Толстой", FirstName: "Лев"}.latinQuery()
	require.False(t, ok, "имени нет в словаре — латинского запроса нет")
	l, ok = AuthorQuery{LastName: "Гибсон", FirstName: "Уильям", LatinName: "gibson william ford"}.latinQuery()
	require.True(t, ok)
	require.Equal(t, "ford", l.MiddleName, "латинское имя из переводов важнее словаря")
}

// TestOpenLibrary_GuessedLatinNeedsBook — латинское имя угадано по словарю
// (#259): OpenLibrary берёт автора только по книге. Приёмка 2026-10-07: по
// «John Holm» нашёлся драматург John Cecil Holm, а «Холм Джон» в каталоге —
// псевдоним соавтора Гаррисона («Молот и Крест»).
func TestOpenLibrary_GuessedLatinNeedsBook(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/search/authors.json":
			_ = json.NewEncoder(w).Encode(map[string]any{"docs": []any{map[string]any{"key": "OL9A", "name": "John Holm"}}})
		case r.URL.Path == "/search.json" && r.URL.Query().Get("title") == "The Hammer and the Cross":
			_ = json.NewEncoder(w).Encode(map[string]any{"docs": []any{
				map[string]any{"author_key": []string{"OL1A", "OL2A"}, "author_name": []string{"Harry Harrison", "John Holm"}},
			}})
		case r.URL.Path == "/search.json":
			_ = json.NewEncoder(w).Encode(map[string]any{"docs": []any{}})
		case strings.HasPrefix(r.URL.Path, "/authors/OL9A"):
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "John Holm", "bio": "American dramatist"})
		case strings.HasPrefix(r.URL.Path, "/authors/OL2A"):
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "John Holm", "bio": "pseudonym"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p := NewOpenLibraryProvider(srv.Client()).WithEndpoints(srv.URL+"/search.json", srv.URL)

	q := AuthorQuery{LastName: "Холм", FirstName: "Джон", FullName: "Холм Джон",
		BookTitles: []string{"Молот и Крест", "The Hammer and the Cross"}}
	l, ok := q.latinQuery()
	require.True(t, ok)
	require.True(t, l.LatinGuessed, "имени из переводов нет — угадано")
	a, err := p.authorSearch(context.Background(), q)
	require.NoError(t, err)
	require.Equal(t, "OL2A", a.OLID, "автор нашей книги, а не первый John Holm по имени")

	q.BookTitles = []string{"Нет такой"}
	_, err = p.authorSearch(context.Background(), q)
	require.True(t, errors.Is(err, ErrNotFound), "книгой не подтвердился — не найдено")

	withLatin := AuthorQuery{LastName: "Холм", FirstName: "Джон", FullName: "Холм Джон", LatinName: "holm john"}
	l, ok = withLatin.latinQuery()
	require.True(t, ok)
	require.False(t, l.LatinGuessed, "имя из переводов — не угадано")
}
