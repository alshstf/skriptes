package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// wikiFormsServer — поиск Википедии, отвечающий по форме запроса (opensearch) и
// полнотекстовым поиском по srsearch (строгий путь).
func wikiFormsServer(t *testing.T, bySearch map[string][]string, hits map[string][]string) (*httptest.Server, *[]string) {
	t.Helper()
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qv := r.URL.Query()
		switch {
		case qv.Get("action") == "opensearch":
			calls = append(calls, "opensearch:"+qv.Get("search"))
			titles := bySearch[qv.Get("search")]
			if titles == nil {
				titles = []string{}
			}
			_ = json.NewEncoder(w).Encode([]any{qv.Get("search"), titles, []string{}, []string{}})
		case qv.Get("ppprop") == "wikibase_item": // QID статьи — без связи с Wikidata
			_ = json.NewEncoder(w).Encode(map[string]any{"query": map[string]any{"pages": []any{
				map[string]any{"title": qv.Get("titles")}}}})
		case qv.Get("list") == "search":
			calls = append(calls, "search:"+qv.Get("srsearch"))
			var out []map[string]string
			for _, h := range hits[qv.Get("srsearch")] {
				out = append(out, map[string]string{"title": h})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"query": map[string]any{"search": out}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// Поиск по нескольким формам имени (case study #280): статьи «Имя Фамилия»
// (псевдонимы, китайские имена), второе имя иностранца, первый результат — не
// всегда наш, несколько совпадений — тёзки.
func TestWikipedia_ResolveByNameForms(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name     string
		q        AuthorQuery
		search   map[string][]string
		hits     map[string][]string
		want     string
		wantErr  error
		wantCall []string
	}{
		{
			name:     "статья «Имя Фамилия» (псевдоним)",
			q:        AuthorQuery{LastName: "Кинселла", FirstName: "Софи", FullName: "Кинселла Софи"},
			search:   map[string][]string{"Софи Кинселла": {"Софи Кинселла"}},
			want:     "Софи Кинселла",
			wantCall: []string{"opensearch:Кинселла Софи", "opensearch:Софи Кинселла"},
		},
		{
			name:   "китайское имя в обратном порядке",
			q:      AuthorQuery{LastName: "Цысинь", FirstName: "Лю", FullName: "Цысинь Лю"},
			search: map[string][]string{"Лю Цысинь": {"Лю Цысинь", "Лю Синьюй", "Лю Синь"}},
			want:   "Лю Цысинь",
		},
		{
			name:   "второе имя иностранца не мешает",
			q:      AuthorQuery{LastName: "Виндж", FirstName: "Вернор", MiddleName: "Стефан", FullName: "Виндж Вернор Стефан"},
			search: map[string][]string{"Виндж Вернор": {"Виндж, Вернор"}},
			want:   "Виндж, Вернор",
		},
		{
			name:   "первый результат — чужой префикс, наш дальше",
			q:      AuthorQuery{LastName: "Генри", FirstName: "О", FullName: "Генри О"},
			search: map[string][]string{"Генри О": {"Генри Лайон Олди", "О. Генри", "Seal"}},
			want:   "О. Генри",
		},
		{
			name: "полное ФИО находит одну статью — тёзки по «Фамилия Имя» не мешают",
			q:    AuthorQuery{LastName: "Губарев", FirstName: "Алексей", MiddleName: "Александрович", FullName: "Губарев Алексей Александрович"},
			search: map[string][]string{
				"Губарев Алексей Александрович": {"Губарев, Алексей Александрович"},
				"Губарев Алексей":               {"Губарев, Алексей Александрович", "Губарев, Алексей Юрьевич"},
			},
			want:     "Губарев, Алексей Александрович",
			wantCall: []string{"opensearch:Губарев Алексей Александрович"},
		},
		{
			name: "несколько статей — тёзки, подтверждение книгой",
			q: AuthorQuery{LastName: "Васильев", FirstName: "Борис", FullName: "Васильев Борис",
				BookTitles: []string{"А зори здесь тихие"}},
			search: map[string][]string{"Васильев Борис": {"Васильев, Борис", "Васильев, Борис Львович", "Васильев, Борис Михайлович"}},
			hits:   map[string][]string{`"Васильев" "А зори здесь тихие"`: {"Васильев, Борис Львович", "А зори здесь тихие…"}},
			want:   "Васильев, Борис Львович",
		},
		{
			name: "несколько статей и книга не подтвердила — пусто",
			q: AuthorQuery{LastName: "Иванов", FirstName: "Юрий", FullName: "Иванов Юрий",
				BookTitles: []string{"Неизвестная книга"}},
			search:  map[string][]string{"Иванов Юрий": {"Иванов, Юрий Иванович", "Иванов, Юрий Михайлович"}},
			wantErr: ErrNotFound,
		},
		{
			name: "основная статья без уточнения и одноимённые с уточнением — основная (Тургенев)",
			q: AuthorQuery{LastName: "Тургенев", FirstName: "Иван", MiddleName: "Сергеевич", FullName: "Тургенев Иван Сергеевич",
				BookTitles: []string{"Отцы и дети"}},
			search: map[string][]string{"Тургенев Иван Сергеевич": {
				"Тургенев, Иван Сергеевич", "Тургенев, Иван Сергеевич (учёный)", "Тургенев, Иван Сергеевич (значения)"}},
			want:     "Тургенев, Иван Сергеевич",
			wantCall: []string{"opensearch:Тургенев Иван Сергеевич"},
		},
		{
			name: "страница «(значения)» — не тёзка (Достоевский)",
			q:    AuthorQuery{LastName: "Достоевский", FirstName: "Федор", MiddleName: "Михайлович", FullName: "Достоевский Федор Михайлович"},
			search: map[string][]string{"Достоевский Федор Михайлович": {
				"Достоевский, Фёдор Михайлович", "Достоевский, Фёдор Михайлович (значения)"}},
			want: "Достоевский, Фёдор Михайлович",
		},
		{
			name:    "ничего не нашлось",
			q:       AuthorQuery{LastName: "Несуществующий", FirstName: "Автор", FullName: "Несуществующий Автор"},
			wantErr: ErrNotFound,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, calls := wikiFormsServer(t, c.search, c.hits)
			p := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL)
			got, err := p.resolveTitle(ctx, "ru", c.q)
			if c.wantErr != nil {
				require.True(t, errors.Is(err, c.wantErr), "err = %v", err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, c.want, got)
			if c.wantCall != nil {
				require.Equal(t, c.wantCall, *calls)
			}
		})
	}
}

func TestSearchForms(t *testing.T) {
	require.Equal(t, []string{"Пушкин Александр Сергеевич", "Пушкин Александр", "Александр Пушкин"},
		searchForms(AuthorQuery{LastName: "Пушкин", FirstName: "Александр", MiddleName: "Сергеевич", FullName: "Пушкин Александр Сергеевич"}))
	require.Equal(t, []string{"Чайлд Ли", "Ли Чайлд"}, searchForms(AuthorQuery{LastName: "Чайлд", FirstName: "Ли", FullName: "Чайлд Ли"}))
	require.Equal(t, []string{"Гомер"}, searchForms(AuthorQuery{LastName: "Гомер", FullName: "Гомер"}))
}

// Редирект с имени автора на статью под другим именем (псевдоним): принимаем,
// только если полный текст статьи называет автора (case study #280).
func TestWikipedia_PseudonymRedirect(t *testing.T) {
	ctx := context.Background()
	q := AuthorQuery{LastName: "Йовил", FirstName: "Джек", FullName: "Йовил Джек"}
	for _, c := range []struct {
		name string
		full string
		ok   bool
	}{
		{"полный текст называет псевдоним", "Ким Ньюман — британский писатель. Под псевдонимом Джек Йовил написал серию романов.", true},
		{"не называет — другой человек", "Ким Ньюман — британский писатель и кинокритик.", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				qv := r.URL.Query()
				switch {
				case qv.Get("action") == "opensearch":
					_ = json.NewEncoder(w).Encode([]any{qv.Get("search"), []string{"Йовил, Джек"}, []string{}, []string{}})
				case qv.Get("prop") == "extracts|pageprops": // начало статьи (intro), редирект уже пройден
					_ = json.NewEncoder(w).Encode(map[string]any{"query": map[string]any{"pages": []any{
						map[string]any{"title": "Ньюман, Ким", "extract": "Ким Ньюман (англ. Kim Newman; род. 31 июля 1959) — британский писатель."},
					}}})
				case qv.Get("prop") == "extracts": // полный текст для проверки псевдонима
					_ = json.NewEncoder(w).Encode(map[string]any{"query": map[string]any{"pages": []any{
						map[string]any{"title": "Ньюман, Ким", "extract": c.full},
					}}})
				default:
					http.NotFound(w, r)
				}
			}))
			defer srv.Close()
			p := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL)
			var tr AuthorTrace
			bio, err := p.intro(WithAuthorTrace(ctx, &tr), "ru", q)
			if c.ok {
				require.NoError(t, err)
				require.Contains(t, bio, "Ким Ньюман")
				require.Contains(t, tr.Reason(), "pass accept")
				return
			}
			require.True(t, errors.Is(err, ErrNotFound), "err = %v", err)
			require.Contains(t, tr.Reason(), "article_is_author")
		})
	}
}

