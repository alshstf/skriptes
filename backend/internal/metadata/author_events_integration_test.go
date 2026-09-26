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
