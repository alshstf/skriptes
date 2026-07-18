package metadata

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

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
	name  string
	code  string
	err   error
	calls int
}

func (f *fakeSrcLangProvider) Name() string { return f.name }
func (f *fakeSrcLangProvider) FetchSrcLang(context.Context, BookQuery) (string, error) {
	f.calls++
	return f.code, f.err
}

func TestSrcLangBackfiller_Integration(t *testing.T) {
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
	// пишем, lookup not_found (дозаполнять нечего).
	nativeBook := mkBook("L-native", "ru", true)
	ruProv := &fakeSrcLangProvider{name: "wikidata", code: "ru"}
	bf2 := NewSrcLangBackfiller(pool, ruProv, cfg, nil, quiet)
	bf2.drain(ctx)

	require.NoError(t, pool.QueryRow(ctx, `SELECT src_lang FROM books WHERE id=$1`, nativeBook).Scan(&sl))
	require.Nil(t, sl, "натив (оригинал = язык издания) src_lang не получает")
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT outcome FROM book_src_lang_lookups WHERE book_id=$1 AND source='wikidata'`, nativeBook).Scan(&outcome))
	require.Equal(t, "not_found", outcome)

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
	cov, err := ctl.Coverage(ctx)
	require.NoError(t, err)
	require.Equal(t, 4, cov.Total)
	require.Equal(t, 2, cov.WithSrcLang, "transBook + unscanned")
	require.Equal(t, 2, cov.BySource["wikidata"])
}
