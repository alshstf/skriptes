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

func TestExternalRatingBackfiller_isDue(t *testing.T) {
	b := &ExternalRatingBackfiller{cfg: ExternalRatingBackfillConfig{NotFoundRetryDays: 90, ErrorRetryHours: 24}}
	now := time.Now()

	require.True(t, b.isDue(lookupRow{}, now), "нет строки → спрашиваем")
	require.False(t, b.isDue(lookupRow{outcome: "found", checkedAt: now}, now), "found → не спрашиваем")
	require.False(t, b.isDue(lookupRow{outcome: "not_found", checkedAt: now.Add(-10 * 24 * time.Hour)}, now),
		"not_found свежий (10д < 90д) → не спрашиваем")
	require.True(t, b.isDue(lookupRow{outcome: "not_found", checkedAt: now.Add(-100 * 24 * time.Hour)}, now),
		"not_found старый (100д > 90д) → перепроверяем")
	require.True(t, b.isDue(lookupRow{outcome: "error", checkedAt: now.Add(-48 * time.Hour)}, now),
		"error старый (48ч > 24ч) → ретраим")
}

// ── candidateCond (pure) ────────────────────────────────────────

func TestExternalRatingBackfiller_candidateCond(t *testing.T) {
	fallback := &ExternalRatingBackfiller{cfg: ExternalRatingBackfillConfig{WholeCollection: false}}
	require.Equal(t, "b.rating IS NULL AND b.external_rating IS NULL", fallback.candidateCond(),
		"фолбэк: только книги без любого рейтинга")

	whole := &ExternalRatingBackfiller{cfg: ExternalRatingBackfillConfig{WholeCollection: true}}
	require.Equal(t, "b.external_rating IS NULL", whole.candidateCond(),
		"вся коллекция: любые книги без web-рейтинга (даже с LIBRATE)")
}

// ── фейковый rating-провайдер ───────────────────────────────────

type fakeRatingProvider struct {
	name  string
	res   RatingResult // Average>0 → found; иначе ErrNotFound
	err   error        // override (для error-кейса)
	calls int
}

func (f *fakeRatingProvider) Name() string { return f.name }
func (f *fakeRatingProvider) FetchRating(context.Context, WorkQuery) (RatingResult, error) {
	f.calls++
	if f.err != nil {
		return RatingResult{}, f.err
	}
	if f.res.Average <= 0 {
		return RatingResult{}, ErrNotFound
	}
	return f.res, nil
}

// ── Worker integration (testcontainers PG + фейковый провайдер) ──

func TestExternalRatingBackfiller_Integration(t *testing.T) {
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

	mkBook := func(lib string, rating *int) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, rating)
			VALUES ($1,$2,$3,'f','fb2','T','t',$4) RETURNING id`,
			collID, archID, lib, rating).Scan(&id))
		return id
	}
	readRating := func(id int64) (*float64, *string, *int) {
		var avg *float64
		var src *string
		var cnt *int
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT external_rating, external_rating_source, external_rating_count FROM books WHERE id=$1`, id).
			Scan(&avg, &src, &cnt))
		return avg, src, cnt
	}

	// found: один источник вернул рейтинг → external_rating/source/count проставлены.
	foundBook := mkBook("L-found", nil)
	gb := &fakeRatingProvider{name: "googlebooks", res: RatingResult{Average: 4.2, Count: 100}}
	bf := NewExternalRatingBackfiller(pool, gb, nil,
		ExternalRatingBackfillConfig{GoogleBooks: true, GoogleBooksRPM: 0, NotFoundRetryDays: 90, ErrorRetryHours: 24}, quiet)
	require.Equal(t, 1, bf.drain(ctx))
	avg, src, cnt := readRating(foundBook)
	require.NotNil(t, avg)
	require.InDelta(t, 4.2, *avg, 0.001)
	require.Equal(t, "googlebooks", *src)
	require.Equal(t, 100, *cnt)

	// max-count: два источника, выбираем с бОльшим числом голосов (OL 500 > GB 50).
	mcBook := mkBook("L-mc", nil)
	gbLow := &fakeRatingProvider{name: "googlebooks", res: RatingResult{Average: 4.0, Count: 50}}
	olHigh := &fakeRatingProvider{name: "openlibrary", res: RatingResult{Average: 3.5, Count: 500}}
	bf2 := NewExternalRatingBackfiller(pool, gbLow, olHigh,
		ExternalRatingBackfillConfig{GoogleBooks: true, OpenLibrary: true, GoogleBooksRPM: 0, OpenLibraryRPM: 0, NotFoundRetryDays: 90, ErrorRetryHours: 24}, quiet)
	bf2.drain(ctx)
	avg, src, cnt = readRating(mcBook)
	require.NotNil(t, avg)
	require.InDelta(t, 3.5, *avg, 0.001, "берём рейтинг источника с большим числом голосов")
	require.Equal(t, "openlibrary", *src)
	require.Equal(t, 500, *cnt)

	// not_found: источник ничего → external_rating NULL, lookup not_found, TTL не перепрашивает.
	nfBook := mkBook("L-nf", nil)
	nfProv := &fakeRatingProvider{name: "googlebooks"} // Average 0 → ErrNotFound
	bf3 := NewExternalRatingBackfiller(pool, nfProv, nil,
		ExternalRatingBackfillConfig{GoogleBooks: true, GoogleBooksRPM: 0, NotFoundRetryDays: 90, ErrorRetryHours: 24}, quiet)
	bf3.drain(ctx)
	avg, _, _ = readRating(nfBook)
	require.Nil(t, avg, "not_found → external_rating пуст")
	var outcome string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT outcome FROM book_external_rating_lookups WHERE book_id=$1 AND source='googlebooks'`, nfBook).Scan(&outcome))
	require.Equal(t, "not_found", outcome)
	callsBefore := nfProv.calls
	bf3.drain(ctx)
	require.Equal(t, callsBefore, nfProv.calls, "свежий not_found не перепрашивается (TTL)")

	// фолбэк НЕ берёт книгу с LIBRATE (rating не NULL).
	five := 5
	libBook := mkBook("L-lib", &five)
	libProv := &fakeRatingProvider{name: "googlebooks", res: RatingResult{Average: 3.0, Count: 9}}
	bf4 := NewExternalRatingBackfiller(pool, libProv, nil,
		ExternalRatingBackfillConfig{GoogleBooks: true, GoogleBooksRPM: 0, NotFoundRetryDays: 90, ErrorRetryHours: 24}, quiet)
	bf4.drain(ctx)
	avg, _, _ = readRating(libBook)
	require.Nil(t, avg, "фолбэк: книга с LIBRATE не кандидат")

	// вся коллекция — берёт книгу с LIBRATE (добавляет web рядом).
	bf5 := NewExternalRatingBackfiller(pool, libProv, nil,
		ExternalRatingBackfillConfig{GoogleBooks: true, WholeCollection: true, GoogleBooksRPM: 0, NotFoundRetryDays: 90, ErrorRetryHours: 24}, quiet)
	bf5.drain(ctx)
	avg, src, _ = readRating(libBook)
	require.NotNil(t, avg, "вся коллекция: web-рейтинг проставлен даже при LIBRATE")
	require.InDelta(t, 3.0, *avg, 0.001)
	require.Equal(t, "googlebooks", *src)
}
