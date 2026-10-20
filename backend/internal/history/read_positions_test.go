package history_test

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/history"
	"github.com/skriptes/skriptes/backend/internal/recsys"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestService_ReadPositions — журнал позиций: строка на сдвиг доли ≥ 0.01
// или через 10 минут; скорость чтения из журнала; «ещё ≈ N минут» в
// «Продолжить чтение» и компакция старых строк до одной в день.
func TestService_ReadPositions(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)

	var userID, collID, archID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ('rp@e.com','RP','x','user') RETURNING id`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	mkBook := func(lib string, size int64) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, size_bytes)
			VALUES ($1,$2,$3,$3,'fb2','Книга','книга',$4) RETURNING id`, collID, archID, lib, size).Scan(&id))
		return id
	}
	book := mkBook("L1", 1_000_000)
	svc := history.New(pool)
	count := func() int {
		var n int
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT count(*) FROM read_positions WHERE user_id=$1 AND book_id=$2`, userID, book).Scan(&n))
		return n
	}
	fp := func(f float64) *float64 { return &f }

	// Первая позиция — строка; та же доля через секунду — нет; сдвиг на 0.005 — нет; на 0.01 — да.
	require.NoError(t, svc.SavePosition(ctx, userID, book, "c1", fp(0.10)))
	require.Equal(t, 1, count())
	require.NoError(t, svc.SavePosition(ctx, userID, book, "c2", fp(0.10)))
	require.NoError(t, svc.SavePosition(ctx, userID, book, "c3", fp(0.105)))
	require.Equal(t, 1, count(), "сдвиг меньше шага без паузы — не пишем")
	require.NoError(t, svc.SavePosition(ctx, userID, book, "c4", fp(0.11)))
	require.Equal(t, 2, count())
	// Без доли (Kindle/позиция без fraction) — журнал не трогаем.
	require.NoError(t, svc.SavePosition(ctx, userID, book, "c5", nil))
	require.Equal(t, 2, count())
	// Прошло больше 10 минут при той же доле — строка (время тоже сигнал).
	_, err := pool.Exec(ctx, `UPDATE read_positions SET at = at - interval '11 minutes' WHERE user_id=$1`, userID)
	require.NoError(t, err)
	require.NoError(t, svc.SavePosition(ctx, userID, book, "c6", fp(0.11)))
	require.Equal(t, 3, count())

	// Скорость: одна сессия 0.10→0.30 за 20 минут на файле 1 МБ = 10 000 байт/мин.
	_, err = pool.Exec(ctx, `DELETE FROM read_positions WHERE user_id=$1`, userID)
	require.NoError(t, err)
	t0 := time.Now().Add(-2 * time.Hour)
	for i, f := range []float64{0.10, 0.20, 0.30} {
		_, err = pool.Exec(ctx, `INSERT INTO read_positions (user_id, book_id, fraction, at) VALUES ($1,$2,$3,$4)`,
			userID, book, f, t0.Add(time.Duration(i)*10*time.Minute))
		require.NoError(t, err)
	}
	speed, err := svc.ReadingSpeed(ctx, userID)
	require.NoError(t, err)
	require.True(t, speed.Known())
	require.InDelta(t, 10_000, speed.BytesPerMin, 1)
	require.Equal(t, 1, speed.Sessions)
	require.Equal(t, 70, history.MinutesLeft(speed, 1_000_000, 0.30))
	require.Equal(t, 0, history.MinutesLeft(speed, recsys.MaxSpeedFileBytes+1, 0.30), "файл с картинками — без оценки")
	require.Equal(t, 0, history.MinutesLeft(recsys.Speed{}, 1_000_000, 0.30), "скорости нет")

	// «Продолжить чтение»: причина in_progress несёт минуты.
	require.NoError(t, svc.SavePosition(ctx, userID, book, "c7", fp(0.30)))
	items, err := svc.ContinueReading(ctx, userID, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	r, ok := items[0].Reason(recsys.ReasonInProgress)
	require.True(t, ok)
	require.Equal(t, 70, r.Minutes)

	// Пользователь без журнала — минут нет.
	var other int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ('o@e.com','O','x','user') RETURNING id`).Scan(&other))
	sp, err := svc.ReadingSpeed(ctx, other)
	require.NoError(t, err)
	require.False(t, sp.Known())

	// Компакция: 5 строк за один день 100 дней назад → остаётся 1 (последняя);
	// свежие строки не трогаются.
	old := time.Now().Add(-100 * 24 * time.Hour)
	for i := 0; i < 5; i++ {
		_, err = pool.Exec(ctx, `INSERT INTO read_positions (user_id, book_id, fraction, at) VALUES ($1,$2,$3,$4)`,
			userID, book, 0.5+float64(i)/100, old.Add(time.Duration(i)*time.Hour))
		require.NoError(t, err)
	}
	before := count()
	n, err := svc.CompactReadPositions(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 4, n)
	require.Equal(t, before-4, count())
	var kept float32
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT fraction FROM read_positions WHERE user_id=$1 AND at < now() - interval '90 days'`, userID).Scan(&kept))
	require.InDelta(t, 0.54, kept, 1e-6, "остаётся последняя строка дня")
	n, err = svc.CompactReadPositions(ctx)
	require.NoError(t, err)
	require.Zero(t, n, "идемпотентно")
}
