package collections_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/collections"
	"github.com/skriptes/skriptes/backend/internal/db"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestCollectionsFlow — полный жизненный цикл полки на реальном PG.
// Сценарий: создать → переименовать → добавить/убрать книги → перечислить →
// удалить. Плюс проверка изоляции между пользователями (чужую полку не видно).
func TestCollectionsFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startCollectionsPostgres(t, ctx)

	// Seed: 2 пользователя + библиотечная коллекция (collections = INPX-импорт,
	// НЕ user_collections) + архив + 2 живые книги + 1 deleted.
	var u1, u2, libColl, archID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ('a@e.com','A','x','user') RETURNING id`).Scan(&u1))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ('b@e.com','B','x','user') RETURNING id`).Scan(&u2))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('lib','lib.inpx') RETURNING id`).Scan(&libColl))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, libColl).Scan(&archID))

	mkBook := func(lib, title string, deleted bool) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, deleted)
			VALUES ($1,$2,$3,$4,'fb2',$5,$6,$7) RETURNING id
		`, libColl, archID, lib, lib, title, title, deleted).Scan(&id))
		return id
	}
	b1 := mkBook("L1", "Book One", false)
	b2 := mkBook("L2", "Book Two", false)
	bDel := mkBook("L3", "Deleted Book", true)

	svc := collections.New(pool)

	// Пустое имя → ошибка.
	_, err := svc.CreateCollection(ctx, u1, "   ")
	require.ErrorIs(t, err, collections.ErrEmptyName)

	// Создать полку (имя триммится).
	c, err := svc.CreateCollection(ctx, u1, "  Любимое  ")
	require.NoError(t, err)
	require.Equal(t, "Любимое", c.Name)
	require.Greater(t, c.ID, int64(0))

	// Список: одна полка, 0 книг.
	list, err := svc.ListCollections(ctx, u1)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, 0, list[0].BookCount)

	// Переименовать.
	require.NoError(t, svc.RenameCollection(ctx, u1, c.ID, "Прочитать летом"))
	list, err = svc.ListCollections(ctx, u1)
	require.NoError(t, err)
	require.Equal(t, "Прочитать летом", list[0].Name)

	// Добавить две живые книги + одну удалённую; повторный add — идемпотентен.
	require.NoError(t, svc.AddBookToCollection(ctx, u1, c.ID, b1))
	require.NoError(t, svc.AddBookToCollection(ctx, u1, c.ID, b1)) // no-op
	require.NoError(t, svc.AddBookToCollection(ctx, u1, c.ID, b2))
	require.NoError(t, svc.AddBookToCollection(ctx, u1, c.ID, bDel))

	// book_count считает только живые → 2 (bDel не считается).
	list, err = svc.ListCollections(ctx, u1)
	require.NoError(t, err)
	require.Equal(t, 2, list[0].BookCount)

	// ListCollectionBooks: только живые, свежедобавленная (b2) сверху.
	books, err := svc.ListCollectionBooks(ctx, u1, c.ID)
	require.NoError(t, err)
	require.Len(t, books, 2, "deleted-книга исключена")
	ids := []int64{books[0].ID, books[1].ID}
	require.Contains(t, ids, b1)
	require.Contains(t, ids, b2)
	require.NotContains(t, ids, bDel)

	// CollectionsForBook — членство per-book для индикации на карточке.
	shB1, err := svc.CollectionsForBook(ctx, u1, b1)
	require.NoError(t, err)
	require.Len(t, shB1, 1)
	require.Equal(t, c.ID, shB1[0].ID)
	require.Equal(t, list[0].Name, shB1[0].Name) // текущее имя (полку выше переименовали)
	// Книга не на полках → пусто.
	bNone := mkBook("L4", "No Shelf", false)
	none, err := svc.CollectionsForBook(ctx, u1, bNone)
	require.NoError(t, err)
	require.Empty(t, none)
	// Чужой пользователь не видит членство в чужих полках.
	shU2, err := svc.CollectionsForBook(ctx, u2, b1)
	require.NoError(t, err)
	require.Empty(t, shU2)

	// Убрать книгу (идемпотентно).
	require.NoError(t, svc.RemoveBookFromCollection(ctx, u1, c.ID, b1))
	require.NoError(t, svc.RemoveBookFromCollection(ctx, u1, c.ID, b1)) // no-op
	books, err = svc.ListCollectionBooks(ctx, u1, c.ID)
	require.NoError(t, err)
	require.Len(t, books, 1)
	require.Equal(t, b2, books[0].ID)

	// ── Изоляция между пользователями ──
	// u2 не видит полку u1.
	listU2, err := svc.ListCollections(ctx, u2)
	require.NoError(t, err)
	require.Empty(t, listU2)
	// u2 не может читать/менять чужую полку → ErrNotFound.
	_, err = svc.ListCollectionBooks(ctx, u2, c.ID)
	require.ErrorIs(t, err, collections.ErrNotFound)
	err = svc.RenameCollection(ctx, u2, c.ID, "Взлом")
	require.ErrorIs(t, err, collections.ErrNotFound)
	err = svc.AddBookToCollection(ctx, u2, c.ID, b1)
	require.ErrorIs(t, err, collections.ErrNotFound)
	err = svc.RemoveBookFromCollection(ctx, u2, c.ID, b2)
	require.ErrorIs(t, err, collections.ErrNotFound)
	err = svc.DeleteCollection(ctx, u2, c.ID)
	require.ErrorIs(t, err, collections.ErrNotFound)

	// Удалить свою полку; повторное удаление → ErrNotFound.
	require.NoError(t, svc.DeleteCollection(ctx, u1, c.ID))
	err = svc.DeleteCollection(ctx, u1, c.ID)
	require.ErrorIs(t, err, collections.ErrNotFound)

	// Несуществующая полка для операций.
	require.True(t, errors.Is(svc.RenameCollection(ctx, u1, 999999, "x"), collections.ErrNotFound))
}

