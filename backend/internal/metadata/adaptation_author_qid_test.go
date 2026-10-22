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

	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestEnsureAdaptations_AuthorQIDFromDB — #570: ленивый путь карточки передаёт
// только имена авторов, QID автора EnsureAdaptations берёт из базы — и «Дюна»
// «Херберта Фрэнка» подтверждается в Wikidata, хотя там он «Фрэнк Герберт».
// Найденный QID книги становится ключом группировки изданий.
func TestEnsureAdaptations_AuthorQIDFromDB(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	srv := wdMockServer(t, wdMockArgs{
		searchHits: []string{"Q25391", "Q190192"},
		// Только полное имя: по буквам «Херберт Фрэнк» сходится одна фамилия —
		// роман подтверждает QID автора из базы.
		authorLabels: map[string][]string{"Q190192": {"Franklin Patrick Herbert Jr."}},
		authorQIDs:   map[string]string{"Q190192": "Q7934"},
		adaptations: map[string][]map[string]string{"Q190192": {
			{"film": "http://www.wikidata.org/entity/Q114819", "filmLabel": "Дюна", "year": "1984", "kindLabel": "film"},
		}},
	})
	defer srv.Close()
	wd := NewWikidataAdaptationsProvider(nil).WithEndpoints(srv.URL+"/w/api.php", srv.URL+"/sparql", "")
	enricher, err := New(pool, t.TempDir(), nil, nil, nil, nil, []AdaptationProvider{wd}, quiet)
	require.NoError(t, err)

	collID, archID := seedTitleFixture(t, ctx, pool)
	herbert := seedGroupAuthor(t, ctx, pool, "Херберт", "херберт фрэнк")
	_, err = pool.Exec(ctx, `UPDATE authors SET first_name = 'Фрэнк', ext_ids = '{"wd_qid":"Q7934"}' WHERE id = $1`, herbert)
	require.NoError(t, err)
	dune := seedGroupBook(t, ctx, pool, collID, archID, herbert, "D1", "Дюна", "дюна", "ru", "", "", "")

	// Как ленивый путь: имя автора из карточки, QID не передаётся.
	enricher.EnsureAdaptations(ctx, BookQuery{ID: dune, Title: "Дюна", Authors: []string{"Херберт Фрэнк"}})

	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM book_adaptations WHERE book_id = $1`, dune).Scan(&n))
	require.Equal(t, 1, n, "экранизация «Дюны» найдена")
	var key string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT work_key FROM book_work_lookups WHERE book_id = $1 AND source = 'wikidata' AND outcome = 'found'`,
		dune).Scan(&key))
	require.Equal(t, "Q190192", key)
}

// TestRecheckAdaptationsByAuthorQID — #570: перепросить промахи только у книг
// авторов, которых старое правило не находило в их метках Wikidata; сбой
// Wikidata — без сброса.
func TestRecheckAdaptationsByAuthorQID(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)

	labels := map[string][]string{
		"Q7934": {"Frank Herbert", "Фрэнк Герберт"}, // «Херберт Фрэнк» по буквам не находился
		"Q7243": {"Leo Tolstoy", "Лев Толстой"},     // находился — перепрашивать незачем
		"Q1":    {"Evan Hunter", "Эван Хантер"},     // псевдоним «Эд Макбейн»
	}
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		var bindings []map[string]map[string]string
		for _, q := range extractAllQIDs(r.FormValue("query")) {
			for _, l := range labels[q] {
				bindings = append(bindings, map[string]map[string]string{
					"a": {"value": "http://www.wikidata.org/entity/" + q}, "l": {"value": l},
				})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": map[string]any{"bindings": bindings}})
	}))
	defer srv.Close()
	wd := NewWikidataAdaptationsProvider(nil).WithEndpoints("", srv.URL, "")

	collID, archID := seedTitleFixture(t, ctx, pool)
	author := func(last, first, qid string) int64 {
		id := seedGroupAuthor(t, ctx, pool, last, last+" "+first)
		_, err := pool.Exec(ctx, `UPDATE authors SET first_name = $2,
			ext_ids = CASE WHEN $3 = '' THEN '{}'::jsonb ELSE jsonb_build_object('wd_qid', $3::text) END WHERE id = $1`, id, first, qid)
		require.NoError(t, err)
		return id
	}
	herbert := author("Херберт", "Фрэнк", "Q7934")
	tolstoy := author("Толстой", "Лев", "Q7243")
	mcbain := author("Макбейн", "Эд", "Q1")
	noQID := author("Безкуидов", "Иван", "")

	book := func(lib string, a int64) int64 {
		id := seedGroupBook(t, ctx, pool, collID, archID, a, lib, lib, lib, "ru", "", "", "")
		_, err := pool.Exec(ctx, `UPDATE books SET adaptations_fetched_at = now() - interval '100 days' WHERE id = $1`, id)
		require.NoError(t, err)
		return id
	}
	dune := book("dune", herbert)
	found := book("found", herbert)
	_, err := pool.Exec(ctx, `INSERT INTO book_adaptations (book_id, provider, ext_id, title, kind) VALUES ($1,'wikidata','Q1','Фильм','film')`, found)
	require.NoError(t, err)
	deleted := book("deleted", herbert)
	_, err = pool.Exec(ctx, `UPDATE books SET deleted = true WHERE id = $1`, deleted)
	require.NoError(t, err)
	anna := book("anna", tolstoy)
	together := book("together", tolstoy)
	_, err = pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id, position) VALUES ($1, $2, 1)`, together, herbert)
	require.NoError(t, err)
	cop := book("cop", mcbain)
	alien := book("alien", noQID)

	pending := func(id int64) bool {
		var at *time.Time
		require.NoError(t, pool.QueryRow(ctx, `SELECT adaptations_fetched_at FROM books WHERE id = $1`, id).Scan(&at))
		return at == nil
	}

	fail.Store(true)
	_, _, err = RecheckAdaptationsByAuthorQID(ctx, pool, wd, 0)
	require.ErrorIs(t, err, ErrUpstream)
	require.False(t, pending(dune), "сбой Wikidata — ничего не сброшено")

	fail.Store(false)
	authors, books, err := RecheckAdaptationsByAuthorQID(ctx, pool, wd, 0)
	require.NoError(t, err)
	require.Equal(t, 2, authors, "Херберт и Макбейн")
	require.EqualValues(t, 3, books)
	for _, id := range []int64{dune, together, cop} {
		require.True(t, pending(id), "книга автора, которого старое правило не находило, — перепросить")
	}
	require.False(t, pending(anna), "Толстого старое правило находило")
	require.False(t, pending(alien), "автор без QID — проверка та же, что была")
	require.False(t, pending(found), "экранизации уже есть")
	require.False(t, pending(deleted))
}

func TestLegacyAuthorNameMatches(t *testing.T) {
	require.False(t, legacyAuthorNameMatches("Херберт Фрэнк", []string{"Frank Herbert", "Фрэнк Герберт"}))
	require.True(t, legacyAuthorNameMatches("Толстой, Лев Николаевич", []string{"Лев Николаевич Толстой"}))
	require.False(t, legacyAuthorNameMatches("Тертлдав Гарри", []string{"Гарри Тёртлдав"}), "ё не сводилась")
	require.True(t, legacyAuthorNameMatches("Гомер", []string{"Гомер"}))
	require.False(t, legacyAuthorNameMatches("Гомер", nil))
}
