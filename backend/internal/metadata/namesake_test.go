package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestNoteMatchesQualifier(t *testing.T) {
	require.True(t, noteMatchesQualifier("писатель-маринист", "писатель"))
	require.True(t, noteMatchesQualifier("журналист", "журналистка"))
	require.True(t, noteMatchesQualifier("Блум", "Блум"))
	require.True(t, noteMatchesQualifier("историк", "историк, краевед"))
	require.False(t, noteMatchesQualifier("фантаст", "писатель"))
	require.False(t, noteMatchesQualifier("поэт", "поэтесса"), "короткие — только целиком")
	require.False(t, noteMatchesQualifier("#17465", "писатель"))
}

func TestSplitQualifierAndBases(t *testing.T) {
	b, q := splitQualifier("Блинов, Николай Николаевич (писатель)")
	require.Equal(t, "Блинов, Николай Николаевич", b)
	require.Equal(t, "писатель", q)
	b, q = splitQualifier("Гибсон, Уильям")
	require.Equal(t, "Гибсон, Уильям", b)
	require.Empty(t, q)

	require.Equal(t, []string{"Блинов, Николай Николаевич", "Блинов, Николай"},
		wikiTitleBases(AuthorQuery{LastName: "Блинов", FirstName: "Николай", MiddleName: "Николаевич"}))
	require.Equal(t, []string{"Антоний"}, wikiTitleBases(AuthorQuery{LastName: "Антоний"}))
	require.True(t, numericNote("#17465"))
	require.False(t, numericNote("фантаст"))
}

// wikiNamesakeServer — Википедия: opensearch → titles, list=search → hits по
// srsearch, query titles=… → существует ли статья (articles).
func wikiNamesakeServer(t *testing.T, titles []string, articles map[string]bool, hits map[string][]string) (*httptest.Server, *[]string) {
	t.Helper()
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		qv := r.URL.Query()
		switch {
		case qv.Get("action") == "opensearch":
			calls = append(calls, "opensearch:"+qv.Get("limit"))
			_ = json.NewEncoder(w).Encode([]any{qv.Get("search"), titles, []string{}, []string{}})
		case qv.Get("list") == "search":
			calls = append(calls, "search:"+qv.Get("srsearch"))
			var out []map[string]string
			for _, h := range hits[qv.Get("srsearch")] {
				out = append(out, map[string]string{"title": h})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"query": map[string]any{"search": out}})
		case qv.Get("ppprop") == "disambiguation":
			title := qv.Get("titles")
			calls = append(calls, "title:"+title)
			page := map[string]any{"title": title}
			if !articles[title] {
				page["missing"] = true
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"query": map[string]any{"pages": []any{page}}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestWikipedia_StrictTitle(t *testing.T) {
	ctx := context.Background()

	t.Run("статья «Имя (уточнение)»", func(t *testing.T) {
		srv, _ := wikiNamesakeServer(t, nil, map[string]bool{"Антоний (Блум)": true}, nil)
		p := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL)
		got, err := p.resolveTitle(ctx, "ru", AuthorQuery{LastName: "Антоний", FullName: "Антоний", Note: "Блум"})
		require.NoError(t, err)
		require.Equal(t, "Антоний (Блум)", got)
	})

	t.Run("уточнение статьи совпало по основе", func(t *testing.T) {
		srv, _ := wikiNamesakeServer(t, []string{
			"Блинов, Николай Николаевич",
			"Блинов, Николай Николаевич (медицинский физик)",
			"Блинов, Николай Николаевич (писатель)",
		}, nil, nil)
		p := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL)
		got, err := p.resolveTitle(ctx, "ru", AuthorQuery{
			LastName: "Блинов", FirstName: "Николай", MiddleName: "Николаевич",
			FullName: "Блинов Николай Николаевич", Note: "писатель-маринист",
		})
		require.NoError(t, err)
		require.Equal(t, "Блинов, Николай Николаевич (писатель)", got)
	})

	t.Run("якорь по книге", func(t *testing.T) {
		srv, calls := wikiNamesakeServer(t,
			[]string{"Гибсон, Уильям", "Гибсон, Уильям (драматург)"}, nil,
			map[string][]string{`"Гибсон" "Нейромант"`: {"Нейромант", "Гибсон, Уильям"}})
		p := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL)
		got, err := p.resolveTitle(ctx, "ru", AuthorQuery{
			LastName: "Гибсон", FirstName: "Уильям", FullName: "Гибсон Уильям",
			Note: "фантаст", BookTitles: []string{"Нейромант"},
		})
		require.NoError(t, err)
		require.Equal(t, "Гибсон, Уильям", got, "статью о книге отсеял имя-гейт, взята статья об авторе")
		require.Contains(t, *calls, `search:"Гибсон" "Нейромант"`)
	})

	t.Run("тёзка без подтверждения — не найдено, а не первый по имени", func(t *testing.T) {
		srv, _ := wikiNamesakeServer(t, []string{"Афанасьев, Александр Николаевич"}, nil, nil)
		p := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL)
		_, err := p.resolveTitle(ctx, "ru", AuthorQuery{
			LastName: "Афанасьев", FirstName: "Александр", MiddleName: "Николаевич",
			FullName: "Афанасьев Александр Николаевич", Note: "#17465", BookTitles: []string{"Неизвестная книга"},
		})
		require.True(t, errors.Is(err, ErrNotFound))
	})

	t.Run("без тёзок — как раньше: первый результат по имени", func(t *testing.T) {
		srv, calls := wikiNamesakeServer(t, []string{"Гибсон, Уильям"}, nil, nil)
		p := NewWikipediaProvider(srv.Client()).WithAPIRoot(srv.URL)
		got, err := p.resolveTitle(ctx, "ru", AuthorQuery{LastName: "Гибсон", FirstName: "Уильям", FullName: "Гибсон Уильям"})
		require.NoError(t, err)
		require.Equal(t, "Гибсон, Уильям", got)
		require.Equal(t, []string{"opensearch:1"}, *calls)
	})
}

