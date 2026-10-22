package metadata

import (
	"context"
	"io"
	"log/slog"
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

// TestRecheckAdaptationsByAuthorQID — #570: перепросить промахи только у живых
// книг без экранизаций с автором, у которого есть QID, и у известных работ.
func TestRecheckAdaptationsByAuthorQID(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)

	collID, archID := seedTitleFixture(t, ctx, pool)
	withQID := seedGroupAuthor(t, ctx, pool, "Херберт", "херберт")
	_, err := pool.Exec(ctx, `UPDATE authors SET ext_ids = '{"wd_qid":"Q7934"}' WHERE id = $1`, withQID)
	require.NoError(t, err)
	noQID := seedGroupAuthor(t, ctx, pool, "Безкуидов", "безкуидов")

	book := func(lib string, author int64, workSQL string) int64 {
		id := seedGroupBook(t, ctx, pool, collID, archID, author, lib, lib, lib, "ru", "", "", "")
		_, err := pool.Exec(ctx, `UPDATE books SET adaptations_fetched_at = now() - interval '100 days' WHERE id = $1`, id)
		require.NoError(t, err)
		if workSQL != "" {
			_, err = pool.Exec(ctx, `UPDATE works SET `+workSQL+` WHERE id = (SELECT work_id FROM books WHERE id = $1)`, id)
			require.NoError(t, err)
		}
		return id
	}
	editions := book("editions", withQID, "edition_count = 2")
	workQID := book("work-qid", withQID, `ext_ids = '{"wd_qid":"Q190192"}'`)
	fantlab := book("fantlab", withQID, "fantlab_marks = 120")
	obscure := book("obscure", withQID, "")
	alien := book("no-qid", noQID, "edition_count = 3")
	found := book("found", withQID, "edition_count = 2")
	_, err = pool.Exec(ctx, `INSERT INTO book_adaptations (book_id, provider, ext_id, title, kind) VALUES ($1,'wikidata','Q1','Фильм','film')`, found)
	require.NoError(t, err)
	deleted := book("deleted", withQID, "edition_count = 2")
	_, err = pool.Exec(ctx, `UPDATE books SET deleted = true WHERE id = $1`, deleted)
	require.NoError(t, err)

	n, err := RecheckAdaptationsByAuthorQID(ctx, pool)
	require.NoError(t, err)
	require.EqualValues(t, 3, n)
	pending := func(id int64) bool {
		var at *time.Time
		require.NoError(t, pool.QueryRow(ctx, `SELECT adaptations_fetched_at FROM books WHERE id = $1`, id).Scan(&at))
		return at == nil
	}
	for _, id := range []int64{editions, workQID, fantlab} {
		require.True(t, pending(id), "известная работа автора с QID — перепросить")
	}
	require.False(t, pending(obscure), "одиночное издание без сигналов известности — нет")
	require.False(t, pending(alien), "автор без QID — проверка та же, что была")
	require.False(t, pending(found), "экранизации уже есть")
	require.False(t, pending(deleted))

	n, err = RecheckAdaptationsByAuthorQID(ctx, pool)
	require.NoError(t, err)
	require.Zero(t, n, "повтор — только то, что снова помечено (сброшенные ждут воркер)")
}
