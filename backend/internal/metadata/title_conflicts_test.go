package metadata

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestTitleConflictWorks — #279: разбор получают склейки разных текстов одного
// языка без src, разных томов и склейки по заглушке src_title; перевод +
// оригинал и одинаковые названия — нет. Заглушки src_title обнуляются.
func TestTitleConflictWorks(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	collID, archID := seedTitleFixture(t, ctx, pool)
	bunin := seedGroupAuthor(t, ctx, pool, "Бунин", "бунин иван")
	glue := func(lib1, t1, n1, l1, s1, lib2, t2, n2, l2, s2 string) int64 {
		t.Helper()
		a := seedGroupBook(t, ctx, pool, collID, archID, bunin, lib1, t1, n1, l1, s1, "", "")
		b := seedGroupBook(t, ctx, pool, collID, archID, bunin, lib2, t2, n2, l2, s2, "", "")
		w := workIDOf(t, ctx, pool, a)
		_, err := pool.Exec(ctx, `UPDATE books SET work_id = $1 WHERE id = $2`, w, b)
		require.NoError(t, err)
		return w
	}
	stories := glue("S1", "Танька", "танька", "ru", "", "S2", "Антоновские яблоки", "антоновские яблоки", "ru", "")
	volumes := glue("V1", "Свечка. Том 1", "свечка. том 1", "ru", "Svechka 1", "V2", "Свечка. Том 2", "свечка. том 2", "ru", "Svechka 1")
	stub := glue("B1", "Под игото", "под игото", "bg", "(no data for original title)", "B2", "Бай Ганьо", "бай ганьо", "bg", "(no data for original title)")
	translation := glue("T1", "Тёмные аллеи", "тёмные аллеи", "ru", "", "T2", "Dark Avenues", "dark avenues", "en", "Тёмные аллеи")
	spelling := glue("P1", "Жизнь Арсеньева", "жизнь арсеньева", "ru", "", "P2", "Жизнь Арсеньева", "жизнь арсеньева", "ru", "")

	n, err := CleanStubSrcTitles(ctx, pool)
	require.NoError(t, err)
	require.Equal(t, int64(2), n)
	var left int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM books WHERE src_title = '(no data for original title)'`).Scan(&left))
	require.Zero(t, left)

	works, err := TitleConflictWorks(ctx, pool)
	require.NoError(t, err)
	require.ElementsMatch(t, []int64{stories, volumes, stub}, works)
	require.NotContains(t, works, translation)
	require.NotContains(t, works, spelling)
}
