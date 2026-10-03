package metadata

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestPlausibleFb2Year(t *testing.T) {
	now := time.Now().Year()
	require.Equal(t, 1934, plausibleFb2Year(1934, 1993))
	require.Equal(t, 2009, plausibleFb2Year(2013, 2009), "позже издания — год издания")
	require.Equal(t, 2013, plausibleFb2Year(2013, 0), "издание неизвестно — как есть")
	require.Zero(t, plausibleFb2Year(now+2, 0), "будущее")
	require.Zero(t, plausibleFb2Year(999, 0), "раньше 1000")
	require.Equal(t, 2013, plausibleFb2Year(2013, 999), "неправдоподобный год издания не потолок")
}

// TestWorkYears_Integration — год работы (#288): самый ранний правдоподобный год
// изданий, потолок — самое раннее издание, внешний год раньше — побеждает,
// ручная правка неприкосновенна; разовая чистка накопленных fb2-годов.
func TestWorkYears_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	collID, archID := seedTitleFixture(t, ctx, pool)
	au := seedGroupAuthor(t, ctx, pool, "Кристи", "кристи агата")
	setYears := func(book int64, written, edition any) {
		_, err := pool.Exec(ctx, `UPDATE books SET written_year = $2, written_year_source = 'fb2_title', edition_year = $3 WHERE id = $1`,
			book, written, edition)
		require.NoError(t, err)
	}
	year := func(work int64) (y *int, src *string) {
		require.NoError(t, pool.QueryRow(ctx, `SELECT written_year::int, written_year_source FROM works WHERE id = $1`, work).Scan(&y, &src))
		return
	}

	// Перевод-переиздание: fb2 1993 = году издания; внешний год Фантлаба 1934 раньше.
	mirror := seedGroupBook(t, ctx, pool, collID, archID, au, "Y1", "В сумраке зеркала", "в сумраке зеркала", "ru", "", "", "")
	setYears(mirror, 1993, 1993)
	_, err := pool.Exec(ctx, `UPDATE works SET external_year = 1934, external_year_source = 'fantlab' WHERE id = $1`, workIDOf(t, ctx, pool, mirror))
	require.NoError(t, err)
	// Два издания: fb2-год 2013 у перевода, издание оригинала 2009 — потолок.
	burn1 := seedGroupBook(t, ctx, pool, collID, archID, au, "Y2", "Горящая земля", "горящая земля", "ru", "", "", "")
	burn2 := seedGroupBook(t, ctx, pool, collID, archID, au, "Y3", "Горящая земля", "горящая земля", "en", "", "", "")
	_, err = pool.Exec(ctx, `UPDATE books SET work_id = $1 WHERE id = $2`, workIDOf(t, ctx, pool, burn1), burn2)
	require.NoError(t, err)
	setYears(burn1, 2013, 2013)
	setYears(burn2, nil, 2009)
	// Неправдоподобные: позже издания и в будущем.
	late := seedGroupBook(t, ctx, pool, collID, archID, au, "Y4", "Поздняя", "поздняя", "ru", "", "", "")
	setYears(late, 2015, 2014)
	future := seedGroupBook(t, ctx, pool, collID, archID, au, "Y5", "Будущая", "будущая", "ru", "", "", "")
	setYears(future, time.Now().Year()+3, nil)
	// Ручная правка года не трогается.
	manual := seedGroupBook(t, ctx, pool, collID, archID, au, "Y6", "Правленая", "правленая", "ru", "", "", "")
	setYears(manual, 2000, 2000)
	_, err = pool.Exec(ctx, `UPDATE works SET written_year = 1950, written_year_source = 'override' WHERE id = $1`, workIDOf(t, ctx, pool, manual))
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO metadata_overrides (target_kind, target_id, field, override_value, original_value)
		VALUES ('work', $1, 'written_year', '1950', 'null')`, workIDOf(t, ctx, pool, manual))
	require.NoError(t, err)

	changed, err := CleanImplausibleBookYears(ctx, pool)
	require.NoError(t, err)
	require.NotEmpty(t, changed)

	y, src := year(workIDOf(t, ctx, pool, mirror))
	require.Equal(t, 1934, *y)
	require.Equal(t, "fantlab", *src, "внешний год раньше fb2 — побеждает")
	y, src = year(workIDOf(t, ctx, pool, burn1))
	require.Equal(t, 2009, *y)
	require.Equal(t, "edition_year", *src, "потолок — самое раннее издание")
	y, _ = year(workIDOf(t, ctx, pool, late))
	require.Equal(t, 2014, *y, "позже издания — год издания")
	y, _ = year(workIDOf(t, ctx, pool, future))
	require.Nil(t, y, "будущий год вычищен")
	y, src = year(workIDOf(t, ctx, pool, manual))
	require.Equal(t, 1950, *y)
	require.Equal(t, "override", *src)

	again, err := RecomputeWorkYears(ctx, pool, []int64{workIDOf(t, ctx, pool, mirror)})
	require.NoError(t, err)
	require.Empty(t, again, "повторный пересчёт ничего не меняет")
}
