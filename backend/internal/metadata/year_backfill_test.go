package metadata

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestEnrichBooksNow_NoopWhenNotReady — ленивый внешний путь без провайдеров
// (и/или без пула) ничего не делает и не паникует.
func TestEnrichBooksNow_NoopWhenNotReady(t *testing.T) {
	c := NewYearBackfillController(nil, nil, nil, YearBackfillConfig{}, nil, slog.Default())
	require.False(t, c.ready())
	c.EnrichBooksNow(context.Background(), []LazyBook{{ID: 1, Title: "X"}})
	// no panic, no work → тест проходит, если не паникнули.
}

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
	mu    sync.Mutex // воркер зовёт провайдер из нескольких горутин
	name  string
	year  int
	err   error
	calls int
}

func (f *fakeYearProvider) Name() string { return f.name }
func (f *fakeYearProvider) FetchYear(context.Context, BookQuery) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.year, f.err
}

// orderYearProvider — фейк, записывающий ПОРЯДОК обработанных book_id.
type orderYearProvider struct {
	mu  sync.Mutex
	ids []int64
}

func (f *orderYearProvider) Name() string { return "openlibrary" }
func (f *orderYearProvider) FetchYear(_ context.Context, q BookQuery) (int, error) {
	f.mu.Lock()
	f.ids = append(f.ids, q.ID)
	f.mu.Unlock()
	return 0, ErrNotFound
}

// TestYearBackfiller_CoreFirst — приоритизация «ядро сначала» (bookCoreCond):
// книга ядра (работа с 2 изданиями) обрабатывается РАНЬШЕ хвостовой, даже если
// хвостовая имеет меньший id (наивный id-порядок шёл бы к ней первым).
func TestYearBackfiller_CoreFirst(t *testing.T) {
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

	mkBook := func(lib string, workID any) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, year_local_scanned_at, work_id)
			VALUES ($1,$2,$3,'f','fb2','T','t', now(), $4) RETURNING id`,
			collID, archID, lib, workID).Scan(&id))
		return id
	}

	// Хвостовая книга — ПЕРВОЙ (меньший id), синглтон-работа.
	var tailWork, coreWork int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO works (title, normalized_title) VALUES ('Хвост','хвост') RETURNING id`).Scan(&tailWork))
	tail := mkBook("L-tail", tailWork)
	// Ядро — работа с ДВУМЯ изданиями (bookCoreCond: переиздания).
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO works (title, normalized_title) VALUES ('Ядро','ядро') RETURNING id`).Scan(&coreWork))
	core1 := mkBook("L-core1", coreWork)
	core2 := mkBook("L-core2", coreWork)

	prov := &orderYearProvider{}
	bf := NewYearBackfiller(pool, prov, nil,
		YearBackfillConfig{OpenLibrary: true, OpenLibraryRPM: 0, NotFoundRetryDays: 90, ErrorRetryHours: 24}, nil, quiet)
	require.Equal(t, 3, bf.drain(ctx))

	require.Len(t, prov.ids, 3)
	// Первые двое — издания ядра (в любом порядке: батч конкурентный), хвост — последним.
	require.ElementsMatch(t, []int64{core1, core2}, prov.ids[:2], "ядро идёт первым")
	require.Equal(t, tail, prov.ids[2], "хвост — после ядра")
}

func TestYearBackfiller_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := testpg.Pool(t, ctx)
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

// seedYearBook — издание для тестов источников года: локальная fb2-фаза прошла,
// года нет; title/src_title/edition_year/work — по случаю.
func seedYearBook(t *testing.T, ctx context.Context, pool *pgxpool.Pool, lib, title, srcTitle string, edition int, workID *int64) int64 {
	t.Helper()
	var collID, archID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ($1, $1 || '.inpx') RETURNING id`, "c-"+lib).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1, $2 || '.zip') RETURNING id`, collID, lib).Scan(&archID))
	var ed *int
	if edition > 0 {
		ed = &edition
	}
	var id int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title,
		                   src_title, edition_year, work_id, year_local_scanned_at)
		VALUES ($1,$2,$3,'f','fb2',$4,lower($4), NULLIF($5,''), $6, $7, now()) RETURNING id`,
		collID, archID, lib, title, srcTitle, ed, workID).Scan(&id))
	return id
}

