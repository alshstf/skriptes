package metadata

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// ── rateGate (pure) ─────────────────────────────────────────────

func TestRateGate_Interval(t *testing.T) {
	g := &rateGate{}
	g.setRPM(60)
	require.Equal(t, time.Second, g.interval, "60 rpm = 1 запрос/сек")
	g.setRPM(120)
	require.Equal(t, 500*time.Millisecond, g.interval)
	g.setRPM(0)
	require.Equal(t, time.Duration(0), g.interval, "0 rpm = без лимита")

	// interval=0 → wait не блокирует и не ошибается.
	require.NoError(t, g.wait(context.Background()))
}

// ── isDue (pure) ────────────────────────────────────────────────

func TestYearBackfiller_isDue(t *testing.T) {
	b := &YearBackfiller{cfg: YearBackfillConfig{NotFoundRetryDays: 90, ErrorRetryHours: 24}}
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

// ── OpenLibrary FetchYear (httptest) ────────────────────────────

func TestOpenLibrary_FetchYear(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Бесы", r.URL.Query().Get("title"))
		_ = json.NewEncoder(w).Encode(olSearchResponse{
			Docs: []olSearchDoc{{Key: "/works/OL1W", FirstPublishYear: 1872}},
		})
	}))
	defer srv.Close()

	p := NewOpenLibraryProvider(nil).WithEndpoints(srv.URL+"/search.json", srv.URL)
	year, err := p.FetchYear(context.Background(), BookQuery{Title: "Бесы", Authors: []string{"Достоевский"}})
	require.NoError(t, err)
	require.Equal(t, 1872, year)
}

func TestOpenLibrary_FetchYear_NoResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(olSearchResponse{Docs: []olSearchDoc{}})
	}))
	defer srv.Close()
	p := NewOpenLibraryProvider(nil).WithEndpoints(srv.URL+"/search.json", srv.URL)
	_, err := p.FetchYear(context.Background(), BookQuery{Title: "X"})
	require.ErrorIs(t, err, ErrNotFound)
}

// ── Wikidata FetchYear (httptest) ───────────────────────────────

func TestWikidata_FetchYear(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/w/api.php"):
			// wbsearchentities → один кандидат.
			_, _ = io.WriteString(w, `{"search":[{"id":"Q12345"}]}`)
		case strings.HasSuffix(r.URL.Path, "/sparql"):
			q := r.FormValue("query")
			switch {
			case strings.Contains(q, "P50"): // validateBookQID — автор
				_, _ = io.WriteString(w, `{"results":{"bindings":[{"authorLabel":{"value":"Фёдор Достоевский"}}]}}`)
			case strings.Contains(q, "P577"): // год публикации
				_, _ = io.WriteString(w, `{"results":{"bindings":[{"year":{"value":"1872"}}]}}`)
			default:
				http.Error(w, "unknown sparql", http.StatusBadRequest)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p := NewWikidataAdaptationsProvider(nil).WithEndpoints(srv.URL+"/w/api.php", srv.URL+"/sparql", "")
	year, err := p.FetchYear(context.Background(), BookQuery{
		Title: "Бесы", Authors: []string{"Достоевский Фёдор"},
	})
	require.NoError(t, err)
	require.Equal(t, 1872, year)
}

// ── Worker integration (testcontainers PG + фейковый провайдер) ──

type fakeYearProvider struct {
	name  string
	year  int
	err   error
	calls int
}

func (f *fakeYearProvider) Name() string { return f.name }
func (f *fakeYearProvider) FetchYear(context.Context, BookQuery) (int, error) {
	f.calls++
	return f.year, f.err
}

func TestYearBackfiller_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := startPGForPrewarm(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Минимальный сид: collection → archive → 2 книги, локальная fb2-фаза
	// уже прошла (year_local_scanned_at NOT NULL), written_year пустой.
	var collID, archID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))

	mkBook := func(lib string) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, year_local_scanned_at)
			VALUES ($1,$2,$3,'f','fb2','T','t', now()) RETURNING id`,
			collID, archID, lib).Scan(&id))
		return id
	}
	foundBook := mkBook("L-found")
	missBook := mkBook("L-miss")

	// found: OpenLibrary вернул год → written_year проставлен, lookup found.
	okProv := &fakeYearProvider{name: "openlibrary", year: 1869}
	bf := NewYearBackfiller(pool, okProv, nil,
		YearBackfillConfig{OpenLibrary: true, OpenLibraryRPM: 0, NotFoundRetryDays: 90, ErrorRetryHours: 24}, nil, quiet)
	require.Equal(t, 2, bf.drain(ctx), "оба кандидата обработаны")

	var wy *int
	var src *string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT written_year, written_year_source FROM books WHERE id=$1`, foundBook).Scan(&wy, &src))
	require.NotNil(t, wy)
	require.Equal(t, 1869, *wy)
	require.NotNil(t, src)
	require.Equal(t, "openlibrary", *src)

	var outcome string
	var lyear *int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT outcome, year FROM book_year_lookups WHERE book_id=$1 AND source='openlibrary'`, foundBook).
		Scan(&outcome, &lyear))
	require.Equal(t, "found", outcome)
	require.NotNil(t, lyear)
	require.Equal(t, 1869, *lyear)

	// miss: тот же провайдер вернул и для второй книги 1869 (fake одинаков) —
	// проверяем именно not_found отдельным прогоном с провайдером-ErrNotFound.
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT written_year FROM books WHERE id=$1`, missBook).Scan(&wy))
	require.NotNil(t, wy, "fake вернул год и для второй книги")

	// Отдельная книга + провайдер not_found: written_year остаётся NULL,
	// в lookups — not_found.
	nfBook := mkBook("L-nf")
	nfProv := &fakeYearProvider{name: "openlibrary", year: 0, err: ErrNotFound}
	bf2 := NewYearBackfiller(pool, nfProv, nil,
		YearBackfillConfig{OpenLibrary: true, OpenLibraryRPM: 0, NotFoundRetryDays: 90, ErrorRetryHours: 24}, nil, quiet)
	bf2.drain(ctx)

	var wyNF *int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT written_year FROM books WHERE id=$1`, nfBook).Scan(&wyNF))
	require.Nil(t, wyNF, "not_found → written_year остаётся пустым")
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT outcome FROM book_year_lookups WHERE book_id=$1 AND source='openlibrary'`, nfBook).Scan(&outcome))
	require.Equal(t, "not_found", outcome)

	// Повторный проход не должен переспрашивать свежий not_found.
	callsBefore := nfProv.calls
	bf2.drain(ctx)
	require.Equal(t, callsBefore, nfProv.calls, "свежий not_found не перепрашивается (TTL)")
}
