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

// TestAdaptationWorkKeys — #467: ключ из поиска экранизаций (RecordBookWorkKey)
// сбрасывает work_scanned_at, и группировка без сети склеивает по нему работы из
// нескольких изданий («Скотный двор» ru ×2 + «Animal Farm» en ×2); одноязычные
// издания с разными названиями под одним ключом — нет (гейт sameLangTitleConflict).
func TestAdaptationWorkKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	collID, archID := seedTitleFixture(t, ctx, pool)
	orwell := seedGroupAuthor(t, ctx, pool, "Оруэлл", "оруэлл джордж")
	other := seedGroupAuthor(t, ctx, pool, "Другой", "другой автор")

	farm1 := seedGroupBook(t, ctx, pool, collID, archID, orwell, "F1", "Скотный двор", "скотный двор", "ru", "", "", "")
	farm2 := seedGroupBook(t, ctx, pool, collID, archID, orwell, "F2", "Скотный двор", "скотный двор", "ru", "", "", "")
	en1 := seedGroupBook(t, ctx, pool, collID, archID, orwell, "E1", "Animal Farm", "animal farm", "en", "", "", "")
	en2 := seedGroupBook(t, ctx, pool, collID, archID, orwell, "E2", "Animal Farm", "animal farm", "en", "", "", "")
	// Разные книги другого автора, ошибочно получившие один ключ.
	x1 := seedGroupBook(t, ctx, pool, collID, archID, other, "X1", "Первая", "первая", "ru", "", "", "")
	x2 := seedGroupBook(t, ctx, pool, collID, archID, other, "X2", "Вторая", "вторая", "ru", "", "", "")

	g := NewWorkGrouper(pool, nil, nil, WorkGroupConfig{}, nil, quiet)
	g.drainAll(ctx)
	require.Equal(t, workIDOf(t, ctx, pool, farm1), workIDOf(t, ctx, pool, farm2))
	require.Equal(t, workIDOf(t, ctx, pool, en1), workIDOf(t, ctx, pool, en2))
	require.NotEqual(t, workIDOf(t, ctx, pool, farm1), workIDOf(t, ctx, pool, en1), "без ключа переводы не склеены")

	// Поиск экранизаций нашёл QID у одного издания каждой группы.
	RecordBookWorkKey(ctx, pool, quiet, farm2, wikidataSource, "Q100")
	RecordBookWorkKey(ctx, pool, quiet, en1, wikidataSource, "Q100")
	RecordBookWorkKey(ctx, pool, quiet, x1, wikidataSource, "Q200")
	RecordBookWorkKey(ctx, pool, quiet, x2, wikidataSource, "Q200")
	var unscanned int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM books WHERE work_scanned_at IS NULL`).Scan(&unscanned))
	require.Equal(t, 4, unscanned, "новый ключ — группировка пересмотрит издание")

	g.drainAll(ctx)
	w := workIDOf(t, ctx, pool, farm1)
	for _, b := range []int64{farm2, en1, en2} {
		require.Equal(t, w, workIDOf(t, ctx, pool, b), "одна книга под одним QID — одна работа")
	}
	var qid string
	require.NoError(t, pool.QueryRow(ctx, `SELECT COALESCE(ext_ids->>'wd_qid','') FROM works WHERE id = $1`, w).Scan(&qid))
	require.Equal(t, "Q100", qid)
	require.NotEqual(t, workIDOf(t, ctx, pool, x1), workIDOf(t, ctx, pool, x2), "разные названия одного языка под одним ключом — не склеиваем")

	// Повторная запись того же ключа — без сброса.
	RecordBookWorkKey(ctx, pool, quiet, farm2, wikidataSource, "Q100")
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM books WHERE work_scanned_at IS NULL`).Scan(&unscanned))
	require.Zero(t, unscanned)
}
