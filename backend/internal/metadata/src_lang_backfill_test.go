package metadata

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// ── isDue (pure) ────────────────────────────────────────────────

func TestSrcLangBackfiller_isDue(t *testing.T) {
	b := &SrcLangBackfiller{cfg: SrcLangBackfillConfig{NotFoundRetryDays: 90, ErrorRetryHours: 24}}
	now := time.Now()

	require.True(t, b.isDue(lookupRow{}, now), "нет строки → спрашиваем")
	require.False(t, b.isDue(lookupRow{outcome: "found", checkedAt: now}, now), "found → не спрашиваем")
	require.False(t, b.isDue(lookupRow{outcome: "not_found", checkedAt: now.Add(-10 * 24 * time.Hour)}, now))
	require.True(t, b.isDue(lookupRow{outcome: "not_found", checkedAt: now.Add(-100 * 24 * time.Hour)}, now))
	require.True(t, b.isDue(lookupRow{outcome: "error", checkedAt: now.Add(-48 * time.Hour)}, now))
}

// ── Worker integration (testcontainers PG + фейковый провайдер) ──

type fakeSrcLangProvider struct {
	mu      sync.Mutex // воркер зовёт провайдер из нескольких горутин (race в CI)
	name    string
	code    string
	err     error
	calls   int
	queries []BookQuery
}

func (f *fakeSrcLangProvider) Name() string { return f.name }
func (f *fakeSrcLangProvider) FetchSrcLang(_ context.Context, q BookQuery) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.queries = append(f.queries, q)
	return f.code, f.err
}

