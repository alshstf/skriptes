package timeline

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skriptes/skriptes/backend/internal/bio"
	"github.com/skriptes/skriptes/backend/internal/testpg"
)

func TestSeedTimelineAndHides(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	pack, ds := testPack(t)
	svc, err := New(pool, pack, nil)
	require.NoError(t, err)

	// Сев идемпотентен: второй раз — no-op (updated_at не меняется).
	require.NoError(t, svc.Seed(ctx))
	var n int
	var ts1 time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*), max(updated_at) FROM author_bio`).Scan(&n, &ts1))
	require.Equal(t, len(ds.Authors), n)
	require.NoError(t, svc.Seed(ctx))
	var ts2 time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT max(updated_at) FROM author_bio`).Scan(&ts2))
	require.Equal(t, ts1, ts2, "повторный сев той же версии должен быть no-op")

	// Автор каталога с QID Достоевского и две работы: одна матчится по названию.
	var authorID, idiotID, otherID int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO authors (last_name, first_name, middle_name, normalized_name, ext_ids)
		VALUES ('Достоевский', 'Фёдор', 'Михайлович', 'достоевский фёдор михайлович', '{"wd_qid":"Q991"}') RETURNING id`).Scan(&authorID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO works (title, normalized_title, primary_author_id, written_year) VALUES ('Идиот', 'идиот', $1, 1868) RETURNING id`, authorID).Scan(&idiotID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO works (title, normalized_title, primary_author_id, written_year) VALUES ('Неизвестная повесть', 'неизвестная повесть', $1, 1870) RETURNING id`, authorID).Scan(&otherID))
	var noQID int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO authors (last_name, first_name, middle_name, normalized_name) VALUES ('Безквидов', 'Иван', '', 'безквидов иван') RETURNING id`).Scan(&noQID))

	tl, err := svc.Timeline(ctx, authorID, false)
	require.NoError(t, err)
	require.True(t, tl.Available)
	require.Equal(t, "Q991", tl.QID)
	require.NotEmpty(t, tl.Chapters)
	require.NotEmpty(t, tl.Context, "фон региона ru")
	for _, e := range tl.Events {
		require.Nil(t, e.Evidence, "evidence наружу не отдаём")
	}
	var idiot *WorkOut
	for i := range tl.Works {
		if tl.Works[i].Ref == "idiot" {
			idiot = &tl.Works[i]
		}
	}
	require.NotNil(t, idiot)
	require.Equal(t, idiotID, idiot.WorkID, "«Идиот» сопоставлен по названию")

	empty, err := svc.Timeline(ctx, noQID, false)
	require.NoError(t, err)
	require.False(t, empty.Available)
	_, err = svc.Timeline(ctx, 999999, false)
	require.ErrorIs(t, err, ErrNotFound)

	// Окно для «Идиота» — период из датасета (1867–1869), смерть Сони линкована.
	lc, err := svc.LifeContextForWork(ctx, idiotID)
	require.NoError(t, err)
	require.True(t, lc.Available)
	require.Equal(t, bio.Written{From: 1867, To: 1869}, *lc.Period)
	require.Equal(t, "Достоевский Фёдор Михайлович", lc.Author.Name)
	found := false
	for _, it := range lc.Items {
		if it.Event.Key == "loss-1868" {
			found = it.Linked
		}
	}
	require.True(t, found)
	// Работа без пары в датасете — окно по written_year.
	lc2, err := svc.LifeContextForWork(ctx, otherID)
	require.NoError(t, err)
	require.True(t, lc2.Available)
	require.Equal(t, bio.Written{From: 1868, To: 1870}, *lc2.Period)

	// Скрытие: неадмин не видит, админ видит с флагом, окно тоже без неё.
	require.NoError(t, svc.SetHidden(ctx, "Q991", "loss-1868", true, 1))
	require.ErrorIs(t, svc.SetHidden(ctx, "Q991", "nope-1868", true, 1), ErrNotFound)
	tl, err = svc.Timeline(ctx, authorID, false)
	require.NoError(t, err)
	for _, e := range tl.Events {
		require.NotEqual(t, "loss-1868", e.Key)
	}
	tlA, err := svc.Timeline(ctx, authorID, true)
	require.NoError(t, err)
	hiddenSeen := false
	for _, e := range tlA.Events {
		if e.Key == "loss-1868" {
			hiddenSeen = e.Hidden
		}
	}
	require.True(t, hiddenSeen)
	lc, err = svc.LifeContextForWork(ctx, idiotID)
	require.NoError(t, err)
	for _, it := range lc.Items {
		require.NotEqual(t, "loss-1868", it.Event.Key)
	}

	// Скрытие переживает пересев (другая версия → полный upsert).
	svc2, err := New(pool, &bio.PackFile{Path: pack.Path, Version: pack.Version + "x"}, nil)
	require.NoError(t, err)
	require.NoError(t, svc2.Seed(ctx))
	var hides int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM author_event_hides`).Scan(&hides))
	require.Equal(t, 1, hides)
	require.NoError(t, svc.SetHidden(ctx, "Q991", "loss-1868", false, 1))

	st, err := svc.Stats(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, st.AuthorsWithTimeline)
	require.Equal(t, 1, st.AuthorsWithQID)
	require.Equal(t, 0, st.HiddenEvents)
}
