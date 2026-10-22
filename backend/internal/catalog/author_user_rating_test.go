package catalog_test

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/catalog"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestAuthorUserRating — «ваша средняя — 4.5 по 6 книгам» (план recsys/03,
// задача 3.6): среднее оценок ТЕКУЩЕГО пользователя по работам автора; чужие
// оценки и издания одной работы не считаются; без пользователя или без
// оценок — поля пустые.
func TestAuthorUserRating(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	f := seedAuthorsList(t, ctx, pool)
	svc := catalog.New(pool)

	var me int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ('me@e','Me','x','user') RETURNING id`).Scan(&me))
	// Работы Кинга: «Оно» (два издания) и ещё одна.
	rows, err := pool.Query(ctx, `
		SELECT DISTINCT b.work_id FROM book_authors ba JOIN books b ON b.id = ba.book_id
		WHERE ba.author_id = $1 AND b.work_id IS NOT NULL ORDER BY b.work_id`, f.kingID)
	require.NoError(t, err)
	var works []int64
	for rows.Next() {
		var id int64
		require.NoError(t, rows.Scan(&id))
		works = append(works, id)
	}
	rows.Close()
	require.Len(t, works, 2)

	// Без оценок — нет поля; без пользователя — тоже.
	card, err := svc.GetAuthor(ctx, f.kingID, me, nil, nil, false)
	require.NoError(t, err)
	require.Nil(t, card.UserRating)
	require.Equal(t, 0, card.UserRatingCount)

	_, err = pool.Exec(ctx, `INSERT INTO book_ratings (user_id, work_id, rating) VALUES ($1,$2,5), ($1,$3,4)`, me, works[0], works[1])
	require.NoError(t, err)
	// Чужая оценка — не в счёт.
	var stranger int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ('other@e','Other','x','user') RETURNING id`).Scan(&stranger))
	_, err = pool.Exec(ctx, `INSERT INTO book_ratings (user_id, work_id, rating) VALUES ($1,$2,1)`, stranger, works[0])
	require.NoError(t, err)

	card, err = svc.GetAuthor(ctx, f.kingID, me, nil, nil, false)
	require.NoError(t, err)
	require.NotNil(t, card.UserRating)
	require.InDelta(t, 4.5, *card.UserRating, 1e-9)
	require.Equal(t, 2, card.UserRatingCount, "по работам, издания «Оно» не удваивают")

	anon, err := svc.GetAuthor(ctx, f.kingID, 0, nil, nil, false)
	require.NoError(t, err)
	require.Nil(t, anon.UserRating)
	// У другого автора моих оценок нет.
	other, err := svc.GetAuthor(ctx, f.asimovID, me, nil, nil, false)
	require.NoError(t, err)
	require.Nil(t, other.UserRating)
}
