package metadata

import (
	"context"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/testpg"
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

	pool := testpg.Pool(t, ctx)
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
	fl := &fakeRenownProvider{name: "fantlab", res: RenownResult{Ratings: 6724, Year: 2005, MidMark: 8.04, WeightedRating: 7.97, ExternalID: 4351}}
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
	var extYear, workYear *int
	require.NoError(t, pool.QueryRow(ctx, `SELECT external_year::int, written_year::int FROM works WHERE id=$1`, headWork).Scan(&extYear, &workYear))
	var midmark, flRating float64
	require.NoError(t, pool.QueryRow(ctx, `SELECT fantlab_midmark::float8, fantlab_rating::float8 FROM works WHERE id=$1`, headWork).Scan(&midmark, &flRating))
	require.InDelta(t, 8.04, midmark, 1e-4, "средняя оценка Фантлаба сохранена (#296)")
	require.InDelta(t, 7.97, flRating, 1e-4)
	require.NotNil(t, extYear)
	require.Equal(t, 2005, *extYear, "год Фантлаба — внешний год работы (#288)")
	require.NotNil(t, workYear)
	require.Equal(t, 2005, *workYear, "и год работы, раз у изданий года нет")
	var flID string
	require.NoError(t, pool.QueryRow(ctx, `SELECT ext_ids->>'fl_id' FROM works WHERE id=$1`, headWork).Scan(&flID))
	require.Equal(t, "4351", flID, "id работы Фантлаба в ext_ids (#412)")

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

// TestRenownBackfiller_WritesKind — типизация от Фантлаба: collection/anthology
// пишутся в works.kind (source='fantlab'), "novel" СНИМАЕТ ошибочную эвристику
// (kind → NULL), ручной override неприкосновенен.
func TestRenownBackfiller_WritesKind(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	var collID, archID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	mkWork := func(title string, kind, kindSource *string) int64 {
		var workID int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO works (title, normalized_title, edition_count, kind, kind_source)
			VALUES ($1, lower($1), 2, $2, $3) RETURNING id`,
			title, kind, kindSource).Scan(&workID))
		for i := 0; i < 2; i++ {
			_, err := pool.Exec(ctx, `
				INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id)
				VALUES ($1,$2,$3||$4,'f','fb2',$3,lower($3),$5)`,
				collID, archID, title, strconv.Itoa(i), workID)
			require.NoError(t, err)
		}
		return workID
	}
	kindOf := func(id int64) (kind, source string) {
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT COALESCE(kind,''), COALESCE(kind_source,'') FROM works WHERE id=$1`, id).Scan(&kind, &source))
		return kind, source
	}
	run := func(res RenownResult) *RenownBackfiller {
		fl := &fakeRenownProvider{name: "fantlab", res: res}
		return NewRenownBackfiller(pool, fl, nil, nil, &fakeWorksSyncer{},
			RenownBackfillConfig{Fantlab: true, FoundRefreshDays: 180, NotFoundRetryDays: 90, ErrorRetryHours: 24}, quiet)
	}

	// 1. Фантлаб говорит «сборник» → kind=collection, source=fantlab.
	w1 := mkWork("Лавка миров", nil, nil)
	run(RenownResult{Ratings: 310, Kind: "collection"}).drain(ctx)
	k, src := kindOf(w1)
	require.Equal(t, "collection", k)
	require.Equal(t, "fantlab", src)

	// 2. Эвристика ошиблась (heuristic-метка), Фантлаб уверен «роман» →
	// kind СНИМАЕТСЯ (NULL), source=fantlab (эвристика больше не вернёт).
	h, hs := "omnibus", "heuristic"
	w2 := mkWork("Ложно помеченный роман", &h, &hs)
	run(RenownResult{Ratings: 42, Kind: "novel"}).drain(ctx)
	k, src = kindOf(w2)
	require.Empty(t, k, "novel от Фантлаба снимает ошибочную эвристику")
	require.Equal(t, "fantlab", src)

	// 3. Ручной override неприкосновенен.
	o, os := "anthology", "override"
	w3 := mkWork("Руками помеченная антология", &o, &os)
	run(RenownResult{Ratings: 7, Kind: "novel"}).drain(ctx)
	k, src = kindOf(w3)
	require.Equal(t, "anthology", k, "override не перетирается фантлабом")
	require.Equal(t, "override", src)

	// 4. Kind="" (цикл/статья/незнакомый тип) — ничего не трогает.
	w4 := mkWork("Цикл без решения", &h, &hs)
	run(RenownResult{Ratings: 5, Kind: ""}).drain(ctx)
	k, src = kindOf(w4)
	require.Equal(t, "omnibus", k, "пустой Kind не трогает существующую метку")
	require.Equal(t, "heuristic", src)
}

