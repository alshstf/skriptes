package histimport_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/recsys"
	"github.com/skriptes/skriptes/backend/internal/recsys/histimport"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestExportRoundTrip — выгрузка своей истории — это наш CSV-шаблон: разбор
// и сопоставление с тем же каталогом возвращают те же работы, оценки, полки и
// подчёркивания (взаимность импорта, recsys/04, 4.5).
func TestExportRoundTrip(t *testing.T) {
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
	other := d.int(`INSERT INTO users (email, display_name, password_hash, role) VALUES ('o@x','O','x','user') RETURNING id`)
	zam := d.int(`INSERT INTO authors (last_name, first_name, normalized_name) VALUES ('Замятин','Евгений','замятин евгений') RETURNING id`)
	bulg := d.int(`INSERT INTO authors (last_name, first_name, normalized_name) VALUES ('Булгаков','Михаил','булгаков михаил') RETURNING id`)
	author := func(work int64, a int64) {
		d.exec(`INSERT INTO book_authors (book_id, author_id) SELECT id, $2 FROM books WHERE work_id = $1`, work, a)
	}
	we, weEd, _ := d.work("Мы")
	author(we, zam)
	master, masterEd, _ := d.work("Мастер и Маргарита")
	author(master, bulg)
	dog, _, dogEd2 := d.work("Собачье сердце")
	author(dog, bulg)
	guard, guardEd, _ := d.work("Белая гвардия")
	author(guard, bulg)

	read := time.Date(2023, 3, 5, 12, 0, 0, 0, time.UTC)
	d.exec(`INSERT INTO book_ratings (user_id, work_id, rating) VALUES ($1,$2,5)`, me, we)
	d.exec(`INSERT INTO reads (user_id, book_id, completed_at) VALUES ($1,$2,$3)`, me, weEd, read)
	d.exec(`INSERT INTO reads (user_id, book_id, last_pos, fraction) VALUES ($1,$2,'cfi',0.3)`, me, masterEd)
	d.exec(`INSERT INTO annotations (user_id, book_id, kind, cfi, excerpt, note) VALUES
		($1,$2,'highlight','c1','Рукописи не горят','главное'), ($1,$2,'highlight','c2','Никогда и ничего не просите',''),
		($1,$2,'bookmark','c3','','')`, me, masterEd)
	shelf := d.int(`INSERT INTO user_collections (user_id, name, kind) VALUES ($1,'Хочу прочитать','to_read') RETURNING id`, me)
	d.exec(`INSERT INTO user_collection_books (collection_id, book_id) VALUES ($1,$2)`, shelf, dogEd2)
	d.exec(`INSERT INTO book_ratings (user_id, work_id, rating) VALUES ($1,$2,1)`, other, guard) // чужое — не в выгрузке
	_ = guardEd

	var buf bytes.Buffer
	require.NoError(t, histimport.Export(ctx, pool, me, &buf))
	require.True(t, strings.HasPrefix(buf.String(), "\ufefftitle,author,rating,read_at,shelf,highlight,note,isbn,work_id\n"))
	require.NotContains(t, buf.String(), "Белая гвардия")

	res, err := histimport.Parse(&buf, "")
	require.NoError(t, err)
	require.Equal(t, histimport.SourceCSV, res.Source)
	require.Zero(t, res.SkippedTotal)
	byTitle := map[string]histimport.Item{}
	for _, it := range res.Items {
		byTitle[it.Title] = it
	}
	require.Len(t, byTitle, 3)
	require.Equal(t, 5, byTitle["Мы"].Rating)
	require.Equal(t, []string{"Евгений Замятин"}, byTitle["Мы"].Authors)
	require.Equal(t, time.Date(2023, 3, 5, 0, 0, 0, 0, time.UTC), *byTitle["Мы"].ReadAt)
	require.Equal(t, histimport.ShelfReading, byTitle["Мастер и Маргарита"].Shelf)
	require.Equal(t, []histimport.Highlight{{Text: "Рукописи не горят", Note: "главное"}, {Text: "Никогда и ничего не просите"}},
		byTitle["Мастер и Маргарита"].Highlights)
	require.Equal(t, histimport.ShelfToRead, byTitle["Собачье сердце"].Shelf)

	got, err := (&histimport.Matcher{DB: pool}).Match(ctx, res.Items, recsys.Exclusions{})
	require.NoError(t, err)
	want := map[string]int64{"Мы": we, "Мастер и Маргарита": master, "Собачье сердце": dog}
	for i, it := range res.Items {
		require.Equal(t, histimport.ConfidenceHigh, got[i].Confidence, it.Title)
		require.Equal(t, want[it.Title], got[i].WorkID, it.Title)
	}
}
