package metadata

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ── isDue (pure) ────────────────────────────────────────────────

func TestRenownBackfiller_isDue(t *testing.T) {
	b := &RenownBackfiller{cfg: RenownBackfillConfig{FoundRefreshDays: 180, NotFoundRetryDays: 90, ErrorRetryHours: 24}}
	now := time.Now()

	require.True(t, b.isDue(lookupRow{}, now), "нет строки → спрашиваем")
	require.False(t, b.isDue(lookupRow{outcome: "found", checkedAt: now.Add(-30 * 24 * time.Hour)}, now),
		"found свежий (30д < 180д) → не освежаем")
	require.True(t, b.isDue(lookupRow{outcome: "found", checkedAt: now.Add(-200 * 24 * time.Hour)}, now),
		"found старый (200д > 180д) → освежаем: известность растёт")
	require.True(t, b.isDue(lookupRow{outcome: "not_found", checkedAt: now.Add(-100 * 24 * time.Hour)}, now))
	require.False(t, b.isDue(lookupRow{outcome: "not_found", checkedAt: now.Add(-10 * 24 * time.Hour)}, now))
	require.True(t, b.isDue(lookupRow{outcome: "error", checkedAt: now.Add(-48 * time.Hour)}, now))

	noRefresh := &RenownBackfiller{cfg: RenownBackfillConfig{FoundRefreshDays: 0}}
	require.False(t, noRefresh.isDue(lookupRow{outcome: "found", checkedAt: now.Add(-1000 * 24 * time.Hour)}, now),
		"FoundRefreshDays=0 → found не освежается")
}

// ── фейки ───────────────────────────────────────────────────────

type fakeRenownProvider struct {
	name string
	res  RenownResult // total()>0 → found; иначе ErrNotFound
	err  error

	mu    sync.Mutex
	calls int
	qids  []string // WikidataQID из входящих запросов (проверка хинта)
}

func (f *fakeRenownProvider) Name() string { return f.name }
func (f *fakeRenownProvider) FetchRenown(_ context.Context, q WorkQuery) (RenownResult, error) {
	f.mu.Lock()
	f.calls++
	f.qids = append(f.qids, q.WikidataQID)
	f.mu.Unlock()
	if f.err != nil {
		return RenownResult{}, f.err
	}
	if f.res.total() <= 0 {
		return RenownResult{}, ErrNotFound
	}
	return f.res, nil
}

func (f *fakeRenownProvider) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// fakeWorksSyncer записывает таргетные upsert'ы works-индекса.
type fakeWorksSyncer struct {
	mu       sync.Mutex
	upserted []int64
}

func (f *fakeWorksSyncer) UpsertWorksToIndex(_ context.Context, ids []int64) error {
	f.mu.Lock()
	f.upserted = append(f.upserted, ids...)
	f.mu.Unlock()
	return nil
}
func (f *fakeWorksSyncer) DeleteWorksFromIndex(context.Context, []int64) error { return nil }

// ── Worker integration (testcontainers PG + фейковые провайдеры) ──