func TestSrcLangBackfiller_Integration(t *testing.T) {
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

	// mkBook: локальный edition-скан прошёл (edition_meta_scanned_at NOT NULL),
	// src_lang пустой — кандидат фолбэк-режима.
	mkBook := func(lib, lang string, scanned bool) int64 {
		var id int64
		scannedSQL := "NULL"
		if scanned {
			scannedSQL = "now()"
		}
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, lang, edition_meta_scanned_at)
			VALUES ($1,$2,$3,'f','fb2','T','t',$4,`+scannedSQL+`) RETURNING id`,
			collID, archID, lib, lang).Scan(&id))
		return id
	}

	// 1. Перевод: провайдер даёт fr, издание ru → src_lang записан, lookup found.
	transBook := mkBook("L-trans", "ru", true)
	frProv := &fakeSrcLangProvider{name: "wikidata", code: "fr"}
	cfg := SrcLangBackfillConfig{Wikidata: true, WikidataRPM: 0, NotFoundRetryDays: 90, ErrorRetryHours: 24}
	bf := NewSrcLangBackfiller(pool, frProv, cfg, nil, quiet)
	require.Equal(t, 1, bf.drain(ctx))

	var sl *string
	require.NoError(t, pool.QueryRow(ctx, `SELECT src_lang FROM books WHERE id=$1`, transBook).Scan(&sl))
	require.NotNil(t, sl)
	require.Equal(t, "fr", *sl)
	var outcome string
	var lcode *string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT outcome, src_lang FROM book_src_lang_lookups WHERE book_id=$1 AND source='wikidata'`, transBook).
		Scan(&outcome, &lcode))
	require.Equal(t, "found", outcome)
	require.NotNil(t, lcode)
	require.Equal(t, "fr", *lcode)

	// 2. Гейт записи: провайдер даёт ru при издании ru (натив) → src_lang НЕ
	// пишем, lookup native — окончательный, не перепрашивается (#294).
	nativeBook := mkBook("L-native", "ru", true)
	ruProv := &fakeSrcLangProvider{name: "wikidata", code: "ru"}
	bf2 := NewSrcLangBackfiller(pool, ruProv, cfg, nil, quiet)
	bf2.drain(ctx)

	require.NoError(t, pool.QueryRow(ctx, `SELECT src_lang FROM books WHERE id=$1`, nativeBook).Scan(&sl))
	require.Nil(t, sl, "натив (оригинал = язык издания) src_lang не получает")
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT outcome FROM book_src_lang_lookups WHERE book_id=$1 AND source='wikidata'`, nativeBook).Scan(&outcome))
	require.Equal(t, outcomeNative, outcome)
	_, err := pool.Exec(ctx, `UPDATE book_src_lang_lookups SET checked_at = now() - interval '5 years' WHERE book_id=$1`, nativeBook)
	require.NoError(t, err)
	nativeCalls := ruProv.calls
	bf2.drain(ctx)
	require.Equal(t, nativeCalls, ruProv.calls, "native не перепрашивается и через годы")

	// 3. Не сканированная fb2-фазой книга — НЕ кандидат фолбэк-режима.
	unscanned := mkBook("L-unscanned", "ru", false)
	callsBefore := ruProv.calls
	bf2.drain(ctx)
	require.Equal(t, callsBefore, ruProv.calls, "unscanned не кандидат (и native под свежим TTL)")
	require.NoError(t, pool.QueryRow(ctx, `SELECT src_lang FROM books WHERE id=$1`, unscanned).Scan(&sl))
	require.Nil(t, sl)

	// ...но кандидат в режиме «вся коллекция».
	wholeCfg := cfg
	wholeCfg.WholeCollection = true
	bf3 := NewSrcLangBackfiller(pool, frProv, wholeCfg, nil, quiet)
	bf3.drain(ctx)
	require.NoError(t, pool.QueryRow(ctx, `SELECT src_lang FROM books WHERE id=$1`, unscanned).Scan(&sl))
	require.NotNil(t, sl, "whole-collection берёт и не сканированные")
	require.Equal(t, "fr", *sl)

	// 4. ErrNotFound → not_found, свежий TTL не перепрашивается.
	nfBook := mkBook("L-nf", "ru", true)
	nfProv := &fakeSrcLangProvider{name: "wikidata", err: ErrNotFound}
	bf4 := NewSrcLangBackfiller(pool, nfProv, cfg, nil, quiet)
	bf4.drain(ctx)
	require.NoError(t, pool.QueryRow(ctx, `SELECT src_lang FROM books WHERE id=$1`, nfBook).Scan(&sl))
	require.Nil(t, sl)
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT outcome FROM book_src_lang_lookups WHERE book_id=$1 AND source='wikidata'`, nfBook).Scan(&outcome))
	require.Equal(t, "not_found", outcome)
	nfCalls := nfProv.calls
	bf4.drain(ctx)
	require.Equal(t, nfCalls, nfProv.calls, "свежий not_found не перепрашивается (TTL)")

	// 5. Coverage: 3 книги с src_lang (transBook + unscanned + ... проверим точно),
	// by_source wikidata ≥ 2 (found-строки transBook и unscanned).
	ctl := NewSrcLangBackfillController(pool, frProv, cfg, nil, quiet)
	cov, cerr := ctl.Coverage(ctx)
	err = cerr
	require.NoError(t, err)
	require.Equal(t, 4, cov.Total)
	require.Equal(t, 2, cov.WithSrcLang, "transBook + unscanned")
	require.Equal(t, 2, cov.BySource["wikidata"])
}