// OpenLibrary у «Известности» не спрашивается о работе, чьё издание он не найдёт
// (русское, без ISBN и названия оригинала): исход skipped окончательный, сброс
// неудачных попыток его снимает (#294). Фантлаб спрашивается как обычно.
func TestRenownBackfiller_SkipsOpenLibraryForUnfindable(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	var collID, archID, workID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO works (title, normalized_title, edition_count) VALUES ('Тихий Дон', 'тихий дон', 2) RETURNING id`).Scan(&workID))
	for _, lib := range []string{"td1", "td2"} {
		_, err := pool.Exec(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, lang, work_id)
			VALUES ($1,$2,$3,'f','fb2','Тихий Дон','тихий дон','ru',$4)`, collID, archID, lib, workID)
		require.NoError(t, err)
	}

	fl := &fakeRenownProvider{name: "fantlab", res: RenownResult{Ratings: 1000}}
	ol := &fakeRenownProvider{name: "openlibrary", res: RenownResult{Ratings: 5}}
	cfg := RenownBackfillConfig{Fantlab: true, OpenLibrary: true, FoundRefreshDays: 180, NotFoundRetryDays: 90, ErrorRetryHours: 24}
	bf := NewRenownBackfiller(pool, fl, ol, nil, nil, cfg, quiet)
	bf.drain(ctx)
	require.Equal(t, 1, fl.callCount(), "Фантлаб спрошен")
	require.Zero(t, ol.callCount(), "OpenLibrary не спрошен")
	var outcome string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT outcome FROM work_renown_lookups WHERE work_id = $1 AND source = 'openlibrary'`, workID).Scan(&outcome))
	require.Equal(t, outcomeSkipped, outcome)

	require.Zero(t, bf.drain(ctx), "skipped и found — работа больше не кандидат")

	ctl := NewRenownBackfillController(pool, fl, ol, nil, nil, cfg, quiet)
	_, err := ctl.ResetFailedLookups(ctx)
	require.NoError(t, err)
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM work_renown_lookups WHERE work_id = $1 AND source = 'openlibrary'`, workID).Scan(&n))
	require.Zero(t, n, "сброс неудачных попыток снимает skipped")
}