// Латинское имя для латинских источников (case study #280).
func TestAuthorQuery_LatinQuery(t *testing.T) {
	q := AuthorQuery{LastName: "Ле Гуин", FirstName: "Урсула К", FullName: "Ле Гуин Урсула К", LatinName: "le guin ursula k."}
	l, ok := q.latinQuery()
	require.True(t, ok)
	require.Equal(t, "le guin", l.LastName)
	require.Equal(t, "ursula", l.FirstName)
	require.Equal(t, "k.", l.MiddleName)
	require.True(t, authorNameMatches(l, "Ursula K. Le Guin"))

	for _, c := range []struct {
		last, first, latin string
		ok                 bool
	}{
		{"Виндж", "Вернор", "vinge vernor", true},
		{"Гюго", "Виктор", "hugo victor", true},
		{"Цысинь", "Лю", "cixin liu", true},
		{"Хемингуэй", "Эрнест", "hemingway ernest", true},
		{"Джойс", "Джеймс", "joyce james", true},
		{"Кинселла", "Софи", "wickham madeleine", false}, // настоящее имя, не наше
		{"Старджон", "Теодор", "sturgeon theodore", true},
		{"Кинселла", "Софи", "", false},
		{"Кинселла", "Софи", "kinsella", false}, // у нас есть имя, у латинского — нет
	} {
		_, ok := AuthorQuery{LastName: c.last, FirstName: c.first, LatinName: c.latin}.latinQuery()
		require.Equal(t, c.ok, ok, "%s %s ← %q", c.last, c.first, c.latin)
	}

	require.Equal(t, q, queryForLang(q, "ru"), "русская Википедия — по-русски")
	require.Equal(t, "le guin ursula k.", queryForLang(q, "en").FullName)
	noLatin := AuthorQuery{LastName: "Пушкин", FirstName: "Александр", FullName: "Пушкин Александр"}
	require.Equal(t, noLatin, queryForLang(noLatin, "en"), "нет латинского имени — как раньше")
}

