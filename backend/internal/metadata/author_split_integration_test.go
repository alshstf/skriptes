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

// TestSplitAuthor_Integration — разделение автора (#356): «Берг Николай» — поэт XIX
// века и автор сетевой литературы под одним именем.
func TestSplitAuthor_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	collID, archID := seedTitleFixture(t, ctx, pool)
	berg := seedGroupAuthor(t, ctx, pool, "Берг", "берг николай")
	_, err := pool.Exec(ctx, `UPDATE authors SET first_name='Николай', bio='Николай Васильевич Берг — поэт', photo_path='berg.jpg',
		metadata_fetched_at=now() WHERE id=$1`, berg)
	require.NoError(t, err)
	coauthor := seedGroupAuthor(t, ctx, pool, "Соавтор", "соавтор")
	gladiator := seedGroupBook(t, ctx, pool, collID, archID, berg, "B1", "Гладиатор", "гладиатор", "ru", "", "", "")
	poems := seedGroupBook(t, ctx, pool, collID, archID, berg, "B2", "Стихотворения", "стихотворения", "ru", "", "", "")
	shift := seedGroupBook(t, ctx, pool, collID, archID, berg, "B3", "Ночная смена", "ночная смена", "ru", "", "", "")
	// У «Стихотворений» есть соавтор — он остаётся на своём месте.
	seedBookAuthors(t, ctx, pool, poems, coauthor, berg)
	other := seedGroupBook(t, ctx, pool, collID, archID, coauthor, "C1", "Чужая", "чужая", "ru", "", "", "")

	ctl := NewOverrideController(pool, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	works := []int64{workIDOf(t, ctx, pool, gladiator), workIDOf(t, ctx, pool, poems)}

	// Ошибки: без уточнения, чужая работа, несуществующий автор.
	_, err = ctl.SplitAuthor(ctx, berg, "  ", works, 0)
	require.ErrorIs(t, err, ErrSplitAuthorNote)
	_, err = ctl.SplitAuthor(ctx, berg, "поэт", []int64{workIDOf(t, ctx, pool, other)}, 0)
	require.ErrorIs(t, err, ErrSplitAuthorWorks)
	_, err = ctl.SplitAuthor(ctx, 999999, "поэт", works, 0)
	require.ErrorIs(t, err, ErrOverrideTargetNotFound)

	poet, err := ctl.SplitAuthor(ctx, berg, "поэт", works, 0)
	require.NoError(t, err)
	require.NotEqual(t, berg, poet)
	var last, first, note string
	require.NoError(t, pool.QueryRow(ctx, `SELECT last_name, first_name, name_note FROM authors WHERE id=$1`, poet).Scan(&last, &first, &note))
	require.Equal(t, []string{"Берг", "Николай", "поэт"}, []string{last, first, note})

	require.Equal(t, []int64{poet}, ovAuthors(t, ctx, pool, gladiator))
	require.Equal(t, []int64{coauthor, poet}, ovAuthors(t, ctx, pool, poems), "соавтор на месте")
	require.Equal(t, []int64{berg}, ovAuthors(t, ctx, pool, shift), "невыбранная работа осталась")

	// Био и фото прежнего сброшены, прежние значения — в журнале.
	var bio, photo *string
	var fetched *time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT bio, photo_path, metadata_fetched_at FROM authors WHERE id=$1`, berg).Scan(&bio, &photo, &fetched))
	require.Nil(t, bio)
	require.Nil(t, photo)
	require.Nil(t, fetched, "обогащение пройдёт заново")
	var journaled int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM author_meta_recheck WHERE author_id=$1 AND action='cleared'`, berg).Scan(&journaled))
	require.Equal(t, 2, journaled)

	// Импорт переписал авторов изданий — правка применяется снова.
	seedBookAuthors(t, ctx, pool, gladiator, berg)
	_, err = ctl.ReapplyAfterImport(ctx)
	require.NoError(t, err)
	require.Equal(t, []int64{poet}, ovAuthors(t, ctx, pool, gladiator))

	// Повторное разделение с тем же уточнением — к тому же автору.
	again, err := ctl.SplitAuthor(ctx, berg, "Поэт", []int64{workIDOf(t, ctx, pool, shift)}, 0)
	require.NoError(t, err)
	require.Equal(t, poet, again)

	// Откат по работе возвращает её прежнему автору.
	require.NoError(t, ctl.RevertOverride(ctx, "work", works[0], "authors"))
	require.Equal(t, []int64{berg}, ovAuthors(t, ctx, pool, gladiator))
}