// TestRenownBackfiller_StaleCounters — #408: работа вне ядра с уже записанными
// счётчиками освежается; «не найдено» сбрасывает счётчики источника (а сбой —
// нет); разовая чистка сбрасывает счётчики, по которым источник уже ответил
// «не найдено».
func TestRenownBackfiller_StaleCounters(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	var collID, archID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	mkWork := func(title string, editions int) int64 {
		var workID int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO works (title, normalized_title, edition_count) VALUES ($1, lower($1), $2) RETURNING id`,
			title, editions).Scan(&workID))
		for i := 0; i < editions; i++ {
			_, err := pool.Exec(ctx, `
				INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id)
				VALUES ($1,$2,$3||$4,'f','fb2',$3,lower($3),$5)`, collID, archID, title, strconv.Itoa(i), workID)
			require.NoError(t, err)
		}
		return workID
	}
	marks := func(id int64) (fl *int, mid *float64) {
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT fantlab_marks, fantlab_midmark::float8 FROM works WHERE id=$1`, id).Scan(&fl, &mid))
		return fl, mid
	}
	cfg := RenownBackfillConfig{Fantlab: true, FoundRefreshDays: 180, NotFoundRetryDays: 90, ErrorRetryHours: 24}

	// Синглтон вне ядра, но со старым числом оценок без средней (как 1,5 тыс. на
	// проде) — кандидат, средняя приходит.
	outside := mkWork("Вне ядра", 1)
	_, err := pool.Exec(ctx, `UPDATE works SET fantlab_marks = 40 WHERE id = $1`, outside)
	require.NoError(t, err)
	syncer := &fakeWorksSyncer{}
	fl := &fakeRenownProvider{name: "fantlab", res: RenownResult{Ratings: 42, MidMark: 7.5}}
	NewRenownBackfiller(pool, fl, nil, nil, syncer, cfg, quiet).drain(ctx)
	got, mid := marks(outside)
	require.NotNil(t, got)
	require.Equal(t, 42, *got)
	require.NotNil(t, mid)
	require.InDelta(t, 7.5, *mid, 1e-4, "работа с записанными счётчиками освежается и вне ядра")

	// Сбой источника счётчики не трогает.
	core := mkWork("Ядро", 2)
	_, err = pool.Exec(ctx, `UPDATE works SET fantlab_marks = 100, fantlab_midmark = 8 WHERE id = $1`, core)
	require.NoError(t, err)
	failing := &fakeRenownProvider{name: "fantlab", err: ErrUpstream}
	NewRenownBackfiller(pool, failing, nil, nil, syncer, cfg, quiet).drain(ctx)
	got, _ = marks(core)
	require.NotNil(t, got, "сбой — не повод сбрасывать")

	// «Не найдено» — сбрасывает и отправляет работу в ресинк индекса.
	_, err = pool.Exec(ctx, `DELETE FROM work_renown_lookups WHERE work_id = $1`, core)
	require.NoError(t, err)
	syncer2 := &fakeWorksSyncer{}
	nf := &fakeRenownProvider{name: "fantlab"}
	NewRenownBackfiller(pool, nf, nil, nil, syncer2, cfg, quiet).drain(ctx)
	got, mid = marks(core)
	require.Nil(t, got, "источник не нашёл работу — его счётчики сброшены")
	require.Nil(t, mid)
	require.Contains(t, syncer2.upserted, core, "известность изменилась — в ресинк")

	// Разовая чистка: OpenLibrary раньше ответил «не найдено», а счётчики остались.
	stale := mkWork("Устаревшая", 2)
	_, err = pool.Exec(ctx, `UPDATE works SET ol_ratings_count = 5, ol_want_count = 9, fantlab_marks = 30,
		ext_ids = ext_ids || '{"fl_id": 77}' WHERE id = $1`, stale)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO work_renown_lookups (work_id, source, outcome, checked_at)
		VALUES ($1, 'openlibrary', 'not_found', now()), ($1, 'fantlab', 'found', now())`, stale)
	require.NoError(t, err)
	changed, err := ClearStaleRenown(ctx, pool)
	require.NoError(t, err)
	require.Contains(t, changed, stale)
	var olr, olw, flm *int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT ol_ratings_count, ol_want_count, fantlab_marks FROM works WHERE id=$1`, stale).Scan(&olr, &olw, &flm))
	require.Nil(t, olr)
	require.Nil(t, olw)
	require.NotNil(t, flm, "Фантлаб нашёл — его счётчики на месте")
	var flKept bool
	require.NoError(t, pool.QueryRow(ctx, `SELECT ext_ids ? 'fl_id' FROM works WHERE id=$1`, stale).Scan(&flKept))
	require.True(t, flKept, "и id Фантлаба тоже")
	again, err := ClearStaleRenown(ctx, pool)
	require.NoError(t, err)
	require.Empty(t, again, "повтор ничего не меняет")
}