func TestRenownBackfiller_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := startPGForPrewarm(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	var collID, archID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))

	// mkWork — работа + N изданий (edition_count определяет ядро).
	mkWork := func(title string, editions int, rating *int) int64 {
		var workID int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO works (title, normalized_title, edition_count) VALUES ($1, lower($1), $2) RETURNING id`,
			title, editions).Scan(&workID))
		for i := 0; i < editions; i++ {
			_, err := pool.Exec(ctx, `
				INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, rating, work_id)
				VALUES ($1,$2,$3||$4,'f','fb2',$3,lower($3),$5,$6)`,
				collID, archID, title, strconv.Itoa(i), rating, workID)
			require.NoError(t, err)
		}
		return workID
	}
	readCounters := func(id int64) (fl, olr, olw *int) {
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT fantlab_marks, ol_ratings_count, ol_want_count FROM works WHERE id=$1`, id).Scan(&fl, &olr, &olw))
		return fl, olr, olw
	}

	// «Голова»: 2 издания → кандидат; оба источника находят → обе группы колонок
	// заполнены, работа таргетно ушла в ресинк индекса.
	headWork := mkWork("Метро 2033", 2, nil)
	fl := &fakeRenownProvider{name: "fantlab", res: RenownResult{Ratings: 6724}}
	ol := &fakeRenownProvider{name: "openlibrary", res: RenownResult{Ratings: 36, Want: 302}}
	syncer := &fakeWorksSyncer{}
	bf := NewRenownBackfiller(pool, fl, ol, nil, syncer,
		RenownBackfillConfig{Fantlab: true, OpenLibrary: true, FoundRefreshDays: 180, NotFoundRetryDays: 90, ErrorRetryHours: 24}, quiet)
	require.Equal(t, 1, bf.drain(ctx), "кандидат только head-работа")
	gotFl, gotOlr, gotOlw := readCounters(headWork)
	require.NotNil(t, gotFl)
	require.Equal(t, 6724, *gotFl)
	require.NotNil(t, gotOlr)
	require.Equal(t, 36, *gotOlr)
	require.NotNil(t, gotOlw)
	require.Equal(t, 302, *gotOlw)
	require.Contains(t, syncer.upserted, headWork, "найденное — таргетный ресинк works-индекса")

	// found не перепрашивается на следующем проходе (TTL 180д).
	callsBefore := fl.callCount()
	bf.drain(ctx)
	require.Equal(t, callsBefore, fl.callCount(), "свежий found не переспрашивается")

	// Безвестный синглтон — НЕ кандидат в режиме ядра…
	tailWork := mkWork("Безвестная книга", 1, nil)
	fl2 := &fakeRenownProvider{name: "fantlab", res: RenownResult{Ratings: 5}}
	bf2 := NewRenownBackfiller(pool, fl2, nil, nil, syncer,
		RenownBackfillConfig{Fantlab: true, FoundRefreshDays: 180, NotFoundRetryDays: 90, ErrorRetryHours: 24}, quiet)
	bf2.drain(ctx)
	gotFl, _, _ = readCounters(tailWork)
	require.Nil(t, gotFl, "ядро: синглтон без сигналов не кандидат")

	// …но кандидат при LIBRATE-рейтинге издания.
	five := 5
	libWork := mkWork("Книга с LIBRATE", 1, &five)
	bf2.drain(ctx)
	gotFl, _, _ = readCounters(libWork)
	require.NotNil(t, gotFl, "LIBRATE-издание делает работу кандидатом ядра")
	require.Equal(t, 5, *gotFl)

	// «Вся коллекция» — берёт и безвестный синглтон.
	bf3 := NewRenownBackfiller(pool, fl2, nil, nil, syncer,
		RenownBackfillConfig{Fantlab: true, WholeCollection: true, FoundRefreshDays: 180, NotFoundRetryDays: 90, ErrorRetryHours: 24}, quiet)
	bf3.drain(ctx)
	gotFl, _, _ = readCounters(tailWork)
	require.NotNil(t, gotFl, "вся коллекция: синглтон стал кандидатом")

	// Wikidata: sitelinks пишутся в свою колонку, готовый QID из ext_ids
	// доезжает до провайдера хинтом (резолв пропускается).
	wdWork := mkWork("Мастер и Маргарита", 2, nil)
	_, err := pool.Exec(ctx,
		`UPDATE works SET ext_ids = '{"wd_qid":"Q188538"}'::jsonb WHERE id = $1`, wdWork)
	require.NoError(t, err)
	wd := &fakeRenownProvider{name: "wikidata", res: RenownResult{Sitelinks: 78}}
	bf5 := NewRenownBackfiller(pool, nil, nil, wd, syncer,
		RenownBackfillConfig{Wikidata: true, FoundRefreshDays: 180, NotFoundRetryDays: 90, ErrorRetryHours: 24}, quiet)
	bf5.drain(ctx)
	var sitelinks *int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT wd_sitelinks FROM works WHERE id=$1`, wdWork).Scan(&sitelinks))
	require.NotNil(t, sitelinks)
	require.Equal(t, 78, *sitelinks)
	require.Contains(t, wd.qids, "Q188538", "QID из ext_ids передаётся провайдеру хинтом")

	// not_found помечается и не долбится повторно.
	nfWork := mkWork("Не найдётся", 2, nil)
	nf := &fakeRenownProvider{name: "fantlab"} // нулевой результат → ErrNotFound
	bf4 := NewRenownBackfiller(pool, nf, nil, nil, syncer,
		RenownBackfillConfig{Fantlab: true, FoundRefreshDays: 180, NotFoundRetryDays: 90, ErrorRetryHours: 24}, quiet)
	bf4.drain(ctx)
	var outcome string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT outcome FROM work_renown_lookups WHERE work_id=$1 AND source='fantlab'`, nfWork).Scan(&outcome))
	require.Equal(t, "not_found", outcome)
	nfCalls := nf.callCount()
	bf4.drain(ctx)
	require.Equal(t, nfCalls, nf.callCount(), "свежий not_found не переспрашивается")
}
