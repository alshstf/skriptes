package metadata

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

func seedEventsAuthor(t *testing.T, ctx context.Context, pool *pgxpool.Pool, last string, renown int64, qid string) int64 {
	t.Helper()
	var id int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO authors (last_name, normalized_name, renown, ext_ids)
		VALUES ($1, lower($1), $2, CASE WHEN $3 = '' THEN '{}'::jsonb ELSE jsonb_build_object('wd_qid', $3::text) END)
		RETURNING id`, last, renown, qid).Scan(&id))
	return id
}

// EnsureAuthorEvents: renown-гейт, идемпотентность, hidden переживает refetch,
// транзиент SPARQL не ставит маркер, чистка выпавших не-hidden строк.
func TestEnsureAuthorEvents(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPGForPrewarm(t, ctx)

	// SPARQL-мок: управляемый набор событий + режим 500.
	var fail atomic.Bool
	var bindings atomic.Value // []map[string]sparqlVal
	bindings.Store([]map[string]sparqlVal{
		evRow(map[string]string{"prop": "P569", "date": "1821-11-11T00:00:00Z", "prec": "11"}),
		evRow(map[string]string{"prop": "P26", "date": "1867-02-15T00:00:00Z", "who": "http://www.wikidata.org/entity/Q463877", "whoLabel": "Анна Сниткина"}),
		evRow(map[string]string{"prop": "P793", "date": "1849-04-23T00:00:00Z", "who": "http://www.wikidata.org/entity/Q5", "whoLabel": "арест петрашевцев"}),
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		rows := bindings.Load().([]map[string]sparqlVal)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": map[string]any{"bindings": rows},
		})
	}))
	defer srv.Close()

	enricher, err := New(pool, t.TempDir()+"/covers", nil, nil, nil, nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	enricher.WithAuthorEvents(NewWikidataEventsProvider(nil).WithEndpoints(srv.URL, srv.URL), nil)

	author := seedEventsAuthor(t, ctx, pool, "Достоевский", 2000, "Q991")
	nobody := seedEventsAuthor(t, ctx, pool, "Безвестный", 0, "Q000")

	fetchedAt := func(id int64) *time.Time {
		var v *time.Time
		require.NoError(t, pool.QueryRow(ctx, `SELECT events_fetched_at FROM authors WHERE id = $1`, id).Scan(&v))
		return v
	}
	countEvents := func(id int64) int {
		var n int
		require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM author_events WHERE author_id = $1`, id).Scan(&n))
		return n
	}

	// renown=0 → no-op (не ядро).
	enricher.EnsureAuthorEvents(ctx, nobody)
	require.Zero(t, countEvents(nobody))
	require.Nil(t, fetchedAt(nobody))

	// Транзиент SPARQL → строки не пишутся, маркер НЕ ставится.
	fail.Store(true)
	enricher.EnsureAuthorEvents(ctx, author)
	require.Zero(t, countEvents(author))
	require.Nil(t, fetchedAt(author), "транзиент не ставит маркер (грабля №20)")

	// Успех: 3 события, маркер стоит.
	fail.Store(false)
	enricher.EnsureAuthorEvents(ctx, author)
	require.Equal(t, 3, countEvents(author))
	require.NotNil(t, fetchedAt(author))

	// Идемпотентность: повторный Ensure отсечён маркером.
	enricher.EnsureAuthorEvents(ctx, author)
	require.Equal(t, 3, countEvents(author))

	// Курирование: скрываем арест, сбрасываем маркер (эмуляция refetch),
	// источник теперь отдаёт на одно событие меньше (брак выпал).
	_, err = pool.Exec(ctx, `UPDATE author_events SET hidden = true WHERE author_id = $1 AND event_type = $2`, author, EventPersecution)
	require.NoError(t, err)
	bindings.Store([]map[string]sparqlVal{
		evRow(map[string]string{"prop": "P569", "date": "1821-11-11T00:00:00Z", "prec": "11"}),
	})
	_, err = pool.Exec(ctx, `UPDATE authors SET events_fetched_at = NULL WHERE id = $1`, author)
	require.NoError(t, err)
	enricher.EnsureAuthorEvents(ctx, author)

	// Брак (не-hidden, выпал из набора) — удалён; арест (hidden) — ПЕРЕЖИЛ
	// refetch и остался скрытым; рождение — на месте.
	var hiddenCount, total int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE hidden) FROM author_events WHERE author_id = $1`, author).Scan(&total, &hiddenCount))
	require.Equal(t, 2, total, "рождение + скрытый арест")
	require.Equal(t, 1, hiddenCount, "hidden переживает refetch")
}

// Wiki-путь PR-2: sitelink-резолв, merge wd↔wiki (дубль поглощается, цитата
// переносится в wd-строку), новые вехи из текста, prune wiki-источника.
func TestEnsureAuthorEventsWiki(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPGForPrewarm(t, ctx)

	article := "Преамбула.\n" +
		"== Биография ==\n" +
		"В 1849 году арестован по делу петрашевцев и приговорён к каторге.\n" + // дубль (1849, persecution) с wd P793
		"В 1867 году женился на Анне Григорьевне Сниткиной.\n" + // дубль (1867, love) с wd P26
		"В 1866 году опубликован роман «Преступление и наказание».\n" + // новая веха career
		"== Память ==\n" +
		"В 1971 году открыт музей.\n"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost { // SPARQL
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": map[string]any{"bindings": []map[string]sparqlVal{
					evRow(map[string]string{"prop": "P569", "date": "1821-11-11T00:00:00Z", "prec": "11"}),
					evRow(map[string]string{"prop": "P26", "date": "1867-02-15T00:00:00Z", "who": "http://www.wikidata.org/entity/Q463877", "whoLabel": "Анна Сниткина"}),
					evRow(map[string]string{"prop": "P793", "date": "1849-04-23T00:00:00Z", "who": "http://www.wikidata.org/entity/Q5", "whoLabel": "арест петрашевцев"}),
				}},
			})
			return
		}
		q := r.URL.Query()
		switch {
		case q.Get("action") == "wbgetentities":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"entities": map[string]any{"Q991": map[string]any{
					"sitelinks": map[string]any{"ruwiki": map[string]string{"title": "Достоевский, Фёдор Михайлович"}},
				}},
			})
		case q.Get("action") == "query" && q.Get("prop") == "extracts":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"query": map[string]any{"pages": []map[string]any{{"extract": article}}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	enricher, err := New(pool, t.TempDir()+"/covers", nil, nil, nil, nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	enricher.WithAuthorEvents(NewWikidataEventsProvider(nil).WithEndpoints(srv.URL, srv.URL), nil).
		WithAuthorEventsWiki(NewWikipediaProvider(nil).WithAPIRoot(srv.URL))

	author := seedEventsAuthor(t, ctx, pool, "Достоевский", 2000, "Q991")
	enricher.EnsureAuthorEvents(ctx, author)

	// 3 wd (рождение, брак, арест) + 1 wiki (публикация-1866); дубли поглощены.
	rows, err := pool.Query(ctx,
		`SELECT source, event_type, year_from, COALESCE(quote,'') FROM author_events WHERE author_id = $1 ORDER BY year_from, source`, author)
	require.NoError(t, err)
	type evRow2 struct {
		src, typ, quote string
		year            int
	}
	var got []evRow2
	for rows.Next() {
		var e evRow2
		require.NoError(t, rows.Scan(&e.src, &e.typ, &e.year, &e.quote))
		got = append(got, e)
	}
	require.NoError(t, rows.Err())
	require.Len(t, got, 4, "3 wd + 1 wiki: %+v", got)

	bySrc := map[string]int{}
	for _, e := range got {
		bySrc[e.src]++
		if e.src == "wikidata" && e.typ == EventLove {
			require.Contains(t, e.quote, "женился на Анне", "цитата wiki-дубля должна перейти в wd-строку брака")
		}
		if e.src == "wikipedia" {
			require.Equal(t, EventCareer, e.typ)
			require.Equal(t, 1866, e.year)
		}
	}
	require.Equal(t, 3, bySrc["wikidata"])
	require.Equal(t, 1, bySrc["wikipedia"])
}

// Транзиент MediaWiki маскируется под HTTP 200 + error-JSON (readonly-окно,
// internal_api_error_*). Он ОБЯЗАН быть ErrUpstream, а не «статьи нет»: иначе
// single-shot маркер встанет навсегда и вехи потеряются (грабля №20).
func TestWikiAPIErrorIsTransient(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPGForPrewarm(t, ctx)

	// Какой из двух вызовов возвращает error-JSON: "sitelinks" | "extracts".
	var brokenCall atomic.Value
	brokenCall.Store("sitelinks")
	apiError := map[string]any{"error": map[string]string{"code": "internal_api_error_DBQueryError"}}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost { // SPARQL — всегда здоров
			_ = json.NewEncoder(w).Encode(map[string]any{
				"results": map[string]any{"bindings": []map[string]sparqlVal{
					evRow(map[string]string{"prop": "P569", "date": "1821-11-11T00:00:00Z", "prec": "11"}),
				}},
			})
			return
		}
		broken := brokenCall.Load().(string)
		switch r.URL.Query().Get("action") {
		case "wbgetentities":
			if broken == "sitelinks" {
				_ = json.NewEncoder(w).Encode(apiError) // HTTP 200 + error!
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"entities": map[string]any{"Q991": map[string]any{
					"sitelinks": map[string]any{"ruwiki": map[string]string{"title": "Статья"}},
				}},
			})
		case "query":
			_ = json.NewEncoder(w).Encode(apiError) // HTTP 200 + error!
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	enricher, err := New(pool, t.TempDir()+"/covers", nil, nil, nil, nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	enricher.WithAuthorEvents(NewWikidataEventsProvider(nil).WithEndpoints(srv.URL, srv.URL), nil).
		WithAuthorEventsWiki(NewWikipediaProvider(nil).WithAPIRoot(srv.URL))

	marker := func(id int64) *time.Time {
		var v *time.Time
		require.NoError(t, pool.QueryRow(ctx, `SELECT events_fetched_at FROM authors WHERE id = $1`, id).Scan(&v))
		return v
	}

	// Сбой на sitelinks-вызове.
	a1 := seedEventsAuthor(t, ctx, pool, "Сбойный", 2000, "Q991")
	enricher.EnsureAuthorEvents(ctx, a1)
	require.Nil(t, marker(a1), "error-JSON от wbgetentities — транзиент, маркер не ставим")

	// Сбой на extracts-вызове (sitelinks здоров).
	brokenCall.Store("extracts")
	a2 := seedEventsAuthor(t, ctx, pool, "Сбойный2", 2000, "Q991")
	enricher.EnsureAuthorEvents(ctx, a2)
	require.Nil(t, marker(a2), "error-JSON от extracts — транзиент, маркер не ставим")
}
