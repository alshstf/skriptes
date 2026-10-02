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
			search:  map[string][]string{"Иванов Юрий": {"Иванов, Юрий Иванович (футболист)", "Иванов, Юрий Михайлович"}},
			wantErr: ErrNotFound,
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