func TestOpenLibrary_StrictAuthorKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/search.json" && r.URL.Query().Get("title") == "Neuromancer":
			_ = json.NewEncoder(w).Encode(map[string]any{"docs": []any{
				map[string]any{"author_key": []string{"OL1A"}, "author_name": []string{"Someone Else"}},
				map[string]any{"author_key": []string{"OL2A", "OL3A"}, "author_name": []string{"Брюс Стерлинг", "Уильям Гибсон"}},
			}})
		case r.URL.Path == "/search.json":
			_ = json.NewEncoder(w).Encode(map[string]any{"docs": []any{}})
		case strings.HasPrefix(r.URL.Path, "/authors/OL3A"):
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "William Gibson", "bio": "cyberpunk"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p := NewOpenLibraryProvider(srv.Client()).WithEndpoints(srv.URL+"/search.json", srv.URL)

	a, err := p.authorSearch(context.Background(), AuthorQuery{
		LastName: "Гибсон", FirstName: "Уильям", FullName: "Гибсон Уильям",
		Note: "фантаст", BookTitles: []string{"Нейромант", "Neuromancer"},
	})
	require.NoError(t, err)
	require.Equal(t, "OL3A", a.OLID, "автор книги с подходящим именем, соавтор отсеян")

	_, err = p.authorSearch(context.Background(), AuthorQuery{
		LastName: "Гибсон", FirstName: "Уильям", FullName: "Гибсон Уильям",
		Namesakes: true, BookTitles: []string{"Нет такой"},
	})
	require.True(t, errors.Is(err, ErrNotFound), "книгой не подтвердился — не найдено")
}

func TestEnricher_WithNamesakeContext(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx := context.Background()
	pool := testpg.Pool(t, ctx)
	e := &Enricher{pool: pool, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	id := func(sql string, args ...any) int64 {
		t.Helper()
		var v int64
		require.NoError(t, pool.QueryRow(ctx, sql, args...).Scan(&v))
		return v
	}
	coll := id(`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`)
	arch := id(`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, coll)
	gibson := id(`INSERT INTO authors (last_name, first_name, normalized_name, name_note) VALUES ('Гибсон','Уильям','гибсон уильям','фантаст') RETURNING id`)
	id(`INSERT INTO authors (last_name, first_name, normalized_name, name_note) VALUES ('Гибсон','Уильям','гибсон уильям','драматург') RETURNING id`)
	solo := id(`INSERT INTO authors (last_name, first_name, normalized_name) VALUES ('Пелевин','Виктор','пелевин виктор') RETURNING id`)
	book := func(author int64, lib, title, src string) {
		t.Helper()
		w := id(`INSERT INTO works (title, normalized_title) VALUES ($1, lower($2)) RETURNING id`, title, title)
		b := id(`INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, src_title, work_id)
			VALUES ($1,$2,$3,$3,'fb2',$4,lower($5),NULLIF($6,''),$7) RETURNING id`, coll, arch, lib, title, title, src, w)
		_, err := pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id) VALUES ($1,$2)`, b, author)
		require.NoError(t, err)
	}
	book(gibson, "1", "Нейромант", "Neuromancer")
	book(solo, "2", "Чапаев и Пустота", "")

	q := e.withNamesakeContext(ctx, AuthorQuery{ID: gibson})
	require.Equal(t, "фантаст", q.Note)
	require.True(t, q.Namesakes)
	require.ElementsMatch(t, []string{"Нейромант", "Neuromancer"}, q.BookTitles)
	require.True(t, q.Strict())

	q = e.withNamesakeContext(ctx, AuthorQuery{ID: solo})
	require.Empty(t, q.Note)
	require.False(t, q.Namesakes)
	require.Equal(t, []string{"Чапаев и Пустота"}, q.BookTitles)
	require.False(t, q.Strict(), "без тёзок и уточнения — обычный поиск")
}
