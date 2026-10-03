package metadata

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestSplitAlienEditions — #285: издание без общих авторов с якорем работы
// уходит в свою работу; перевод с совпадающим src_title и работа с ручной
// правкой авторов остаются как есть.
func TestSplitAlienEditions(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	collID, archID := seedTitleFixture(t, ctx, pool)
	dalessandro := seedGroupAuthor(t, ctx, pool, "Д'Алессандро", "д'алессандро джеки")
	roth := seedGroupAuthor(t, ctx, pool, "Рот", "рот йозеф")
	bulgakov := seedGroupAuthor(t, ctx, pool, "Булгаков", "булгаков михаил")
	bulgakovLat := seedGroupAuthor(t, ctx, pool, "Bulgakov", "bulgakov mikhail")
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, sql, args...)
		require.NoError(t, err)
	}

	// Склейка разных книг: якорь — русское издание, основной автор устарел (Рот).
	love := seedGroupBook(t, ctx, pool, collID, archID, dalessandro, "A1", "Любовь в кредит", "любовь в кредит", "ru", "", "", "")
	leviathan := seedGroupBook(t, ctx, pool, collID, archID, roth, "A2", "The Leviathan", "the leviathan", "en", "", "", "")
	w := workIDOf(t, ctx, pool, love)
	exec(`UPDATE books SET work_id = $1, work_scanned_at = now() WHERE id = $2`, w, leviathan)
	exec(`UPDATE works SET primary_author_id = $2 WHERE id = $1`, w, roth)

	// Перевод, склеенный вручную через разные записи автора: src_title совпадает.
	master := seedGroupBook(t, ctx, pool, collID, archID, bulgakov, "B1", "Мастер и Маргарита", "мастер и маргарита", "ru", "", "", "")
	masterEn := seedGroupBook(t, ctx, pool, collID, archID, bulgakovLat, "B2", "The Master and Margarita", "the master and margarita", "en", "Мастер и Маргарита", "ru", "")
	wm := workIDOf(t, ctx, pool, master)
	exec(`UPDATE books SET work_id = $1 WHERE id = $2`, wm, masterEn)

	// Ручная правка авторов — не трогаем.
	kept := seedGroupBook(t, ctx, pool, collID, archID, dalessandro, "C1", "Ночь", "ночь", "ru", "", "", "")
	keptAlien := seedGroupBook(t, ctx, pool, collID, archID, roth, "C2", "Day", "day", "en", "", "", "")
	wk := workIDOf(t, ctx, pool, kept)
	exec(`UPDATE books SET work_id = $1 WHERE id = $2`, wk, keptAlien)
	exec(`INSERT INTO metadata_overrides (target_kind, target_id, field, override_value, original_value)
		VALUES ('work', $1, 'authors', '{}', '{}')`, wk)

	touched, err := SplitAlienEditions(ctx, pool)
	require.NoError(t, err)
	newWork := workIDOf(t, ctx, pool, leviathan)
	require.NotEqual(t, w, newWork, "чужое издание ушло в свою работу")
	require.ElementsMatch(t, []int64{w, newWork}, touched)
	require.Equal(t, w, workIDOf(t, ctx, pool, love), "якорь остался")

	var primary int64
	var title string
	var scanned *time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT primary_author_id, title FROM works WHERE id = $1`, w).Scan(&primary, &title))
	require.Equal(t, dalessandro, primary, "основной автор — из якоря")
	require.Equal(t, "Любовь в кредит", title)
	require.NoError(t, pool.QueryRow(ctx, `SELECT primary_author_id, title FROM works WHERE id = $1`, newWork).Scan(&primary, &title))
	require.Equal(t, roth, primary)
	require.Equal(t, "The Leviathan", title)
	require.NoError(t, pool.QueryRow(ctx, `SELECT work_scanned_at FROM books WHERE id = $1`, leviathan).Scan(&scanned))
	require.Nil(t, scanned, "группировка пересмотрит вынесенное издание")

	require.Equal(t, wm, workIDOf(t, ctx, pool, masterEn), "перевод с тем же src_title — не трогаем")
	require.Equal(t, wk, workIDOf(t, ctx, pool, keptAlien), "ручная правка авторов — не трогаем")

	touched, err = SplitAlienEditions(ctx, pool)
	require.NoError(t, err)
	require.Empty(t, touched, "повторный проход — пусто")
}
