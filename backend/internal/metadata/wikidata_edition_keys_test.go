package metadata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skriptes/skriptes/backend/internal/testpg"
)

// RepairEditionWorkKeys (#574): QID издания в ключе группировки и в QID работы →
// QID произведения; год из элемента-издания стёрт и пересчитан, попытки года и
// известности из Wikidata стёрты, маркер экранизаций и группировки сброшен.
// Работа с QID произведения и омнибус (два P629) не тронуты.
func TestRepairEditionWorkKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)

	const edition, work, okWork, omnibus = "Q100287822", "Q1744399", "Q190192", "Q500"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		q := r.Form.Get("query")
		var bindings []map[string]map[string]string
		add := func(e, wk string) {
			if strings.Contains(q, "wd:"+e+" ") || strings.Contains(q, "wd:"+e+"}") {
				bindings = append(bindings, map[string]map[string]string{
					"e": {"value": "http://www.wikidata.org/entity/" + e},
					"w": {"value": "http://www.wikidata.org/entity/" + wk},
				})
			}
		}
		if strings.Contains(q, "wdt:P629") {
			add(edition, work)
			add(omnibus, "Q1")
			add(omnibus, "Q2")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": map[string]any{"bindings": bindings}})
	}))
	defer srv.Close()
	p := NewWikidataAdaptationsProvider(nil).WithEndpoints(srv.URL+"/w/api.php", srv.URL+"/sparql", "")

	var collID, archID, authorID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO authors (last_name, normalized_name) VALUES ('Пьюзо', 'пьюзо') RETURNING id`).Scan(&authorID))
	mkWork := func(title, qid string, year int) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO works (title, normalized_title, primary_author_id, ext_ids, written_year, written_year_source)
			VALUES ($1, lower($1), $2, jsonb_build_object('wd_qid', $3::text), $4, 'wikidata') RETURNING id`,
			title, authorID, qid, year).Scan(&id))
		return id
	}
	mkBook := func(lib string, workID int64, qid string, year int) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id,
			                   written_year, written_year_source, adaptations_fetched_at, work_scanned_at)
			VALUES ($1,$2,$3::text,'f','fb2',$3::text,$3::text,$4,$5,'wikidata',now(),now()) RETURNING id`,
			collID, archID, lib, workID, year).Scan(&id))
		_, err := pool.Exec(ctx,
			`INSERT INTO book_work_lookups (book_id, source, outcome, work_key, checked_at) VALUES ($1,'wikidata','found',$2,now())`, id, qid)
		require.NoError(t, err)
		_, err = pool.Exec(ctx,
			`INSERT INTO book_year_lookups (book_id, source, outcome, year, checked_at) VALUES ($1,'wikidata','found',$2,now())`, id, year)
		require.NoError(t, err)
		return id
	}
	wEd := mkWork("The Godfather", edition, 2001)
	bEd := mkBook("ed", wEd, edition, 2001)
	wOK := mkWork("Dune", okWork, 1965)
	bOK := mkBook("ok", wOK, okWork, 1965)
	wOmni := mkWork("Omnibus", omnibus, 1990)
	bOmni := mkBook("om", wOmni, omnibus, 1990)
	for _, id := range []int64{wEd, wOK} {
		_, err := pool.Exec(ctx, `INSERT INTO work_renown_lookups (work_id, source, outcome, checked_at) VALUES ($1,'wikidata','found',now())`, id)
		require.NoError(t, err)
	}

	changed, editions, books, err := RepairEditionWorkKeys(ctx, pool, p, 0)
	require.NoError(t, err)
	require.Equal(t, 1, editions)
	require.EqualValues(t, 1, books)
	require.Equal(t, []int64{wEd}, changed, "год работы-издания пересчитан")

	keyOf := func(book int64) string {
		var k string
		require.NoError(t, pool.QueryRow(ctx, `SELECT work_key FROM book_work_lookups WHERE book_id=$1 AND source='wikidata'`, book).Scan(&k))
		return k
	}
	qidOf := func(w int64) string {
		var k string
		require.NoError(t, pool.QueryRow(ctx, `SELECT ext_ids->>'wd_qid' FROM works WHERE id=$1`, w).Scan(&k))
		return k
	}
	require.Equal(t, work, keyOf(bEd))
	require.Equal(t, work, qidOf(wEd))
	require.Equal(t, okWork, keyOf(bOK), "произведение не тронуто")
	require.Equal(t, omnibus, keyOf(bOmni), "омнибус не тронут")
	require.Equal(t, omnibus, qidOf(wOmni))

	var bookYear, workYear *int
	var adaptAt, scanAt *time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT b.written_year, w.written_year, b.adaptations_fetched_at, b.work_scanned_at FROM books b JOIN works w ON w.id=b.work_id WHERE b.id=$1`,
		bEd).Scan(&bookYear, &workYear, &adaptAt, &scanAt))
	require.Nil(t, bookYear, "год издания из Wikidata стёрт")
	require.Nil(t, workYear, "других источников года нет — год работы пуст до перезапроса")
	require.Nil(t, adaptAt, "экранизации перепросят")
	require.Nil(t, scanAt, "группировка перечитает ключ")
	count := func(sql string, args ...any) int {
		var n int
		require.NoError(t, pool.QueryRow(ctx, sql, args...).Scan(&n))
		return n
	}
	require.Zero(t, count(`SELECT count(*) FROM book_year_lookups WHERE book_id=$1`, bEd))
	require.Equal(t, 1, count(`SELECT count(*) FROM book_year_lookups WHERE book_id=$1`, bOK))
	require.Zero(t, count(`SELECT count(*) FROM work_renown_lookups WHERE work_id=$1`, wEd))
	require.Equal(t, 1, count(`SELECT count(*) FROM work_renown_lookups WHERE work_id=$1`, wOK))
	require.NoError(t, pool.QueryRow(ctx, `SELECT written_year FROM works WHERE id=$1`, wOK).Scan(&workYear))
	require.Equal(t, 1965, *workYear)

	// Повтор — изданий больше нет.
	_, editions, _, err = RepairEditionWorkKeys(ctx, pool, p, 0)
	require.NoError(t, err)
	require.Zero(t, editions)
}
