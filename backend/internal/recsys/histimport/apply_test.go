package histimport_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/history"
	"github.com/skriptes/skriptes/backend/internal/recsys/histimport"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

type db struct {
	t    *testing.T
	ctx  context.Context
	pool *pgxpool.Pool
	coll int64
	arch int64
	seq  int
}

func (d *db) exec(sql string, args ...any) {
	_, err := d.pool.Exec(d.ctx, sql, args...)
	require.NoError(d.t, err)
}

func (d *db) int(sql string, args ...any) int64 {
	var v int64
	require.NoError(d.t, d.pool.QueryRow(d.ctx, sql, args...).Scan(&v))
	return v
}

// work — работа с двумя изданиями; возвращает id работы и изданий.
func (d *db) work(title string) (int64, int64, int64) {
	w := d.int(`INSERT INTO works (title, normalized_title) VALUES ($1,lower($1)) RETURNING id`, title)
	var eds [2]int64
	for i := range eds {
		d.seq++
		eds[i] = d.int(`
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id)
			VALUES ($1,$2,$3,$3,'fb2',$4,lower($4),$5) RETURNING id`, d.coll, d.arch, fmt.Sprintf("L%d", d.seq), title, w)
	}
	return w, eds[0], eds[1]
}

// TestApplyUndo — запись импорта не перетирает своё пользователя, повтор того
// же файла не плодит дубли, откат удаляет только своё и не трогает то, что
// человек с тех пор подтвердил сам (оценка поверх) или начал (прогресс чтения).
func TestApplyUndo(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	d := &db{t: t, ctx: ctx, pool: pool}
	d.coll = d.int(`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`)
	d.arch = d.int(`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, d.coll)
	d.exec(`SELECT setval(pg_get_serial_sequence('works','id'), 1000)`)
	me := d.int(`INSERT INTO users (email, display_name, password_hash, role) VALUES ('me@x','U','x','user') RETURNING id`)

	wA, aEd, _ := d.work("A") // оценка 5 и дата — оценка и «прочитано»
	wB, _, _ := d.work("B")   // «хочу прочитать»
	wC, cEd, _ := d.work("C") // подчёркивания с заметкой
	wD, _, _ := d.work("D")   // своя оценка 3 — не трогаем («прочитано» по оценке — ставим)
	wE, _, e2 := d.work("E")  // уже прочитана (другое издание) — не трогаем
	wF, _, f2 := d.work("F")  // читает: прогресс есть, не дочитана
	wH, _, _ := d.work("H")   // скрыта на инстансе
	d.exec(`UPDATE works SET instance_hidden = true WHERE id = $1`, wH)
	d.exec(`INSERT INTO book_ratings (user_id, work_id, rating) VALUES ($1,$2,3)`, me, wD)
	d.exec(`INSERT INTO reads (user_id, book_id, completed_at) VALUES ($1,$2,now())`, me, e2)
	d.exec(`INSERT INTO reads (user_id, book_id, last_pos, fraction) VALUES ($1,$2,'cfi',0.4)`, me, f2)

	read := time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC)
	res := &histimport.Result{Source: histimport.SourceGoodreads, Items: []histimport.Item{
		{Title: "A", Rating: 5, ReadAt: &read, Shelf: histimport.ShelfRead},
		{Title: "B", Shelf: histimport.ShelfToRead},
		{Title: "C", Highlights: []histimport.Highlight{{Text: "цитата", Note: "мысль"}, {Text: "вторая"}}},
		{Title: "D", Rating: 1},
		{Title: "E", Shelf: histimport.ShelfRead},
		{Title: "F", Shelf: histimport.ShelfRead},
		{Title: "не в библиотеке", Rating: 4},
	}}
	choices := []histimport.Choice{{0, wA}, {1, wB}, {2, wC}, {3, wD}, {4, wE}, {5, wF}}

	rep, err := histimport.Apply(ctx, pool, me, res, "goodreads.csv", choices, nil)
	require.NoError(t, err)
	require.Equal(t, histimport.Report{BatchID: rep.BatchID, Applied: 6, Ratings: 1, Reads: 3, ToRead: 1, Highlights: 2, Kept: 2}, rep)

	// A: оценка и «прочитано» на дату из файла.
	require.EqualValues(t, 5, d.int(`SELECT rating FROM book_ratings WHERE user_id=$1 AND work_id=$2`, me, wA))
	require.EqualValues(t, read.Unix(), d.int(`SELECT extract(epoch FROM rated_at)::bigint FROM book_ratings WHERE user_id=$1 AND work_id=$2`, me, wA))
	require.EqualValues(t, read.Unix(), d.int(`SELECT extract(epoch FROM completed_at)::bigint FROM reads WHERE user_id=$1 AND book_id=$2`, me, aEd))
	// D: своя оценка осталась; E: не продублирована на другом издании.
	require.EqualValues(t, 3, d.int(`SELECT rating FROM book_ratings WHERE user_id=$1 AND work_id=$2`, me, wD))
	require.EqualValues(t, 1, d.int(`SELECT count(*) FROM reads r JOIN books b ON b.id=r.book_id WHERE r.user_id=$1 AND b.work_id=$2`, me, wE))
	// F: дочитана в той же строке, где прогресс.
	require.EqualValues(t, 1, d.int(`SELECT count(*) FROM reads WHERE user_id=$1 AND book_id=$2 AND completed_at IS NOT NULL AND last_pos='cfi'`, me, f2))
	// B: на полке; C: подчёркивания без места в тексте.
	require.EqualValues(t, 1, d.int(`SELECT count(*) FROM user_collection_books cb JOIN user_collections c ON c.id=cb.collection_id
		WHERE c.user_id=$1 AND c.kind='to_read'`, me))
	require.EqualValues(t, 2, d.int(`SELECT count(*) FROM annotations WHERE user_id=$1 AND book_id=$2 AND cfi LIKE 'import:%' AND fraction IS NULL`, me, cEd))
	require.EqualValues(t, 1, d.int(`SELECT count(*) FROM annotations WHERE user_id=$1 AND note='мысль'`, me))

	// Повтор того же файла: ничего нового, всё — «уже было».
	rep2, err := histimport.Apply(ctx, pool, me, res, "goodreads.csv", choices, nil)
	require.NoError(t, err)
	require.Equal(t, 0, rep2.Ratings+rep2.Reads+rep2.ToRead+rep2.Highlights)
	require.EqualValues(t, 2, d.int(`SELECT count(*) FROM annotations WHERE user_id=$1`, me))

	// Скрытая работа и строка вне файла — отказ целиком.
	_, err = histimport.Apply(ctx, pool, me, res, "x", []histimport.Choice{{6, wH}}, nil)
	require.ErrorIs(t, err, histimport.ErrBadChoice)
	_, err = histimport.Apply(ctx, pool, me, res, "x", []histimport.Choice{{7, wA}}, nil)
	require.ErrorIs(t, err, histimport.ErrBadChoice)

	// Человек переоценил A сам — это уже его оценка.
	require.NoError(t, history.New(pool).SetRating(ctx, me, wA, 4))

	require.NoError(t, histimport.Undo(ctx, pool, me, rep.BatchID))
	require.EqualValues(t, 4, d.int(`SELECT rating FROM book_ratings WHERE user_id=$1 AND work_id=$2`, me, wA))
	require.EqualValues(t, 1, d.int(`SELECT count(*) FROM reads WHERE user_id=$1 AND book_id=$2 AND completed_at IS NOT NULL`, me, aEd))
	require.EqualValues(t, 0, d.int(`SELECT count(*) FROM user_collection_books`))
	require.EqualValues(t, 0, d.int(`SELECT count(*) FROM annotations WHERE user_id=$1`, me))
	// F: «прочитано» снято, прогресс остался.
	require.EqualValues(t, 1, d.int(`SELECT count(*) FROM reads WHERE user_id=$1 AND book_id=$2 AND completed_at IS NULL AND last_pos='cfi'`, me, f2))
	require.EqualValues(t, 3, d.int(`SELECT rating FROM book_ratings WHERE user_id=$1 AND work_id=$2`, me, wD))

	require.ErrorIs(t, histimport.Undo(ctx, pool, me, rep.BatchID), histimport.ErrBatchNotFound)
	batches, err := histimport.Batches(ctx, pool, me)
	require.NoError(t, err)
	require.Len(t, batches, 2)
	require.NotNil(t, batches[1].UndoneAt)
	require.Equal(t, histimport.SourceGoodreads, batches[1].Source)
	require.Equal(t, 7, batches[1].Items)
}

// TestApplyWishlist — ненайденные книги файла под наблюдением (#597): в списке
// желаний как «название автор», повтор файла не дублирует, применить можно и
// без выбранных книг, откат удаляет желания пакета.
func TestApplyWishlist(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	d := &db{t: t, ctx: ctx, pool: pool}
	d.coll = d.int(`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`)
	d.arch = d.int(`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, d.coll)
	me := d.int(`INSERT INTO users (email, display_name, password_hash, role) VALUES ('w@x','U','x','user') RETURNING id`)
	wA, _, _ := d.work("A")

	res := &histimport.Result{Source: histimport.SourceGoodreads, Items: []histimport.Item{
		{Title: "A", Rating: 5},
		{Title: "Дом, в котором…", Authors: []string{"Мариам Петросян"}, Shelf: histimport.ShelfToRead},
		{Title: "Пикник на обочине", Authors: []string{"Аркадий Стругацкий", "Борис Стругацкий"}},
		{Title: "Ы"}, // слишком коротко для поиска — не в список
	}}
	rep, err := histimport.Apply(ctx, pool, me, res, "goodreads.csv", []histimport.Choice{{0, wA}}, []int{1, 2, 3})
	require.NoError(t, err)
	require.Equal(t, 2, rep.Wishlist)
	rows, err := pool.Query(ctx, `SELECT q FROM wishlist WHERE user_id=$1 ORDER BY q`, me)
	require.NoError(t, err)
	var qs []string
	for rows.Next() {
		var q string
		require.NoError(t, rows.Scan(&q))
		qs = append(qs, q)
	}
	rows.Close()
	require.Equal(t, []string{"Дом, в котором… Мариам Петросян", "Пикник на обочине Аркадий Стругацкий"}, qs)

	// Повтор файла и применение без выбранных книг: желания уже есть.
	rep2, err := histimport.Apply(ctx, pool, me, res, "goodreads.csv", nil, []int{1, 2})
	require.NoError(t, err)
	require.Zero(t, rep2.Wishlist)
	require.EqualValues(t, 2, d.int(`SELECT count(*) FROM wishlist WHERE user_id=$1`, me))

	_, err = histimport.Apply(ctx, pool, me, res, "x", nil, []int{9})
	require.ErrorIs(t, err, histimport.ErrBadChoice, "строка вне файла")

	batches, err := histimport.Batches(ctx, pool, me)
	require.NoError(t, err)
	require.Equal(t, 2, batches[len(batches)-1].Wishlist)

	// Откат второго пакета желаний не трогает (они первого), первого — удаляет.
	require.NoError(t, histimport.Undo(ctx, pool, me, rep2.BatchID))
	require.EqualValues(t, 2, d.int(`SELECT count(*) FROM wishlist WHERE user_id=$1`, me))
	require.NoError(t, histimport.Undo(ctx, pool, me, rep.BatchID))
	require.EqualValues(t, 0, d.int(`SELECT count(*) FROM wishlist WHERE user_id=$1`, me))
}
