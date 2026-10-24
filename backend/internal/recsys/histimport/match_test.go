package histimport

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/recsys"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

type catalog struct {
	t    *testing.T
	ctx  context.Context
	pool *pgxpool.Pool
	coll int64
	arch int64
	seq  int
}

func newCatalog(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *catalog {
	c := &catalog{t: t, ctx: ctx, pool: pool}
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&c.coll))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, c.coll).Scan(&c.arch))
	// id работ и изданий — разные последовательности с пересечением (#399).
	_, err := pool.Exec(ctx, `SELECT setval(pg_get_serial_sequence('works','id'), 1000)`)
	require.NoError(t, err)
	return c
}

func (c *catalog) author(last, first, latin string) int64 {
	var id int64
	var lat *string
	if latin != "" {
		lat = &latin
	}
	require.NoError(c.t, c.pool.QueryRow(c.ctx, `
		INSERT INTO authors (last_name, first_name, normalized_name, latin_name)
		VALUES ($1,$2,lower($1||' '||$2),$3) RETURNING id`, last, first, lat).Scan(&id))
	return id
}

type ed struct {
	lang, src, isbn string
}

// work — работа с изданиями (по умолчанию одно русское) и авторами.
func (c *catalog) work(title string, authors []int64, eds ...ed) int64 {
	var id int64
	require.NoError(c.t, c.pool.QueryRow(c.ctx,
		`INSERT INTO works (title, normalized_title) VALUES ($1,lower($1)) RETURNING id`, title).Scan(&id))
	if len(eds) == 0 {
		eds = []ed{{lang: "ru"}}
	}
	for _, e := range eds {
		c.seq++
		var bid int64
		require.NoError(c.t, c.pool.QueryRow(c.ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id, lang, src_title, isbn)
			VALUES ($1,$2,$3,$3,'fb2',$4,lower($4),$5,$6,NULLIF($7,''),NULLIF($8,'')) RETURNING id`,
			c.coll, c.arch, fmt.Sprintf("L%d", c.seq), title, id, e.lang, e.src, e.isbn).Scan(&bid))
		for i, a := range authors {
			c.exec(`INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,$3)`, bid, a, i)
		}
	}
	return id
}

func (c *catalog) exec(sql string, args ...any) {
	_, err := c.pool.Exec(c.ctx, sql, args...)
	require.NoError(c.t, err)
}

// fakeSearch — запасной поиск: работы, в названии которых есть начало запроса.
type fakeSearch struct {
	byTitle map[string][]int64
	queries []string
}

func (f *fakeSearch) SearchWorkCandidates(_ context.Context, q string, limit int) ([]int64, error) {
	f.queries = append(f.queries, q)
	for t, ids := range f.byTitle {
		if strings.HasPrefix(strings.ToLower(q), strings.ToLower(t)) {
			return ids[:min(limit, len(ids))], nil
		}
	}
	return nil, nil
}

// TestMatch — точность важнее полноты: «уверенно» — только одна работа с
// совпавшими названием и автором; однофамилец, книга без автора, совпадение
// по ISBN или транслит, который гейт не берёт, — на выбор; скрытое — нет.
func TestMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	c := newCatalog(t, ctx, pool)

	strA, strB := c.author("Стругацкий", "Аркадий", ""), c.author("Стругацкий", "Борис", "")
	king := c.author("Кинг", "Стивен", "Stephen King")
	zam, ivanov := c.author("Замятин", "Евгений", ""), c.author("Иванов", "Иван", "")
	bulg := c.author("Булгаков", "Михаил", "")
	lev, alexei := c.author("Толстой", "Лев", ""), c.author("Толстой", "Алексей", "")
	other := c.author("Другой", "Автор", "")

	picnic := c.work("Пикник на обочине", []int64{strA, strB})
	walk := c.work("Долгая прогулка", []int64{king}, ed{lang: "ru", src: "The Long Walk"})
	weZam := c.work("Мы", []int64{zam})
	weIv := c.work("Мы", []int64{ivanov})
	master := c.work("Мастер и Маргарита", []int64{bulg})
	c.work("Мастер и Маргарита. Роман", []int64{bulg})
	childhood := c.work("Детство", []int64{lev})
	c.work("Детство Никиты", []int64{alexei})
	hidden := c.work("Скрытая", []int64{strA})
	c.exec(`UPDATE works SET instance_hidden = true WHERE id = $1`, hidden)
	c.work("Немецкое", []int64{strA}, ed{lang: "de"})
	byISBN := c.work("Совсем иначе", []int64{other}, ed{lang: "ru", isbn: "9785170000001"})

	items := []Item{
		{Title: "Пикник на обочине", Authors: []string{"Стругацкий, Аркадий"}},        // 0 high
		{Title: "Пикник на обочине", Authors: []string{"Arkady Strugatsky"}},          // 1 транслит — на выбор
		{Title: "The Long Walk (Russian Edition)", Authors: []string{"Stephen King"}}, // 2 high по оригиналу
		{Title: "Мы", Authors: []string{"Евгений Замятин"}},                           // 3 high, не Иванов
		{Title: "Мы"}, // 4 без автора — на выбор
		{Title: "Мастер и Маргарита", Authors: []string{"Михаил Булгаков"}},                       // 5 high — точное название
		{Title: "Детство", Authors: []string{"Алексей Толстой"}},                                  // 6 однофамилец — на выбор
		{Title: "Скрытая", Authors: []string{"Аркадий Стругацкий"}},                               // 7 скрыто на инстансе
		{Title: "Немецкое", Authors: []string{"Аркадий Стругацкий"}},                              // 8 язык скрыт у пользователя
		{Title: "Другое название", Authors: []string{"Кто-то"}, ISBNs: []string{"9785170000001"}}, // 9 ISBN — на выбор
		{Title: "Несуществующая книга", Authors: []string{"Стругацкий Аркадий"}},                  // 10 нет
	}
	search := &fakeSearch{byTitle: map[string][]int64{
		"Пикник на обочине": {picnic},
		"Мы":                {weZam, weIv},
	}}
	m := &Matcher{DB: pool, Search: search}
	got, err := m.Match(ctx, items, recsys.Exclusions{Langs: []string{"de"}})
	require.NoError(t, err)
	require.Len(t, got, len(items))

	high := func(i int, work int64) {
		t.Helper()
		require.Equal(t, ConfidenceHigh, got[i].Confidence, "строка %d: %+v", i, got[i])
		require.Equal(t, work, got[i].WorkID, "строка %d", i)
	}
	medium := func(i int, works ...int64) {
		t.Helper()
		require.Equal(t, ConfidenceMedium, got[i].Confidence, "строка %d: %+v", i, got[i])
		var ids []int64
		for _, c := range got[i].Candidates {
			ids = append(ids, c.WorkID)
		}
		require.ElementsMatch(t, works, ids, "строка %d", i)
	}
	none := func(i int) {
		t.Helper()
		require.Equal(t, ConfidenceNone, got[i].Confidence, "строка %d: %+v", i, got[i])
	}

	high(0, picnic)
	require.Equal(t, []string{"Стругацкий Аркадий", "Стругацкий Борис"}, got[0].Candidates[0].Authors)
	medium(1, picnic)
	high(2, walk)
	high(3, weZam)
	medium(4, weZam, weIv)
	high(5, master)
	medium(6, childhood)
	none(7)
	none(8)
	medium(9, byISBN)
	none(10)

	// Запасной поиск — только для ненайденных, с названием без хвостов и автором.
	require.Contains(t, search.queries, "Пикник на обочине Arkady Strugatsky")
	require.Contains(t, search.queries, "Мы")
	require.NotContains(t, search.queries, "Мы Евгений Замятин")
}

func TestTitleVariants(t *testing.T) {
	require.Equal(t, []string{"The Name of the Wind (The Kingkiller Chronicle, #1)", "The Name of the Wind"},
		titleVariants("The Name of the Wind (The Kingkiller Chronicle, #1)"))
	require.Equal(t, []string{"Dune: Deluxe Edition", "Dune"}, titleVariants("Dune: Deluxe Edition"))
	require.Equal(t, []string{"Мы"}, titleVariants("Мы"))
	require.Equal(t, levelFull, titleLevel("Пикник на обочине (сборник)", []string{"Пикник на обочине"}))
	require.Equal(t, levelPartial, titleLevel("Мастер и Маргарита", []string{"Мастер и Маргарита. Роман"}))
	require.Equal(t, levelNone, titleLevel("Детство", []string{"Детство Никиты"}))
}
