package metadata

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skriptes/skriptes/backend/internal/testpg"
)

// Выборка догона: только с био и без QID/годов, по убыванию известности,
// keyset-курсор не возвращает уже пройденных.
func TestNextQIDBackfillBatch(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	_, err := pool.Exec(ctx, `
		INSERT INTO authors (last_name, first_name, normalized_name, bio, renown, ext_ids, born_year) VALUES
		 ('А', 'Без QID', 'а без qid', 'био', 100, '{}', NULL),
		 ('Б', 'С QID', 'б с qid', 'био', 90, '{"wd_qid":"Q1"}', NULL),
		 ('В', 'Без био', 'в без био', NULL, 80, '{}', NULL),
		 ('Г', 'С годами', 'г с годами', 'био', 70, '{}', 1900),
		 ('Д', 'Второй', 'д второй', 'био', 60, '{}', NULL),
		 ('Е', 'Третий', 'е третий', 'био', 60, '{}', NULL)`)
	require.NoError(t, err)
	batch, err := NextQIDBackfillBatch(ctx, pool, QIDBackfillCursor{Renown: 1 << 62, ID: 1 << 62}, 2)
	require.NoError(t, err)
	require.Len(t, batch, 2)
	require.Equal(t, int64(100), batch[0].Renown)
	require.Equal(t, int64(60), batch[1].Renown) // Б (QID), В (без био), Г (годы) пропущены
	rest, err := NextQIDBackfillBatch(ctx, pool, batch[1], 10)
	require.NoError(t, err)
	require.Len(t, rest, 1, "после курсора — только оставшийся автор с renown 60")
	require.NotEqual(t, batch[1].ID, rest[0].ID)
	none, err := NextQIDBackfillBatch(ctx, pool, rest[0], 10)
	require.NoError(t, err)
	require.Empty(t, none)
}

// Политика сбоев: автор повторяется qidBackfillMaxRetries-1 раз, потом
// откладывается; qidBackfillMaxFailures сбоев подряд — проход прерывается;
// успех сбрасывает оба счётчика.
func TestQIDFailuresPolicy(t *testing.T) {
	var f qidFailures
	require.Equal(t, qidRetryAuthor, f.fail())
	require.Equal(t, qidRetryAuthor, f.fail())
	require.Equal(t, qidSkipAuthor, f.fail(), "третий сбой того же автора — пропуск")
	require.Equal(t, qidRetryAuthor, f.fail(), "следующий автор: счёт попыток с нуля")
	f.ok()
	require.Equal(t, 0, f.total)
	for i := 1; i < qidBackfillMaxFailures; i++ {
		require.NotEqual(t, qidAbortPass, f.fail(), "сбой %d подряд ещё не прерывает проход", i)
	}
	require.Equal(t, qidAbortPass, f.fail(), "десятый сбой подряд — проход прерван")
}

// Курсор прохода переживает рестарт: сохранённая позиция читается, снятая —
// возвращает начало; битое значение — тоже начало.
func TestQIDBackfillCursorPersistence(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	cur, err := loadQIDBackfillCursor(ctx, pool)
	require.NoError(t, err)
	require.Equal(t, qidBackfillTop, cur)
	require.NoError(t, saveQIDBackfillCursor(ctx, pool, QIDBackfillCursor{Renown: 2395, ID: 3933}))
	cur, err = loadQIDBackfillCursor(ctx, pool)
	require.NoError(t, err)
	require.Equal(t, QIDBackfillCursor{Renown: 2395, ID: 3933}, cur)
	require.NoError(t, saveQIDBackfillCursor(ctx, pool, QIDBackfillCursor{Renown: 7, ID: 42}))
	cur, err = loadQIDBackfillCursor(ctx, pool)
	require.NoError(t, err)
	require.Equal(t, int64(42), cur.ID, "повторное сохранение перезаписывает позицию")
	_, err = pool.Exec(ctx, `UPDATE app_settings SET value = '"мусор"'::jsonb WHERE key = $1`, qidBackfillCursorKey)
	require.NoError(t, err)
	cur, err = loadQIDBackfillCursor(ctx, pool)
	require.NoError(t, err)
	require.Equal(t, qidBackfillTop, cur, "битое значение — проход с начала")
	require.NoError(t, clearQIDBackfillCursor(ctx, pool))
	cur, err = loadQIDBackfillCursor(ctx, pool)
	require.NoError(t, err)
	require.Equal(t, qidBackfillTop, cur)
}
