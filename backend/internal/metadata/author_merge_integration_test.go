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

// TestMergeAuthors_Integration — слияние авторов (#308): «Лукьяненко Сергей»
// (2 книги, подписчик, био) в «Лукьяненко Сергей Васильевич».
func TestMergeAuthors_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	collID, archID := seedTitleFixture(t, ctx, pool)
	short := seedGroupAuthor(t, ctx, pool, "Лукьяненко", "лукьяненко сергей")
	full := seedGroupAuthor(t, ctx, pool, "Лукьяненко", "лукьяненко сергей васильевич")
	_, err := pool.Exec(ctx, `UPDATE authors SET first_name='Сергей', bio='Сергей Лукьяненко — писатель', photo_path='l.jpg' WHERE id=$1`, short)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE authors SET first_name='Сергей', middle_name='Васильевич' WHERE id=$1`, full)
	require.NoError(t, err)
	co := seedGroupAuthor(t, ctx, pool, "Перумов", "перумов ник")

	watch := seedGroupBook(t, ctx, pool, collID, archID, short, "L1", "Ночной дозор", "ночной дозор", "ru", "", "", "")
	duo := seedGroupBook(t, ctx, pool, collID, archID, short, "L2", "Не время для драконов", "не время для драконов", "ru", "", "", "")
	seedBookAuthors(t, ctx, pool, duo, short, co)
	mine := seedGroupBook(t, ctx, pool, collID, archID, full, "L3", "Черновик", "черновик", "ru", "", "", "")

	var user int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ('u@x','U','x','user') RETURNING id`).Scan(&user))
	_, err = pool.Exec(ctx, `INSERT INTO favorite_authors (user_id, author_id) VALUES ($1, $2)`, user, short)
	require.NoError(t, err)

	ctl := NewOverrideController(pool, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, err = ctl.MergeAuthors(ctx, short, short, 0)
	require.ErrorIs(t, err, ErrMergeSameAuthor)
	_, err = ctl.MergeAuthors(ctx, short, 999999, 0)
	require.ErrorIs(t, err, ErrMergeAuthorNotFound)

	works, err := ctl.MergeAuthors(ctx, short, full, 0)
	require.NoError(t, err)
	require.Len(t, works, 2)
	require.Equal(t, []int64{full}, ovAuthors(t, ctx, pool, watch))
	require.Equal(t, []int64{full, co}, ovAuthors(t, ctx, pool, duo), "соавтор на месте, порядок сохранён")
	require.Equal(t, []int64{full}, ovAuthors(t, ctx, pool, mine))

	var subs int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM favorite_authors WHERE author_id=$1 AND user_id=$2`, full, user).Scan(&subs))
	require.Equal(t, 1, subs, "подписка переехала")
	var bio string
	require.NoError(t, pool.QueryRow(ctx, `SELECT COALESCE(bio,'') FROM authors WHERE id=$1`, full).Scan(&bio))
	require.Equal(t, "Сергей Лукьяненко — писатель", bio, "у цели не было био — взято у источника")

	// Цикл: цель уже не может слиться обратно в источник.
	_, err = ctl.MergeAuthors(ctx, full, short, 0)
	require.ErrorIs(t, err, ErrMergeSameAuthor)

	// Импорт вернул источник в издание — правка авторов работы восстанавливает.
	seedBookAuthors(t, ctx, pool, watch, short)
	_, err = ctl.ReapplyAfterImport(ctx)
	require.NoError(t, err)
	require.Equal(t, []int64{full}, ovAuthors(t, ctx, pool, watch))

	// Новая книга источника из следующего INPX переезжает к цели.
	fresh := seedGroupBook(t, ctx, pool, collID, archID, short, "L4", "Лабиринт отражений", "лабиринт отражений", "ru", "", "", "")
	n, err := ctl.ReapplyAuthorMerges(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, []int64{full}, ovAuthors(t, ctx, pool, fresh))
	n, err = ctl.ReapplyAuthorMerges(ctx)
	require.NoError(t, err)
	require.Zero(t, n, "у источника больше нет книг — переносить нечего")
}
