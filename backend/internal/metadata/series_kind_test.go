package metadata

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestClassifyPublisherSeries — #468: «<Автор>. Сборники» и переиздание
// отдельных книг без номеров — издательские; цикл с номерами и серия из одной
// книги — циклы; у работы в цикле и в издательской серии — цикл; снятие метки,
// когда признаков больше нет; идемпотентность.
func TestClassifyPublisherSeries(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	collID, archID := seedTitleFixture(t, ctx, pool)
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, sql, args...)
		require.NoError(t, err)
	}
	orwell := seedGroupAuthor(t, ctx, pool, "Оруэлл", "оруэлл джордж")
	series := func(title string) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO series (title, normalized_title, author_id) VALUES ($1, lower($1), $2) RETURNING id`,
			title, orwell).Scan(&id))
		return id
	}
	kind := func(id int64) string {
		var k *string
		require.NoError(t, pool.QueryRow(ctx, `SELECT kind FROM series WHERE id = $1`, id).Scan(&k))
		if k == nil {
			return ""
		}
		return *k
	}
	inSeries := func(book, s int64, no int) {
		exec(`UPDATE books SET series_id = $2, ser_no = NULLIF($3, 0) WHERE id = $1`, book, s, no)
	}
	glue := func(work int64, books ...int64) {
		exec(`UPDATE books SET work_id = $1 WHERE id = ANY($2)`, work, books)
		exec(`DELETE FROM works w WHERE NOT EXISTS (SELECT 1 FROM books b WHERE b.work_id = w.id)`)
	}

	collections := series("Оруэлл, Джордж. Сборники")
	reissue := series("Джордж Оруэлл. Лучшее") // издательская только по устройству
	cycle := series("Хроники Оруэлла")
	single := series("Одиночная серия")

	// Сборник в серии-сборниках.
	coll := seedGroupBook(t, ctx, pool, collID, archID, orwell, "C1", "Эссе", "эссе", "ru", "", "", "")
	inSeries(coll, collections, 0)
	// Две книги в переиздании без номеров, у обеих есть русское издание вне серии.
	farm := seedGroupBook(t, ctx, pool, collID, archID, orwell, "R1", "Скотный двор", "скотный двор", "ru", "", "", "")
	farmOld := seedGroupBook(t, ctx, pool, collID, archID, orwell, "R2", "Скотный двор", "скотный двор", "ru", "", "", "")
	glue(workIDOf(t, ctx, pool, farm), farmOld)
	inSeries(farm, reissue, 0)
	n84 := seedGroupBook(t, ctx, pool, collID, archID, orwell, "R3", "1984", "1984", "ru", "", "", "")
	n84Old := seedGroupBook(t, ctx, pool, collID, archID, orwell, "R4", "1984", "1984", "ru", "", "", "")
	glue(workIDOf(t, ctx, pool, n84), n84Old)
	inSeries(n84, reissue, 0)
	// То же издание «1984» ещё и в цикле — серией работы должен стать цикл.
	inSeries(n84Old, cycle, 1)
	exec(`UPDATE works SET series_id = $1 WHERE id = $2`, reissue, workIDOf(t, ctx, pool, n84))
	// Цикл с номерами.
	c2 := seedGroupBook(t, ctx, pool, collID, archID, orwell, "Y2", "Второй том", "второй том", "ru", "", "", "")
	inSeries(c2, cycle, 2)
	// Серия из одной книги без признаков — цикл.
	one := seedGroupBook(t, ctx, pool, collID, archID, orwell, "S1", "Одна", "одна", "ru", "", "", "")
	inSeries(one, single, 0)

	works, err := ClassifyPublisherSeries(ctx, pool)
	require.NoError(t, err)
	require.Equal(t, "publisher", kind(collections), "«<Автор>. Сборники» — по названию")
	require.Equal(t, "publisher", kind(reissue), "переиздание отдельных книг без номеров")
	require.Equal(t, "", kind(cycle))
	require.Equal(t, "", kind(single), "одна книга без признаков — не трогаем")
	require.Contains(t, works, workIDOf(t, ctx, pool, n84))

	var ws int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT series_id FROM works WHERE id = $1`, workIDOf(t, ctx, pool, n84)).Scan(&ws))
	require.Equal(t, cycle, ws, "у работы в цикле и в издательской серии — цикл")

	works, err = ClassifyPublisherSeries(ctx, pool)
	require.NoError(t, err)
	require.Empty(t, works, "повторный проход — пусто")

	// Признаки пропали (книге проставили номер) — метка снимается.
	exec(`UPDATE books SET ser_no = 1 WHERE id = $1`, farm)
	_, err = ClassifyPublisherSeries(ctx, pool)
	require.NoError(t, err)
	require.Equal(t, "", kind(reissue))
}