// Путь фото идёт следом за путём био — найденная статья (и «не найдено») берётся
// из кэша, без повторного поиска.
func TestWikipedia_ResolveTitleCached(t *testing.T) {
	ctx := context.Background()
	srv, calls := wikiFormsServer(t, map[string][]string{"Ли Чайлд": {"Ли Чайлд"}}, nil)
	p := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL)
	q := AuthorQuery{ID: 7, LastName: "Чайлд", FirstName: "Ли", FullName: "Чайлд Ли"}
	for i := 0; i < 2; i++ {
		got, err := p.resolveTitle(ctx, "ru", q)
		require.NoError(t, err)
		require.Equal(t, "Ли Чайлд", got)
	}
	require.Equal(t, []string{"opensearch:Чайлд Ли", "opensearch:Ли Чайлд"}, *calls, "второй раз — из кэша")

	missing := AuthorQuery{ID: 8, LastName: "Нет", FirstName: "Такого", FullName: "Нет Такого"}
	for i := 0; i < 2; i++ {
		_, err := p.resolveTitle(ctx, "ru", missing)
		require.ErrorIs(t, err, ErrNotFound)
	}
	require.Len(t, *calls, 4, "«не найдено» тоже кэшируется")
}

// Ни одна статья не прошла гейт имени, но одна совпала нестрого — кандидат с
// пометкой MatchLoose (решает политика); без проверки кандидата — не принимаем.
func TestWikipedia_LooseCandidate(t *testing.T) {
	ctx := context.Background()
	q := AuthorQuery{LastName: "Джексон", FirstName: "Ширли", FullName: "Джексон Ширли"}
	srv, _ := wikiFormsServer(t, map[string][]string{"Джексон Ширли": {"Джексон, Шерли", "Джексон, Майкл"}}, nil)
	var got MatchKind = -1
	p := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL).
		WithCandidateCheck(func(_ context.Context, _ AuthorQuery, _, _, title, _ string, m MatchKind) (bool, error) {
			require.Equal(t, "Джексон, Шерли", title)
			got = m
			return true, nil
		})
	title, err := p.resolveTitle(ctx, "ru", q)
	require.NoError(t, err)
	require.Equal(t, "Джексон, Шерли", title)
	require.Equal(t, MatchLoose, got)

	plain := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL)
	_, err = plain.resolveTitle(ctx, "ru", q)
	require.ErrorIs(t, err, ErrNotFound)
}

