package db_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/db"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestMigration0039_DedupBooks — база, куда импортированы два INPX одной
// библиотеки (local_fb2 и flib): одни и те же (архив, lib_id) дважды. Миграция
// 0039 схлопывает их в одну книгу, переносит данные пользователей, удаляет
// лишние книги/архивы/опустевшие работы и ставит новые ключи.
func TestMigration0039_DedupBooks(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pgC, err := postgres.Run(ctx,
		"postgres:17-alpine",
		postgres.WithDatabase("skriptes_test"),
		postgres.WithUsername("skriptes"),
		postgres.WithPassword("skriptes"),
		testcontainers.WithWaitStrategy(
			wait.ForAll(
				wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
				wait.ForListeningPort("5432/tcp"),
			).WithStartupTimeoutDefault(60*time.Second),
		),
	)
	testcontainers.CleanupContainer(t, pgC)
	require.NoError(t, err)
	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	require.NoError(t, db.MigrateTo(dsn, 38))
	pool, err := db.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	id := func(q string, args ...any) int64 {
		t.Helper()
		var v int64
		require.NoError(t, pool.QueryRow(ctx, q, args...).Scan(&v))
		return v
	}
	exec := func(q string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, q, args...)
		require.NoError(t, err)
	}

	c1 := id(`INSERT INTO collections (name, inpx_filename) VALUES ('local','librusec_local_fb2.inpx') RETURNING id`)
	c2 := id(`INSERT INTO collections (name, inpx_filename) VALUES ('flib','librusec_flib.inpx') RETURNING id`)
	a1 := id(`INSERT INTO archives (collection_id, filename) VALUES ($1,'x.zip') RETURNING id`, c1)
	a2 := id(`INSERT INTO archives (collection_id, filename) VALUES ($1,'y.zip') RETURNING id`, c1)
	a3 := id(`INSERT INTO archives (collection_id, filename) VALUES ($1,'x.zip') RETURNING id`, c2) // тот же архив
	a4 := id(`INSERT INTO archives (collection_id, filename) VALUES ($1,'z.zip') RETURNING id`, c2)
	work := func(title string) int64 {
		return id(`INSERT INTO works (title, normalized_title) VALUES ($1, $2) RETURNING id`, title, title)
	}
	w1, w2, w3, w4, w5 := work("w1"), work("w2"), work("w3"), work("w4"), work("w5")
	book := func(coll, arch int64, lib string, w int64) int64 {
		return id(`INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id)
			VALUES ($1,$2,$3,$3,'fb2','t','t',$4) RETURNING id`, coll, arch, lib, w)
	}
	b1 := book(c1, a1, "100", w1) // старая копия
	b2 := book(c2, a3, "100", w2) // дубль b1, с ручной правкой → остаётся он
	b3 := book(c1, a2, "200", w3) // без дублей
	b4 := book(c2, a3, "300", w4) // только во flib, архив x.zip
	b5 := book(c1, a1, "400", w5) // дубли в одной работе, правки у обоих
	b6 := book(c2, a3, "400", w5)

	u := id(`INSERT INTO users (email, display_name, password_hash, role) VALUES ('u@x','U','h','user') RETURNING id`)
	shelf := id(`INSERT INTO user_collections (user_id, name) VALUES ($1,'полка') RETURNING id`, u)
	exec(`INSERT INTO reads (user_id, book_id, fraction, updated_at) VALUES ($1,$2,0.5, now() - interval '1 day')`, u, b1)
	exec(`INSERT INTO reads (user_id, book_id, fraction) VALUES ($1,$2,0.9)`, u, b5)
	exec(`INSERT INTO reads (user_id, book_id, fraction) VALUES ($1,$2,0.1)`, u, b6)
	exec(`INSERT INTO views (user_id, book_id) VALUES ($1,$2)`, u, b1)
	exec(`INSERT INTO user_collection_books (collection_id, book_id) VALUES ($1,$2), ($1,$3)`, shelf, b1, b6)
	exec(`INSERT INTO book_ratings (user_id, work_id, rating) VALUES ($1,$2,4)`, u, w1)
	override := func(kind string, target int64, field string) {
		exec(`INSERT INTO metadata_overrides (target_kind, target_id, field, override_value, original_value)
			VALUES ($1,$2,$3,'"v"','"o"')`, kind, target, field)
	}
	override("book", b2, "isbn")
	override("book", b5, "isbn")
	override("book", b6, "publisher")
	override("work", w1, "title")

	require.NoError(t, db.Migrate(dsn))

	ids := func(q string, args ...any) []int64 {
		t.Helper()
		rows, err := pool.Query(ctx, q, args...)
		require.NoError(t, err)
		defer rows.Close()
		var out []int64
		for rows.Next() {
			var v int64
			require.NoError(t, rows.Scan(&v))
			out = append(out, v)
		}
		require.NoError(t, rows.Err())
		return out
	}

	require.Equal(t, []int64{b2, b3, b4, b5}, ids(`SELECT id FROM books ORDER BY id`),
		"b1 уступил b2 (у b2 правка), b6 уступил b5 (правки у обоих — остаётся старший)")
	require.Equal(t, []int64{a1, a2, a4}, ids(`SELECT id FROM archives ORDER BY id`), "x.zip — одна строка")
	require.Equal(t, []int64{a1, a2, a1, a1}, ids(`SELECT archive_id FROM books ORDER BY id`))

	var frac float64
	require.NoError(t, pool.QueryRow(ctx, `SELECT fraction FROM reads WHERE user_id=$1 AND book_id=$2`, u, b2).Scan(&frac))
	require.InDelta(t, 0.5, frac, 1e-6, "чтение b1 переехало на b2")
	require.NoError(t, pool.QueryRow(ctx, `SELECT fraction FROM reads WHERE user_id=$1 AND book_id=$2`, u, b5).Scan(&frac))
	require.InDelta(t, 0.9, frac, 1e-6, "у b5 было своё чтение — оно и осталось")
	require.Equal(t, []int64{b2, b5}, ids(`SELECT book_id FROM reads ORDER BY book_id`))
	require.Equal(t, []int64{b2}, ids(`SELECT book_id FROM views`))
	require.Equal(t, []int64{b2, b5}, ids(`SELECT book_id FROM user_collection_books ORDER BY book_id`))

	require.Equal(t, []int64{w2}, ids(`SELECT work_id FROM book_ratings`), "оценка опустевшей w1 — на работу b2")
	require.Equal(t, []int64{w2, w3, w4, w5}, ids(`SELECT id FROM works ORDER BY id`), "w1 опустела и удалена")
	require.Equal(t, []int64{1}, ids(`SELECT edition_count FROM works WHERE id = $1`, w5))

	require.Equal(t, []int64{b2, b5}, ids(`SELECT target_id FROM metadata_overrides WHERE target_kind='book' ORDER BY target_id`))
	require.Empty(t, ids(`SELECT target_id FROM metadata_overrides WHERE target_kind='work'`))

	var raw []byte
	require.NoError(t, pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key='book_identity_dedup_v1'`).Scan(&raw))
	var purge struct {
		Books       []int64 `json:"books"`
		WorksGone   []int64 `json:"works_gone"`
		WorksUpdate []int64 `json:"works_update"`
	}
	require.NoError(t, json.Unmarshal(raw, &purge))
	require.Equal(t, []int64{b1, b6}, purge.Books)
	require.Equal(t, []int64{w1}, purge.WorksGone)
	require.Equal(t, []int64{w2, w5}, purge.WorksUpdate)

	_, err = pool.Exec(ctx, `INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title)
		VALUES ($1,$2,'100','f','fb2','t','t')`, c1, a1)
	require.Error(t, err, "(архив, lib_id) уникален независимо от коллекции")
	_, err = pool.Exec(ctx, `INSERT INTO archives (collection_id, filename) VALUES ($1,'x.zip')`, c2)
	require.Error(t, err, "имя архива уникально")

	// Откат и повторное применение: на схлопнутых данных оба проходят.
	require.NoError(t, db.MigrateTo(dsn, 38))
	require.NoError(t, db.Migrate(dsn))
	assertConstraints(t, ctx, pool)
}

func assertConstraints(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT conname FROM pg_constraint
		WHERE conrelid IN ('books'::regclass, 'archives'::regclass) AND contype = 'u'
		ORDER BY conname`)
	require.NoError(t, err)
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		require.NoError(t, rows.Scan(&n))
		names = append(names, n)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{"archives_filename_key", "books_archive_id_lib_id_key"}, names)
}