// TestCollections_FavoritesGuards — служебная полка «Избранное» (kind='favorites'):
// дубль руками нельзя, переименовать/удалить нельзя, закреплена первой в списке.
func TestCollections_FavoritesGuards(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := startCollectionsPostgres(t, ctx)

	var u int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ('fav@e.com','F','x','user') RETURNING id`).Scan(&u))
	svc := collections.New(pool)

	// Дубль «Избранное» руками создать нельзя (регистр/пробелы тоже).
	_, err := svc.CreateCollection(ctx, u, "Избранное")
	require.ErrorIs(t, err, collections.ErrReservedName)
	_, err = svc.CreateCollection(ctx, u, "  избранное  ")
	require.ErrorIs(t, err, collections.ErrReservedName)

	// Служебная полка (как её создаёт AddFavorite).
	var favID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO user_collections (user_id, name, kind) VALUES ($1, 'Избранное', 'favorites') RETURNING id`, u).Scan(&favID))
	require.ErrorIs(t, svc.RenameCollection(ctx, u, favID, "Другое"), collections.ErrSystemCollection)
	require.ErrorIs(t, svc.RenameCollection(ctx, u, favID, "Избранное"), collections.ErrReservedName)
	require.ErrorIs(t, svc.DeleteCollection(ctx, u, favID), collections.ErrSystemCollection)

	// Обычную полку создаём, она НЕ favorites; в списке favorites — первой.
	_, err = svc.CreateCollection(ctx, u, "Прочитать")
	require.NoError(t, err)
	list, err := svc.ListCollections(ctx, u)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(list), 2)
	require.Equal(t, "favorites", list[0].Kind, "служебная «Избранное» закреплена сверху")
	require.Equal(t, "Избранное", list[0].Name)
}

func startCollectionsPostgres(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	pgC, err := tcpostgres.Run(ctx,
		"postgres:17-alpine",
		tcpostgres.WithDatabase("skriptes_test"),
		tcpostgres.WithUsername("skriptes"),
		tcpostgres.WithPassword("skriptes"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pgC.Terminate(context.Background()) })
	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, db.Migrate(dsn))
	pool, err := db.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}