// Основная статья не прошла политику, рядом одноимённые с уточнением — строгий
// путь по книгам находит нужную; статья-связка («X и Y») по книге не берётся.
func TestWikipedia_PrimaryRejectedFallsBackToStrict(t *testing.T) {
	ctx := context.Background()
	q := AuthorQuery{LastName: "Иванов", FirstName: "Юрий", FullName: "Иванов Юрий", BookTitles: []string{"Повесть о море"}}
	srv, _ := wikiFormsServer(t,
		map[string][]string{"Иванов Юрий": {"Иванов, Юрий", "Иванов, Юрий (писатель)"}},
		map[string][]string{`"Иванов" "Повесть о море"`: {"Юрий Иванов и Пётр Петров", "Иванов, Юрий (писатель)"}})
	var checked []string
	p := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL).
		WithCandidateCheck(func(_ context.Context, _ AuthorQuery, _, _, title, _ string, m MatchKind) (bool, error) {
			checked = append(checked, title)
			return title != "Иванов, Юрий", nil // основная — футболист
		})
	got, err := p.resolveTitle(ctx, "ru", q)
	require.NoError(t, err)
	require.Equal(t, "Иванов, Юрий (писатель)", got)
	require.Equal(t, []string{"Иванов, Юрий", "Иванов, Юрий (писатель)"}, checked, "связка пропущена, основная проверена первой")
}

// Сбой в русском разделе — английский не спрашиваем (#347, приёмка 1.19.1: Саймак
// получил английскую био вместо русской из-за сбоя Wikidata на ru).
func TestWikipedia_TransientStopsLanguages(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	p := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL)
	_, err := p.FetchAuthorBio(context.Background(), AuthorQuery{LastName: "Саймак", FirstName: "Клиффорд", FullName: "Саймак Клиффорд"})
	require.ErrorIs(t, err, ErrUpstream)
	require.Equal(t, 1, calls, "после 429 в первом разделе запросов больше нет")
	_, err = p.AuthorPhotoSource(context.Background(), AuthorQuery{LastName: "Саймак", FirstName: "Клиффорд", FullName: "Саймак Клиффорд"})
	require.ErrorIs(t, err, ErrUpstream)
	require.Equal(t, 2, calls)
}
