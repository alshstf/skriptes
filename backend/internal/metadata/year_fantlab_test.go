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
	fl    map[string]int64 // произведение Фантлаба по полному названию (Exact)
	kinds map[string]string
	calls map[string]int
	fail  map[string]error
}

func (f *fakeFantlabYears) AuthorYears(_ context.Context, a FantlabAuthorQuery, works []FantlabWorkQuery, wait func(context.Context) error) (FantlabAuthorYears, error) {
	f.calls[a.LastName]++
	if err := f.fail[a.LastName]; err != nil {
		return FantlabAuthorYears{}, err
	}
	var m []FantlabYearMatch
	var k []FantlabKindMatch
	for _, w := range works {
		if y, ok := f.years[w.Title]; ok {
			m = append(m, FantlabYearMatch{WorkID: w.ID, Year: y, FlWorkID: int64(1000 + y)})
		} else if id, ok := f.fl[w.Title]; ok {
			m = append(m, FantlabYearMatch{WorkID: w.ID, FlWorkID: id, Exact: true})
		}
		if kind, ok := f.kinds[w.Title]; ok {
			k = append(k, FantlabKindMatch{WorkID: w.ID, Kind: kind})
		}
	}
	return FantlabAuthorYears{FantlabID: 7, Via: "search", Matched: m, Kinds: k}, nil
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

// TestYearBackfill_FantlabKinds — тип тома-сборника из библиографии (#532):
// работа без года с названием цикла получает omnibus (kind_source=fantlab),
// только если она в 2,5+ раза больше медианы работ автора (роман с названием
// цикла — нет); сборник — без порога; ручную правку не трогаем.
func TestYearBackfill_FantlabKinds(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	collID, archID := seedTitleFixture(t, ctx, pool)
	rowling := seedGroupAuthor(t, ctx, pool, "Роулинг", "роулинг джоан")
	work := func(lib, title string) int64 {
		return workIDOf(t, ctx, pool, seedGroupBook(t, ctx, pool, collID, archID, rowling, lib, title, title, "ru", "", "", ""))
	}
	cycle := work("K1", "Гарри Поттер")
	heur := work("K2", "Сказки барда Бидля")
	manual := work("K3", "Фантастические твари")
	novel := work("K4", "Корморан Страйк") // роман с названием цикла
	coll := work("K5", "Истории Хогвартса")
	for _, w := range []string{"N1", "N2", "N3"} {
		work(w, "Роман "+w)
	}
	size := func(workID, n int64) {
		_, err := pool.Exec(ctx, `UPDATE books SET size_bytes = $2 WHERE work_id = $1`, workID, n)
		require.NoError(t, err)
	}
	_, err := pool.Exec(ctx, `UPDATE books SET size_bytes = 1000000 WHERE work_id IN (SELECT id FROM works WHERE primary_author_id = $1)`, rowling)
	require.NoError(t, err)
	size(cycle, 7000000)
	size(novel, 1300000)
	_, err = pool.Exec(ctx, `UPDATE works SET kind = 'collection', kind_source = 'heuristic' WHERE id = $1`, heur)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE works SET kind = NULL, kind_source = 'override' WHERE id = $1`, manual)
	require.NoError(t, err)

	fake := &fakeFantlabYears{
		years: map[string]int{},
		kinds: map[string]string{"Гарри Поттер": "omnibus", "Сказки барда Бидля": "omnibus", "Фантастические твари": "omnibus",
			"Корморан Страйк": "omnibus", "Истории Хогвартса": "collection"},
		calls: map[string]int{}, fail: map[string]error{},
	}
	cfg := YearBackfillConfig{Fantlab: true, NotFoundRetryDays: 90, ErrorRetryHours: 24}
	NewYearBackfiller(pool, nil, cfg, nil, quiet).withFantlab(fake).drain(ctx)

	k, src := kindOf(t, ctx, pool, cycle)
	require.Equal(t, "omnibus", k, "название цикла — том собрания")
	require.Equal(t, "fantlab", src)
	k, _ = kindOf(t, ctx, pool, heur)
	require.Equal(t, "collection", k, "работа с типом — не кандидат фазы (выборка без сборников)")
	k, src = kindOf(t, ctx, pool, manual)
	require.Empty(t, k, "ручная правка неприкосновенна")
	require.Equal(t, "override", src)
	k, _ = kindOf(t, ctx, pool, novel)
	require.Empty(t, k, "1,3 медианы — роман с названием цикла, не том")
	k, src = kindOf(t, ctx, pool, coll)
	require.Equal(t, "collection", k, "сборник — без порога размера")
	require.Equal(t, "fantlab", src)
}

// TestYearBackfill_FantlabWorkKeys — ключ группировки «произведение Фантлаба»
// (#532): две работы автора с одним произведением библиографии по полному
// названию получают ключ, группировка склеивает их — альтернативные переводы
// одного языка тоже (общий Tier-2 их не склеил бы); разные номера в одной серии
// — нет; одиночке ключ не пишется.
func TestYearBackfill_FantlabWorkKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	collID, archID := seedTitleFixture(t, ctx, pool)
	rowling := seedGroupAuthor(t, ctx, pool, "Роулинг", "роулинг джоан")
	book := func(lib, title string) int64 {
		return seedGroupBook(t, ctx, pool, collID, archID, rowling, lib, title, title, "ru", "", "", "")
	}
	stone := book("H1", "Гарри Поттер и философский камень")
	stoneUS := book("H3", "Гарри Поттер и философский камень") // то же произведение, американский оригинал
	spivak := book("H2", "Гарри Поттер и Волшебный камень")
	_, err0 := pool.Exec(ctx, `UPDATE books SET src_title = CASE WHEN id = $1 THEN 'Harry Potter and the Philosopher''s Stone'
		ELSE 'Harry Potter and the Sorcerer''s Stone' END WHERE id IN ($1, $2)`, stone, stoneUS)
	require.NoError(t, err0)
	_, err0 = pool.Exec(ctx, `UPDATE books SET work_id = $1 WHERE id = $2`, workIDOf(t, ctx, pool, stone), stoneUS)
	require.NoError(t, err0)
	vol1 := book("D1", "Танец с драконами. Грёзы и пыль")
	vol2 := book("D2", "Танец с драконами. Искры над пеплом")
	single := book("S1", "Сказки барда Бидля")
	putInSeries(t, ctx, pool, vol1, "танец с драконами")
	putInSeries(t, ctx, pool, vol2, "танец с драконами")
	_, err := pool.Exec(ctx, `UPDATE books SET ser_no = CASE WHEN id = $1 THEN 1 ELSE 2 END WHERE id IN ($1, $2)`, vol1, vol2)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE books SET work_scanned_at = now()`)
	require.NoError(t, err)

	fake := &fakeFantlabYears{
		years: map[string]int{},
		fl: map[string]int64{
			"Гарри Поттер и философский камень": 3651, "Гарри Поттер и Волшебный камень": 3651,
			"Танец с драконами. Грёзы и пыль": 9000, "Танец с драконами. Искры над пеплом": 9000,
			"Сказки барда Бидля": 7777,
		},
		calls: map[string]int{}, fail: map[string]error{},
	}
	cfg := YearBackfillConfig{Fantlab: true, NotFoundRetryDays: 90, ErrorRetryHours: 24}
	NewYearBackfiller(pool, nil, cfg, nil, quiet).withFantlab(fake).drain(ctx)

	var keyed, unscanned int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM book_work_lookups WHERE source = 'fantlab' AND outcome = 'found'`).Scan(&keyed))
	require.Equal(t, 5, keyed, "ключ — изданиям сошедшихся работ, не одиночке")
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM books WHERE work_scanned_at IS NULL`).Scan(&unscanned))
	require.Equal(t, 5, unscanned, "группировка пересмотрит автора")

	g := NewWorkGrouper(pool, nil, nil, WorkGroupConfig{}, nil, quiet)
	require.NoError(t, g.groupAuthorTier1(ctx, rowling))
	require.Equal(t, workIDOf(t, ctx, pool, stone), workIDOf(t, ctx, pool, spivak),
		"переводы одного произведения — одна работа, хотя у неё два оригинала (британский и американский)")
	require.NotEqual(t, workIDOf(t, ctx, pool, vol1), workIDOf(t, ctx, pool, vol2), "разные номера в одной серии — разные тома")
	require.NotEqual(t, workIDOf(t, ctx, pool, stone), workIDOf(t, ctx, pool, single))
	var fl string
	require.NoError(t, pool.QueryRow(ctx, `SELECT COALESCE(ext_ids->>'fl_id', '') FROM works WHERE id = $1`, workIDOf(t, ctx, pool, stone)).Scan(&fl))
	require.Equal(t, "3651", fl, "ключ — в ext_ids работы как fl_id")
}
