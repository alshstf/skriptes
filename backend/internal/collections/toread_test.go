package collections_test

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/collections"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestCollections_ToReadShelf — служебная полка «Хочу прочитать» (0068, план
// recsys/03 3.3): имя зарезервировано, в списке сразу после «Избранного»,
// переименовать и удалить нельзя.
func TestCollections_ToReadShelf(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	svc := collections.New(pool)

	var u int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ('tr@e.com','T','x','user') RETURNING id`).Scan(&u))

	_, err := svc.CreateCollection(ctx, u, "хочу прочитать")
	require.ErrorIs(t, err, collections.ErrReservedName)
	mine, err := svc.CreateCollection(ctx, u, "Моя")
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `INSERT INTO user_collections (user_id, name, kind) VALUES ($1,'Хочу прочитать','to_read'), ($1,'Избранное','favorites')`, u)
	require.NoError(t, err)
	// Вторая «Хочу прочитать» — нельзя (partial unique).
	_, err = pool.Exec(ctx, `INSERT INTO user_collections (user_id, name, kind) VALUES ($1,'Ещё','to_read')`, u)
	require.Error(t, err)

	list, err := svc.ListCollections(ctx, u)
	require.NoError(t, err)
	kinds := make([]string, 0, len(list))
	for _, c := range list {
		kinds = append(kinds, c.Kind)
	}
	require.Equal(t, []string{"favorites", "to_read", "user"}, kinds)

	toRead := list[1].ID
	require.ErrorIs(t, svc.RenameCollection(ctx, u, toRead, "X"), collections.ErrSystemCollection)
	require.ErrorIs(t, svc.RenameCollection(ctx, u, mine.ID, "Хочу прочитать"), collections.ErrReservedName)
	require.ErrorIs(t, svc.DeleteCollection(ctx, u, toRead), collections.ErrSystemCollection)
}