// Переиспользование того, что Wikidata-пути уже знают о книге (#294): QID работы
// уходит в запрос; свежее «не найдено» Tier-2 группировки по книге без названия
// оригинала — ответ без запроса; с названием оригинала или устаревшее — запрос.
func TestSrcLangBackfiller_ReusesWikidataKnowledge(t *testing.T) {
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
	mkBook := func(lib, srcTitle, extIDs string) int64 {
		var wid, id int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO works (title, normalized_title, ext_ids) VALUES ($1, $2, $3::jsonb) RETURNING id`, lib, lib, extIDs).Scan(&wid))
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, lang,
			                   src_title, edition_meta_scanned_at, work_id)
			VALUES ($1,$2,$3,'f','fb2',$4,$5,'ru',NULLIF($6,''),now(),$7) RETURNING id`,
			collID, archID, lib, lib, lib, srcTitle, wid).Scan(&id))
		return id
	}
	groupingNotFound := func(bookID int64, age string) {
		_, err := pool.Exec(ctx, `INSERT INTO book_work_lookups (book_id, source, outcome, checked_at)
			VALUES ($1, 'wikidata', 'not_found', now() - $2::interval)`, bookID, age)
		require.NoError(t, err)
	}
	mkBook("with-qid", "", `{"wd_qid":"Q42"}`)
	reused := mkBook("reused", "", `{}`)
	groupingNotFound(reused, "10 days")
	groupingNotFound(mkBook("with-src", "Original", `{}`), "10 days")
	// Работа из одного издания — хвост: «не найдено» живёт год (tailNotFoundTTL).
	groupingNotFound(mkBook("stale", "", `{}`), "400 days")

	prov := &fakeSrcLangProvider{name: "wikidata", err: ErrNotFound}
	cfg := SrcLangBackfillConfig{Wikidata: true, NotFoundRetryDays: 90, ErrorRetryHours: 24}
	NewSrcLangBackfiller(pool, prov, cfg, nil, quiet).drain(ctx)

	asked := map[string]BookQuery{}
	for _, q := range prov.queries {
		asked[q.Title] = q
	}
	require.Len(t, prov.queries, 3, "свежее «не найдено» группировки — без запроса")
	require.Equal(t, "Q42", asked["with-qid"].WikidataQID, "QID работы уходит в запрос")
	require.Contains(t, asked, "Original", "с названием оригинала запрос другой — спрашиваем")
	require.Contains(t, asked, "stale", "устаревшее «не найдено» группировки не переиспользуем")

	var outcome string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT outcome FROM book_src_lang_lookups WHERE book_id=$1 AND source='wikidata'`, reused).Scan(&outcome))
	require.Equal(t, "not_found", outcome)
}

func TestIsQID(t *testing.T) {
	for s, want := range map[string]bool{
		"Q42": true, "Q1": true, "": false, "Q": false, "q42": false, "Q42 ": false,
		"Q42} . ?x wdt:P31 ?y": false, "P31": false,
	} {
		require.Equal(t, want, isQID(s), s)
	}
}

// wikidataShortcut — чистые правила переиспользования (#294).
func TestYearCandidate_WikidataShortcut(t *testing.T) {
	now := time.Now()
	fresh := now.Add(-10 * 24 * time.Hour)
	old := now.Add(-100 * 24 * time.Hour)
	ttl := 90 * 24 * time.Hour

	var q BookQuery
	require.False(t, yearCandidate{wdQID: "Q7", wdNotFoundAt: &fresh}.wikidataShortcut(&q, ttl, now), "QID важнее «не найдено»")
	require.Equal(t, "Q7", q.WikidataQID)

	q = BookQuery{}
	require.True(t, yearCandidate{wdNotFoundAt: &fresh}.wikidataShortcut(&q, ttl, now))
	require.False(t, yearCandidate{wdNotFoundAt: &old}.wikidataShortcut(&q, ttl, now), "устаревшее — спрашиваем")
	require.False(t, yearCandidate{srcTitle: "Orig", wdNotFoundAt: &fresh}.wikidataShortcut(&q, ttl, now), "другой запрос")
	require.False(t, yearCandidate{wdQID: "bogus"}.wikidataShortcut(&q, ttl, now))
	require.Empty(t, q.WikidataQID, "чужое значение ext_ids в запрос не идёт")
}

func TestLookupTTL_ForPhase(t *testing.T) {
	ttl := retryTTL(90, 24)
	require.Equal(t, 90*24*time.Hour, ttl.forPhase(corePhases[0]).notFound, "ядро — срок из настроек")
	require.Equal(t, tailNotFoundTTL, ttl.forPhase(corePhases[1]).notFound, "хвост — не чаще раза в год")
	require.Equal(t, 24*time.Hour, ttl.forPhase(corePhases[1]).error, "срок ошибок не меняется")
	long := retryTTL(500, 24)
	require.Equal(t, 500*24*time.Hour, long.forPhase(corePhases[1]).notFound, "срок длиннее года не укорачиваем")
}
