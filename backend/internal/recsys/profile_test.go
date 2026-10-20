package recsys_test

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/recsys"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// fixture — минимальный каталог для профиля: коллекция, архив, авторы,
// серии, жанры; работы и издания создаются по требованию.
type fixture struct {
	t    *testing.T
	ctx  context.Context
	pool *pgxpool.Pool
	coll int64
	arch int64
	seq  int
}

func newFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *fixture {
	f := &fixture{t: t, ctx: ctx, pool: pool}
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&f.coll))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, f.coll).Scan(&f.arch))
	// Сдвигаем последовательность работ: id работы не должен совпасть с id издания (#399).
	_, err := pool.Exec(ctx, `SELECT setval(pg_get_serial_sequence('works','id'), 1000)`)
	require.NoError(t, err)
	return f
}

func (f *fixture) user(email string) int64 {
	var id int64
	require.NoError(f.t, f.pool.QueryRow(f.ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ($1,'U','x','user') RETURNING id`, email).Scan(&id))
	return id
}

func (f *fixture) work(title string, year *int, seriesID *int64) int64 {
	var id int64
	require.NoError(f.t, f.pool.QueryRow(f.ctx,
		`INSERT INTO works (title, normalized_title, written_year, series_id) VALUES ($1,lower($1),$2,$3) RETURNING id`,
		title, year, seriesID).Scan(&id))
	return id
}

func (f *fixture) edition(workID int64, srcLang *string, seriesID *int64) int64 {
	f.seq++
	lib := fmt.Sprintf("L%d", f.seq)
	var id int64
	require.NoError(f.t, f.pool.QueryRow(f.ctx, `
		INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id, src_lang, series_id)
		VALUES ($1,$2,$3,$3,'fb2','T','t',$4,$5,$6) RETURNING id`, f.coll, f.arch, lib, workID, srcLang, seriesID).Scan(&id))
	return id
}

func (f *fixture) author(name string) int64 {
	var id int64
	require.NoError(f.t, f.pool.QueryRow(f.ctx,
		`INSERT INTO authors (last_name, first_name, normalized_name) VALUES ($1,'А',lower($1)||' а') RETURNING id`, name).Scan(&id))
	return id
}

func (f *fixture) series(title string, kind *string) int64 {
	var id int64
	require.NoError(f.t, f.pool.QueryRow(f.ctx,
		`INSERT INTO series (title, normalized_title, kind) VALUES ($1,lower($1),$2) RETURNING id`, title, kind).Scan(&id))
	return id
}

func (f *fixture) genre(code string) int64 {
	var id int64
	require.NoError(f.t, f.pool.QueryRow(f.ctx,
		`INSERT INTO genres (fb2_code) VALUES ($1) ON CONFLICT (fb2_code) DO UPDATE SET fb2_code = EXCLUDED.fb2_code RETURNING id`, code).Scan(&id))
	return id
}

func (f *fixture) exec(sql string, args ...any) {
	_, err := f.pool.Exec(f.ctx, sql, args...)
	require.NoError(f.t, err)
}

func ip(i int) *int       { return &i }
func sp(s string) *string { return &s }
func lp(i int64) *int64   { return &i }

// TestBuildProfile — сигналы по изданиям сводятся к работе, веса складываются
// по таблице 1.1, производные профили считают авторов (не антологий), циклы
// (не издательские серии), жанры, язык и десятилетие; чужой пользователь не
// подмешивается.
func TestBuildProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	f := newFixture(t, ctx, pool)

	me, other := f.user("me@example.com"), f.user("other@example.com")
	solo, co := f.author("Соло"), f.author("Соавтор")
	cycle, publisher := f.series("Цикл", nil), f.series("Издательская", sp("publisher"))
	gSF, gDet := f.genre("sf"), f.genre("detective")

	// Работа A: две редакции, оценка 5, одна дочитана, другая просмотрена
	// дважды, ★ избранное, два подчёркивания; цикл через работу, жанр sf,
	// русский оригинал, 1972 год.
	wA := f.work("A", ip(1972), lp(cycle))
	a1, a2 := f.edition(wA, sp("ru"), nil), f.edition(wA, nil, lp(publisher))
	for _, e := range []int64{a1, a2} {
		f.exec(`INSERT INTO book_authors (book_id, author_id) VALUES ($1,$2)`, e, solo)
		f.exec(`INSERT INTO book_genres (book_id, genre_id) VALUES ($1,$2)`, e, gSF)
	}
	f.exec(`INSERT INTO book_ratings (user_id, work_id, rating, rated_at) VALUES ($1,$2,5,now())`, me, wA)
	f.exec(`INSERT INTO reads (user_id, book_id, completed_at, fraction, updated_at) VALUES ($1,$2,now(),1.0,now())`, me, a1)
	f.exec(`INSERT INTO views (user_id, book_id) VALUES ($1,$2), ($1,$2)`, me, a2)
	var fav int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO user_collections (user_id, name, kind) VALUES ($1,'★','favorites') RETURNING id`, me).Scan(&fav))
	f.exec(`INSERT INTO user_collection_books (collection_id, book_id) VALUES ($1,$2)`, fav, a2)
	f.exec(`INSERT INTO annotations (user_id, book_id, kind, cfi) VALUES ($1,$2,'highlight','c1'), ($1,$2,'bookmark','c2')`, me, a1)

	// Работа B: брошена 200 дней назад на 10 %, скрыта из ленты; жанр detective,
	// английский оригинал, 1999; два автора (соавторство учитывается обоим).
	wB := f.work("B", ip(1999), nil)
	b1 := f.edition(wB, sp("en"), nil)
	f.exec(`INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,0), ($1,$3,1)`, b1, solo, co)
	f.exec(`INSERT INTO book_genres (book_id, genre_id) VALUES ($1,$2)`, b1, gDet)
	f.exec(`INSERT INTO reads (user_id, book_id, fraction, last_pos, updated_at) VALUES ($1,$2,0.1,'p',now() - interval '200 days')`, me, b1)
	f.exec(`INSERT INTO feed_dismissals (user_id, work_id) VALUES ($1,$2)`, me, wB)

	// Работа C: антология (авторов больше MaxAuthors), приобретена через OPDS,
	// не открыта; «никогда не спрашивать оценку». Без года и языка.
	wC := f.work("C", nil, nil)
	c1 := f.edition(wC, nil, nil)
	var anth []int64
	for i := 0; i <= recsys.MaxAuthors; i++ {
		a := f.author(fmt.Sprintf("Антолог%d", i))
		anth = append(anth, a)
		f.exec(`INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,$3)`, c1, a, i)
	}
	f.exec(`INSERT INTO reads (user_id, book_id, acquired_at, updated_at) VALUES ($1,$2,now(),now())`, me, c1)
	f.exec(`INSERT INTO book_rating_prompts (user_id, work_id, state) VALUES ($1,$2,'never')`, me, wC)

	// Чужой пользователь: его оценка работы A в мой профиль не попадает.
	f.exec(`INSERT INTO book_ratings (user_id, work_id, rating) VALUES ($1,$2,1)`, other, wA)

	p, err := recsys.BuildProfile(ctx, pool, me, time.Now(), recsys.DefaultWeights)
	require.NoError(t, err)
	require.False(t, p.IsEmpty())
	require.Len(t, p.Works, 3)

	aA := p.Works[wA]
	require.Equal(t, map[recsys.Kind]float64{
		recsys.KindRating: 5, recsys.KindCompleted: 3, recsys.KindViews: 0.5 * math.Log2(3),
		recsys.KindFavorite: 2, recsys.KindAnnotations: 1,
	}, aA.Parts, aA.String())
	require.InDelta(t, 1, aA.Decay, 1e-6)
	require.InDelta(t, 11.79, aA.Score, 0.01)

	aB := p.Works[wB]
	require.Equal(t, map[recsys.Kind]float64{recsys.KindDropped: -2, recsys.KindDismissed: -3}, aB.Parts, aB.String())
	require.InDelta(t, -5, aB.Score, 1e-6, "скрытие сегодня — затухания нет")

	aC := p.Works[wC]
	require.Equal(t, map[recsys.Kind]float64{recsys.KindAcquired: 1, recsys.KindPromptNever: -1}, aC.Parts, aC.String())

	// Производные: Соло = A + B, Соавтор = только B, антологи — ничего.
	require.InDelta(t, aA.Score+aB.Score, p.Authors[solo], 1e-6)
	require.InDelta(t, aB.Score, p.Authors[co], 1e-6)
	for _, a := range anth {
		require.NotContains(t, p.Authors, a, "антология не делает интересными всех её авторов")
	}
	require.InDelta(t, aA.Score, p.Series[cycle], 1e-6)
	require.NotContains(t, p.Series, publisher, "издательская серия — не вкус")
	require.InDelta(t, aA.Score, p.Genres["sf"], 1e-6)
	require.InDelta(t, aB.Score, p.Genres["detective"], 1e-6)
	require.InDelta(t, aA.Score, p.Langs["ru"], 1e-6)
	require.InDelta(t, aB.Score, p.Langs["en"], 1e-6)
	require.Len(t, p.Langs, 2)
	require.InDelta(t, aA.Score, p.Decades[1970], 1e-6)
	require.InDelta(t, aB.Score, p.Decades[1990], 1e-6)
	require.Len(t, p.Decades, 2)

	require.Contains(t, p.Explain(wA), "rating 5")
	require.Equal(t, fmt.Sprintf("work %d: 0 (нет сигналов)", wA+500), p.Explain(wA+500))

	// Пустой профиль — у пользователя без сигналов.
	empty, err := recsys.BuildProfile(ctx, pool, f.user("nobody@example.com"), time.Now(), recsys.DefaultWeights)
	require.NoError(t, err)
	require.True(t, empty.IsEmpty())
}