// TestYearBackfiller_OpenLibraryNotByCyrillic — по кириллическому названию
// OpenLibrary находит русские издания (год переиздания, прод 2026-10-11:
// «Айвенго» 2007) — его не спрашиваем, год даёт Wikidata. Перевод с названием
// оригинала OpenLibrary по-прежнему спрашивается.
func TestYearBackfiller_OpenLibraryNotByCyrillic(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	ru := seedYearBook(t, ctx, pool, "L-ru", "Айвенго", "", 1994, nil)
	ol := &fakeYearProvider{name: "openlibrary", year: 2007}
	wd := &fakeYearProvider{name: "wikidata", year: 1819}
	bf := NewYearBackfiller(pool, ol, wd, YearBackfillConfig{OpenLibrary: true, Wikidata: true,
		NotFoundRetryDays: 90, ErrorRetryHours: 24}, nil, quiet)
	bf.drain(ctx)

	require.Equal(t, 0, ol.calls, "OpenLibrary по кириллице не спрашиваем")
	var wy int
	var src string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT written_year, written_year_source FROM books WHERE id=$1`, ru).Scan(&wy, &src))
	require.Equal(t, 1819, wy)
	require.Equal(t, "wikidata", src)
	var outcome string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT outcome FROM book_year_lookups WHERE book_id=$1 AND source='openlibrary'`, ru).Scan(&outcome))
	require.Equal(t, "not_found", outcome, "пропуск учтён — книга не всплывает каждый проход")

	// Перевод с названием оригинала (латиница) — OpenLibrary спрашивается.
	tr := seedYearBook(t, ctx, pool, "L-tr", "Там, где в дымке холмы", "A Pale View of Hills", 2007, nil)
	ol2 := &fakeYearProvider{name: "openlibrary", year: 1982}
	bf2 := NewYearBackfiller(pool, ol2, nil, YearBackfillConfig{OpenLibrary: true,
		NotFoundRetryDays: 90, ErrorRetryHours: 24}, nil, quiet)
	bf2.drain(ctx)
	require.Equal(t, 1, ol2.calls)
	require.NoError(t, pool.QueryRow(ctx, `SELECT written_year FROM books WHERE id=$1`, tr).Scan(&wy))
	require.Equal(t, 1982, wy)
}

// TestYearBackfiller_RejectsYearAfterEdition — внешний год позже года издания
// книги отклоняется («не найдено»), и спрашивается следующий источник; год работы
// пересчитывается после прохода.
func TestYearBackfiller_RejectsYearAfterEdition(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	var workID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO works (title, normalized_title) VALUES ('Ivanhoe','ivanhoe') RETURNING id`).Scan(&workID))
	book := seedYearBook(t, ctx, pool, "L-ed", "Ivanhoe", "", 1998, &workID)

	ol := &fakeYearProvider{name: "openlibrary", year: 2012}
	wd := &fakeYearProvider{name: "wikidata", year: 1819}
	bf := NewYearBackfiller(pool, ol, wd, YearBackfillConfig{OpenLibrary: true, Wikidata: true,
		NotFoundRetryDays: 90, ErrorRetryHours: 24}, nil, quiet)
	bf.drain(ctx)

	require.Equal(t, 1, ol.calls)
	require.Equal(t, 1, wd.calls, "отклонённый год — спрашиваем следующий источник")
	var wy int
	var src string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT written_year, written_year_source FROM books WHERE id=$1`, book).Scan(&wy, &src))
	require.Equal(t, 1819, wy)
	require.Equal(t, "wikidata", src)
	var outcome string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT outcome FROM book_year_lookups WHERE book_id=$1 AND source='openlibrary'`, book).Scan(&outcome))
	require.Equal(t, "not_found", outcome)

	var workYear *int
	require.NoError(t, pool.QueryRow(ctx, `SELECT written_year FROM works WHERE id=$1`, workID).Scan(&workYear))
	require.NotNil(t, workYear, "год работы пересчитан после прохода")
	require.Equal(t, 1819, *workYear)
}

// TestCleanExternalBookYears — разовая чистка: год OpenLibrary по кириллице и
// внешний год позже издания — пустые (found-попытка стёрта); год OpenLibrary по
// латинице и год Wikidata по кириллице — остаются.
func TestCleanExternalBookYears(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)

	mk := func(lib, title, srcTitle string, edition, year int, source string) int64 {
		id := seedYearBook(t, ctx, pool, lib, title, srcTitle, edition, nil)
		_, err := pool.Exec(ctx, `UPDATE books SET written_year=$2, written_year_source=$3 WHERE id=$1`, id, year, source)
		require.NoError(t, err)
		_, err = pool.Exec(ctx, `INSERT INTO book_year_lookups (book_id, source, outcome, year, checked_at)
			VALUES ($1, $2, 'found', $3, now())`, id, source, year)
		require.NoError(t, err)
		return id
	}
	olRu := mk("c1", "Конёк-Горбунок", "", 0, 2012, "openlibrary")
	olLatin := mk("c2", "White Fang", "", 0, 1905, "openlibrary")
	olTranslation := mk("c3", "Там, где в дымке холмы", "A Pale View of Hills", 2007, 1982, "openlibrary")
	wdAfterEdition := mk("c4", "Тёмный мир", "", 1998, 2012, "wikidata")
	wdRu := mk("c5", "Сонечка", "", 0, 1992, "wikidata")

	_, cleaned, err := CleanExternalBookYears(ctx, pool)
	require.NoError(t, err)
	require.Equal(t, int64(2), cleaned)

	year := func(id int64) *int {
		var y *int
		require.NoError(t, pool.QueryRow(ctx, `SELECT written_year FROM books WHERE id=$1`, id).Scan(&y))
		return y
	}
	require.Nil(t, year(olRu), "OpenLibrary по кириллице — год переиздания")
	require.Nil(t, year(wdAfterEdition), "год позже издания")
	require.NotNil(t, year(olLatin))
	require.NotNil(t, year(olTranslation), "перевод искали по оригиналу")
	require.NotNil(t, year(wdRu), "Wikidata по кириллице — год работы, не издания")

	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM book_year_lookups WHERE book_id = ANY($1)`, []int64{olRu, wdAfterEdition}).Scan(&n))
	require.Zero(t, n, "найденные попытки стёрты — книги снова кандидаты")
}
