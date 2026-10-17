package metadata

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// fakeFantlabYears — подменный источник: годы по названию работы, исход по фамилии.
type fakeFantlabYears struct {
	years map[string]int
	calls map[string]int
	fail  map[string]error
}

func (f *fakeFantlabYears) AuthorYears(_ context.Context, a FantlabAuthorQuery, works []FantlabWorkQuery, wait func(context.Context) error) (FantlabAuthorYears, error) {
	f.calls[a.LastName]++
	if err := f.fail[a.LastName]; err != nil {
		return FantlabAuthorYears{}, err
	}
	var m []FantlabYearMatch
	for _, w := range works {
		if y, ok := f.years[w.Title]; ok {
			m = append(m, FantlabYearMatch{WorkID: w.ID, Year: y, FlWorkID: int64(1000 + y)})
		}
	}
	return FantlabAuthorYears{FantlabID: 7, Via: "search", Matched: m}, nil
}

// TestYearBackfill_FantlabAuthors — фаза «Фантлаб по автору» (#533): внешний год
// работы — только если раньше уже известного; год работы пересчитывается;
// учёт по автору: найденного/не найденного не переспрашиваем до TTL, сбой —
// отдельный исход; автор без работ без года — не кандидат.
func TestYearBackfill_FantlabAuthors(t *testing.T) {
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
	author := func(last string, renown int) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO authors (last_name, first_name, normalized_name, renown) VALUES ($1, 'Имя', lower($1), $2) RETURNING id`,
			last, renown).Scan(&id))
		return id
	}
	n := 0
	work := func(authorID int64, title string, written any) int64 {
		n++
		var wid, bid int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO works (title, normalized_title, primary_author_id, written_year, written_year_source)
			 VALUES ($1, lower($1), $2, $3::smallint, CASE WHEN $3::smallint IS NULL THEN NULL ELSE 'fb2_title' END) RETURNING id`,
			title, authorID, written).Scan(&wid))
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, lang, work_id, written_year, written_year_source)
			VALUES ($1,$2,$3,'f','fb2',$4,lower($4),'ru',$5,$6::smallint, CASE WHEN $6::smallint IS NULL THEN NULL ELSE 'fb2_title' END) RETURNING id`,
			collID, archID, "L"+string(rune('a'+n)), title, wid, written).Scan(&bid))
		_, err := pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,0)`, bid, authorID)
		require.NoError(t, err)
		return wid
	}
	borges := author("Борхес", 2000)
	undated := work(borges, "Алеф", nil)
	reissue := work(borges, "Поиски Аверроэса", 1949) // fb2 — год переиздания
	later := work(borges, "Вавилонская библиотека", 1941)
	lost := author("Потерянный", 1500)
	work(lost, "Никому не известное", nil)
	broken := author("Сбойный", 1000)
	work(broken, "Что-то", nil)
	dated := author("Датированный", 3000) // все работы с годом — не кандидат
	work(dated, "Готово", 1990)

	fake := &fakeFantlabYears{
		years: map[string]int{"Алеф": 1945, "Поиски Аверроэса": 1947, "Вавилонская библиотека": 1944},
		calls: map[string]int{},
		fail:  map[string]error{"Потерянный": ErrNotFound, "Сбойный": ErrUpstream},
	}
	cfg := YearBackfillConfig{Fantlab: true, FantlabRPM: 0, NotFoundRetryDays: 90, ErrorRetryHours: 24}
	b := NewYearBackfiller(pool, nil, cfg, nil, quiet).withFantlab(fake)
	b.drain(ctx)

	year := func(id int64) (written, external *int) {
		require.NoError(t, pool.QueryRow(ctx, `SELECT written_year, external_year FROM works WHERE id = $1`, id).Scan(&written, &external))
		return
	}
	w, x := year(undated)
	require.Equal(t, 1945, *w, "работа без года получила год Фантлаба")
	require.Equal(t, 1945, *x)
	w, _ = year(reissue)
	require.Equal(t, 1947, *w, "год Фантлаба раньше fb2-года переиздания — он и есть год работы")
	w, x = year(later)
	require.Equal(t, 1941, *w, "fb2-год раньше Фантлаба — остаётся")
	require.Equal(t, 1944, *x)
	var flID int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT (ext_ids->>'fl_id')::bigint FROM works WHERE id = $1`, undated).Scan(&flID))
	require.EqualValues(t, 2945, flID)

	outcome := func(id int64) (o string, dated int) {
		require.NoError(t, pool.QueryRow(ctx, `SELECT outcome, dated FROM author_fantlab_lookups WHERE author_id = $1`, id).Scan(&o, &dated))
		return
	}
	o, d := outcome(borges)
	require.Equal(t, "found", o)
	require.Equal(t, 3, d)
	o, _ = outcome(lost)
	require.Equal(t, "not_found", o)
	o, _ = outcome(broken)
	require.Equal(t, "error", o)
	require.Zero(t, fake.calls["Датированный"], "без работ без года — не спрашиваем")

	// Второй проход: все уже спрошены — до TTL никого не переспрашиваем.
	b.drain(ctx)
	require.Equal(t, 1, fake.calls["Борхес"])
	require.Equal(t, 1, fake.calls["Потерянный"])
	require.Equal(t, 1, fake.calls["Сбойный"])
	// Сбой старше своего TTL — спросим снова.
	_, err := pool.Exec(ctx, `UPDATE author_fantlab_lookups SET checked_at = now() - interval '2 days' WHERE author_id = $1`, broken)
	require.NoError(t, err)
	b.drain(ctx)
	require.Equal(t, 2, fake.calls["Сбойный"])

	ctrl := NewYearBackfillController(pool, nil, cfg, nil, quiet).WithFantlab(fake)
	cov, err := ctrl.Coverage(ctx)
	require.NoError(t, err)
	require.Equal(t, FantlabCoverage{Authors: 3, Found: 1, Dated: 3}, cov.Fantlab)

	// «Сбросить неудачные» — и учёт по авторам: найденный остаётся.
	reset, err := ctrl.ResetFailedLookups(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 2, reset)
	cov, err = ctrl.Coverage(ctx)
	require.NoError(t, err)
	require.Equal(t, FantlabCoverage{Authors: 1, Found: 1, Dated: 3}, cov.Fantlab)
}
