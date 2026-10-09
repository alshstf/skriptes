package metadata

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/skriptes/skriptes/backend/internal/workauthors"
	"github.com/stretchr/testify/require"
)

// TestSplitAnthologyEditions — #464: одноимённая антология выносится из работы
// романа (копии — вместе), авторы работы — по правилу workauthors.Core,
// группировка вынесенное обратно не приклеивает; многоавторская работа и работа
// с ручной правкой авторов не трогаются.
func TestSplitAnthologyEditions(t *testing.T) {
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
	addAuthors := func(book int64, authors ...int64) {
		t.Helper()
		for i, a := range authors {
			exec(`INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,$3)`, book, a, i+1)
		}
	}
	glue := func(work int64, books ...int64) {
		t.Helper()
		exec(`UPDATE books SET work_id = $1, work_scanned_at = now() WHERE id = ANY($2)`, work, books)
		exec(`DELETE FROM works w WHERE NOT EXISTS (SELECT 1 FROM books b WHERE b.work_id = w.id)`)
	}
	coreAuthors := func(work int64) []int64 {
		t.Helper()
		rows, err := pool.Query(ctx, `SELECT author_id FROM (`+workauthors.Core("$1")+`) c ORDER BY minpos, author_id`, work)
		require.NoError(t, err)
		defer rows.Close()
		var out []int64
		for rows.Next() {
			var id int64
			require.NoError(t, rows.Scan(&id))
			out = append(out, id)
		}
		return out
	}

	anderson := seedGroupAuthor(t, ctx, pool, "Андерсон", "андерсон пол")
	niven := seedGroupAuthor(t, ctx, pool, "Нивен", "нивен ларри")
	aldiss := seedGroupAuthor(t, ctx, pool, "Олдисс", "олдисс брайан")
	others := []int64{
		seedGroupAuthor(t, ctx, pool, "Баллард", "баллард джеймс"),
		seedGroupAuthor(t, ctx, pool, "Силверберг", "силверберг роберт"),
		seedGroupAuthor(t, ctx, pool, "Уиндем", "уиндем джон"),
	}

	// Роман в двух изданиях + две копии одноимённой антологии (якорь — антология:
	// у неё меньший id среди изданий с названием работы).
	anth1 := seedGroupBook(t, ctx, pool, collID, archID, anderson, "P1", "Патруль времени", "патруль времени", "ru", "", "", "")
	addAuthors(anth1, others...)
	novel1 := seedGroupBook(t, ctx, pool, collID, archID, anderson, "P2", "Патруль времени", "патруль времени", "ru", "", "", "")
	novel2 := seedGroupBook(t, ctx, pool, collID, archID, anderson, "P3", "Time Patrol", "time patrol", "en", "", "", "")
	anth2 := seedGroupBook(t, ctx, pool, collID, archID, anderson, "P4", "Патруль времени", "патруль времени", "ru", "", "", "")
	addAuthors(anth2, others...)
	patrol := workIDOf(t, ctx, pool, novel1)
	glue(patrol, anth1, novel1, novel2, anth2)
	exec(`UPDATE works SET normalized_title = 'патруль времени' WHERE id = $1`, patrol)

	// «Мир-Кольцо»: соавтор в одном издании из трёх — не автор работы; из двух — автор.
	ring1 := seedGroupBook(t, ctx, pool, collID, archID, niven, "R1", "Мир-Кольцо", "мир-кольцо", "ru", "", "", "")
	ring2 := seedGroupBook(t, ctx, pool, collID, archID, niven, "R2", "Ringworld", "ringworld", "en", "", "", "")
	ringAnth := seedGroupBook(t, ctx, pool, collID, archID, niven, "R3", "Мир-Кольцо", "мир-кольцо", "ru", "", "", "")
	addAuthors(ringAnth, aldiss)
	ring := workIDOf(t, ctx, pool, ring1)
	glue(ring, ring2, ringAnth)
	require.Equal(t, []int64{niven}, coreAuthors(ring), "Олдисс в одном издании из трёх — не автор работы")
	exec(`UPDATE books SET deleted = true WHERE id = $1`, ring2)
	require.Equal(t, []int64{niven, aldiss}, coreAuthors(ring), "из двух изданий — оба автора, как раньше")

	// Многоавторская работа: издания с 3 и 5 авторами — гейт не трогает.
	alm1 := seedGroupBook(t, ctx, pool, collID, archID, others[0], "M1", "Альманах", "альманах", "ru", "", "", "")
	addAuthors(alm1, others[1], others[2])
	alm2 := seedGroupBook(t, ctx, pool, collID, archID, others[0], "M2", "Альманах", "альманах", "ru", "", "", "")
	addAuthors(alm2, others[1], others[2], anderson, niven)
	alm := workIDOf(t, ctx, pool, alm1)
	glue(alm, alm2)

	// Ручная правка авторов — не трогаем.
	kept := seedGroupBook(t, ctx, pool, collID, archID, niven, "K1", "Нейтронная звезда", "нейтронная звезда", "ru", "", "", "")
	keptAnth := seedGroupBook(t, ctx, pool, collID, archID, niven, "K2", "Нейтронная звезда", "нейтронная звезда", "ru", "", "", "")
	addAuthors(keptAnth, others...)
	wk := workIDOf(t, ctx, pool, kept)
	glue(wk, keptAnth)
	exec(`INSERT INTO metadata_overrides (target_kind, target_id, field, override_value, original_value)
		VALUES ('work', $1, 'authors', '{}', '{}')`, wk)

	touched, err := SplitAnthologyEditions(ctx, pool)
	require.NoError(t, err)
	anthWork := workIDOf(t, ctx, pool, anth1)
	require.NotEqual(t, patrol, anthWork, "антология ушла из работы романа")
	require.Equal(t, anthWork, workIDOf(t, ctx, pool, anth2), "копии антологии — в одной работе")
	require.Equal(t, patrol, workIDOf(t, ctx, pool, novel1))
	require.Equal(t, patrol, workIDOf(t, ctx, pool, novel2))
	require.ElementsMatch(t, []int64{patrol, anthWork}, touched)
	require.Equal(t, []int64{anderson}, coreAuthors(patrol), "авторы романа — без авторов антологии")
	require.Equal(t, alm, workIDOf(t, ctx, pool, alm2), "многоавторская работа — не трогаем")
	require.Equal(t, wk, workIDOf(t, ctx, pool, keptAnth), "ручная правка авторов — не трогаем")

	var editions int
	var scanned *time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT edition_count FROM works WHERE id = $1`, patrol).Scan(&editions))
	require.Equal(t, 2, editions)
	require.NoError(t, pool.QueryRow(ctx, `SELECT work_scanned_at FROM books WHERE id = $1`, anth1).Scan(&scanned))
	require.Nil(t, scanned, "группировка пересмотрит вынесенное")
	require.Equal(t, []int64{anderson, others[0], others[1], others[2]}, coreAuthors(anthWork))

	// Группировка с гейтом не приклеивает антологию обратно.
	g := NewWorkGrouper(pool, nil, nil, WorkGroupConfig{}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	g.drainAll(ctx)
	require.Equal(t, patrol, workIDOf(t, ctx, pool, novel1))
	require.Equal(t, anthWork, workIDOf(t, ctx, pool, anth1), "антология осталась отдельной работой")
	require.Equal(t, anthWork, workIDOf(t, ctx, pool, anth2))

	touched, err = SplitAnthologyEditions(ctx, pool)
	require.NoError(t, err)
	require.Empty(t, touched, "повторный проход — пусто")
}
