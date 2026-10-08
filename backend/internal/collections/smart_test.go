package collections_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/collections"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestSmartShelves — умные полки (#389): сохранить фильтры, переименовать,
// заменить фильтры, удалить; пустые и мусорные фильтры не сохраняются; чужую
// полку не видно и не изменить; потолок числа полок.
func TestSmartShelves(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	var u1, u2 int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ('a@e.com','A','x','user') RETURNING id`).Scan(&u1))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ('b@e.com','B','x','user') RETURNING id`).Scan(&u2))
	svc := collections.New(pool)

	f := collections.SmartFilters{Genres: []string{"sf", "sf_space"}, Lang: "ru", Unread: true}
	sh, err := svc.CreateSmartShelf(ctx, u1, "  Непрочитанная фантастика ", f)
	require.NoError(t, err)
	require.Equal(t, "Непрочитанная фантастика", sh.Name)

	_, err = svc.CreateSmartShelf(ctx, u1, "Всё", collections.SmartFilters{})
	require.ErrorIs(t, err, collections.ErrBadFilters, "весь каталог — не полка")
	_, err = svc.CreateSmartShelf(ctx, u1, "Сорт", collections.SmartFilters{Lang: "ru", Sort: "rand"})
	require.ErrorIs(t, err, collections.ErrBadFilters)
	_, err = svc.CreateSmartShelf(ctx, u1, " ", f)
	require.ErrorIs(t, err, collections.ErrEmptyName)

	list, err := svc.ListSmartShelves(ctx, u1)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, f, list[0].Filters)
	other, err := svc.ListSmartShelves(ctx, u2)
	require.NoError(t, err)
	require.Empty(t, other, "чужие полки не видны")

	name := "Фантастика"
	_, err = svc.UpdateSmartShelf(ctx, u2, sh.ID, &name, nil)
	require.ErrorIs(t, err, collections.ErrNotFound, "чужую не изменить")
	upd, err := svc.UpdateSmartShelf(ctx, u1, sh.ID, &name, nil)
	require.NoError(t, err)
	require.Equal(t, "Фантастика", upd.Name)
	require.Equal(t, f, upd.Filters, "фильтры без изменений")
	f2 := collections.SmartFilters{Genres: []string{"sf"}, YearFrom: 1960, YearTo: 1980, Sort: "year_asc"}
	upd, err = svc.UpdateSmartShelf(ctx, u1, sh.ID, nil, &f2)
	require.NoError(t, err)
	require.Equal(t, "Фантастика", upd.Name)
	require.Equal(t, f2, upd.Filters)

	require.ErrorIs(t, svc.DeleteSmartShelf(ctx, u2, sh.ID), collections.ErrNotFound)
	require.NoError(t, svc.DeleteSmartShelf(ctx, u1, sh.ID))
	require.ErrorIs(t, svc.DeleteSmartShelf(ctx, u1, sh.ID), collections.ErrNotFound)

	for i := range 50 {
		_, err := svc.CreateSmartShelf(ctx, u2, fmt.Sprintf("Полка %d", i), collections.SmartFilters{YearFrom: 1900 + i})
		require.NoError(t, err)
	}
	_, err = svc.CreateSmartShelf(ctx, u2, "Лишняя", collections.SmartFilters{Lang: "en"})
	require.ErrorIs(t, err, collections.ErrTooMany)
}
