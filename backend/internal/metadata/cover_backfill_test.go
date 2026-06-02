package metadata

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ── isDue (pure) ────────────────────────────────────────────────

func TestCoverBackfiller_isDue(t *testing.T) {
	b := &CoverBackfiller{cfg: CoverBackfillConfig{NotFoundRetryDays: 90, ErrorRetryHours: 24}}
	now := time.Now()

	require.True(t, b.isDue(lookupRow{}, now), "нет строки → спрашиваем")
	require.False(t, b.isDue(lookupRow{outcome: "found", checkedAt: now}, now), "found → не спрашиваем")
	require.False(t, b.isDue(lookupRow{outcome: "not_found", checkedAt: now.Add(-10 * 24 * time.Hour)}, now),
		"not_found свежий (10д < 90д) → не спрашиваем")
	require.True(t, b.isDue(lookupRow{outcome: "not_found", checkedAt: now.Add(-100 * 24 * time.Hour)}, now),
		"not_found старый (100д > 90д) → перепроверяем")
	require.False(t, b.isDue(lookupRow{outcome: "error", checkedAt: now.Add(-1 * time.Hour)}, now),
		"error свежий (1ч < 24ч) → не спрашиваем")
	require.True(t, b.isDue(lookupRow{outcome: "error", checkedAt: now.Add(-48 * time.Hour)}, now),
		"error старый (48ч > 24ч) → ретраим")
}

// ── candidateCond (pure) ────────────────────────────────────────

func TestCoverBackfiller_candidateCond(t *testing.T) {
	fallback := &CoverBackfiller{cfg: CoverBackfillConfig{WholeCollection: false}}
	require.Equal(t, "b.cover_path IS NULL AND b.metadata_fetched_at IS NOT NULL", fallback.candidateCond(),
		"фолбэк: только книги, прошедшие локальную fb2-фазу")

	whole := &CoverBackfiller{cfg: CoverBackfillConfig{WholeCollection: true}}
	require.Equal(t, "b.cover_path IS NULL", whole.candidateCond(),
		"вся коллекция: любые книги без обложки")
}

// ── фейковый внешний cover-провайдер ────────────────────────────

type fakeCoverProvider struct {
	name  string
	img   []byte // непустой → возвращаем обложку; пустой → ErrNotFound
	calls int
}

func (f *fakeCoverProvider) Name() string { return f.name }
func (f *fakeCoverProvider) FetchCover(context.Context, BookQuery) (*CoverImage, error) {
	f.calls++
	if len(f.img) == 0 {
		return nil, ErrNotFound
	}
	return &CoverImage{Reader: io.NopCloser(bytes.NewReader(f.img)), Mime: "image/jpeg"}, nil
}

// ── Worker integration (testcontainers PG + фейковый провайдер) ──

func TestCoverBackfiller_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := startPGForPrewarm(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	enricher, err := New(pool, t.TempDir(), nil, nil, nil, nil, nil, quiet)
	require.NoError(t, err)

	// Сид: collection → archive → книги. Локальная fb2-фаза прошла
	// (metadata_fetched_at NOT NULL), cover_path пуст.
	var collID, archID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))

	mkBook := func(lib string, localDone bool) int64 {
		var id int64
		var marker any
		if localDone {
			marker = time.Now()
		}
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, metadata_fetched_at)
			VALUES ($1,$2,$3,'f','fb2','T','t', $4) RETURNING id`,
			collID, archID, lib, marker).Scan(&id))
		return id
	}

	// found: внешний провайдер вернул обложку → cover_path проставлен, lookup found.
	foundBook := mkBook("L-found", true)
	okProv := &fakeCoverProvider{name: "openlibrary", img: []byte("JPEGBYTES")}
	bf := NewCoverBackfiller(pool, enricher, okProv, nil,
		CoverBackfillConfig{OpenLibrary: true, OpenLibraryRPM: 0, NotFoundRetryDays: 90, ErrorRetryHours: 24}, quiet)
	require.Equal(t, 1, bf.drain(ctx), "один кандидат обработан")

	var cp *string
	require.NoError(t, pool.QueryRow(ctx, `SELECT cover_path FROM books WHERE id=$1`, foundBook).Scan(&cp))
	require.NotNil(t, cp, "cover_path должен быть проставлен")
	require.NotEmpty(t, *cp)

	var outcome string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT outcome FROM book_cover_lookups WHERE book_id=$1 AND source='openlibrary'`, foundBook).Scan(&outcome))
	require.Equal(t, "found", outcome)

	// not_found: провайдер ничего не вернул → cover_path NULL, lookup not_found.
	nfBook := mkBook("L-nf", true)
	nfProv := &fakeCoverProvider{name: "openlibrary"} // img пуст → ErrNotFound
	bf2 := NewCoverBackfiller(pool, enricher, nfProv, nil,
		CoverBackfillConfig{OpenLibrary: true, OpenLibraryRPM: 0, NotFoundRetryDays: 90, ErrorRetryHours: 24}, quiet)
	bf2.drain(ctx)

	var cpNF *string
	require.NoError(t, pool.QueryRow(ctx, `SELECT cover_path FROM books WHERE id=$1`, nfBook).Scan(&cpNF))
	require.Nil(t, cpNF, "not_found → cover_path остаётся пустым")
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT outcome FROM book_cover_lookups WHERE book_id=$1 AND source='openlibrary'`, nfBook).Scan(&outcome))
	require.Equal(t, "not_found", outcome)

	// Повторный проход не переспрашивает свежий not_found (TTL).
	callsBefore := nfProv.calls
	bf2.drain(ctx)
	require.Equal(t, callsBefore, nfProv.calls, "свежий not_found не перепрашивается (TTL)")

	// Фолбэк-режим НЕ берёт книгу без локальной фазы (metadata_fetched_at NULL).
	rawBook := mkBook("L-raw", false)
	rawProv := &fakeCoverProvider{name: "openlibrary", img: []byte("X")}
	bf3 := NewCoverBackfiller(pool, enricher, rawProv, nil,
		CoverBackfillConfig{OpenLibrary: true, OpenLibraryRPM: 0, NotFoundRetryDays: 90, ErrorRetryHours: 24}, quiet)
	bf3.drain(ctx)
	require.Equal(t, 0, rawProv.calls, "фолбэк: книга без metadata_fetched_at не кандидат")

	// А в режиме «вся коллекция» — берёт.
	bf4 := NewCoverBackfiller(pool, enricher, rawProv, nil,
		CoverBackfillConfig{OpenLibrary: true, WholeCollection: true, OpenLibraryRPM: 0, NotFoundRetryDays: 90, ErrorRetryHours: 24}, quiet)
	bf4.drain(ctx)
	require.Greater(t, rawProv.calls, 0, "вся коллекция: книга без локальной фазы становится кандидатом")
	var cpRaw *string
	require.NoError(t, pool.QueryRow(ctx, `SELECT cover_path FROM books WHERE id=$1`, rawBook).Scan(&cpRaw))
	require.NotNil(t, cpRaw, "вся коллекция: обложка проставлена")
}
