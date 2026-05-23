package genres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/db"
	"github.com/skriptes/skriptes/backend/internal/genres"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestDictionary_Parses — unit-only, без БД. Подтверждает что
// dictionary.json валиден и не пустой. Если кто-то случайно сломает
// JSON формат (например ручной edit с trailing comma) — тут поймаем
// сразу, не на startup'е production'а.
func TestDictionary_Parses(t *testing.T) {
	entries, err := genres.Dictionary()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(entries), 200, "should have ~270 genres")

	// Spot-check для конкретных хорошо-известных кодов.
	byCode := map[string]genres.Entry{}
	for _, e := range entries {
		byCode[e.Code] = e
	}
	require.Equal(t, "Боевая фантастика", byCode["sf_action"].NameRu)
	require.Equal(t, "Фантастика", byCode["sf_action"].Category)
	require.Equal(t, "Классический детектив", byCode["det_classic"].NameRu)
	require.Equal(t, "Попаданцы", byCode["popadanec"].NameRu)
	// Каждая запись должна иметь непустые поля.
	for _, e := range entries {
		require.NotEmptyf(t, e.Code, "entry %+v: empty code", e)
		require.NotEmptyf(t, e.NameRu, "entry %+v: empty name_ru", e)
		require.NotEmptyf(t, e.Category, "entry %+v: empty category", e)
	}
}

// TestSeed — интеграционный. Проверяет:
//
//  1. Seed на пустой БД создаёт записи (~270 жанров + 22 категории).
//  2. Известные коды получают локализованные имена (а не fb2_code).
//  3. parent_id ссылается на pseudo-категорию.
//  4. Идемпотентность: повторный вызов не дублирует и не падает.
//  5. Seed перезаписывает legacy `name_ru = fb2_code` (от старого importer'а)
//     на правильное локализованное имя — `ON CONFLICT DO UPDATE`
//     перетирает name_ru авторитетным значением словаря.
//  6. Коды НЕ из словаря (имитируем добавление importer'ом) остаются
//     с NULL name_ru → SELECT с COALESCE возвращает fb2_code как fallback.
//
// Note: миграция 0009 (UPDATE genres SET name_ru = NULL WHERE name_ru =
// fb2_code) выполняется во время db.Migrate ДО запуска теста; она
// действует на legacy строки которые УЖЕ есть в БД на момент апгрейда
// существующей инсталляции. Тут мы INSERT'им после миграций, так что
// её эффект проверить из этого теста нельзя — она проверяется в
// internal/db/db_test.go (если потребуется), а здесь — defense-in-depth
// фокус: Seed справляется с legacy и без помощи миграции.
func TestSeed_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startGenresPostgres(t, ctx)

	// --- 1) Imitate legacy state: importer-old записал name_ru = fb2_code
	_, err := pool.Exec(ctx, `INSERT INTO genres (fb2_code, name_ru) VALUES ('sf_action', 'sf_action')`)
	require.NoError(t, err)
	// + код которого нет в словаре (новый/обскурный)
	_, err = pool.Exec(ctx, `INSERT INTO genres (fb2_code, name_ru) VALUES ('weird_new_code', 'weird_new_code')`)
	require.NoError(t, err)

	// --- 2) Seed: populates known codes + перетирает legacy для известных
	n, err := genres.Seed(ctx, pool)
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, 200)

	// sf_action теперь должен иметь человеческое имя (был «sf_action»)
	var nameRu *string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT name_ru FROM genres WHERE fb2_code = 'sf_action'`,
	).Scan(&nameRu))
	require.NotNil(t, nameRu)
	require.Equal(t, "Боевая фантастика", *nameRu)

	// parent_id должен ссылаться на pseudo-категорию «Фантастика»
	var parentID *int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT parent_id FROM genres WHERE fb2_code = 'sf_action'`,
	).Scan(&parentID))
	require.NotNil(t, parentID)

	var parentCode, parentNameRu string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT fb2_code, name_ru FROM genres WHERE id = $1`, *parentID,
	).Scan(&parentCode, &parentNameRu))
	require.Equal(t, "cat:sf", parentCode)
	require.Equal(t, "Фантастика", parentNameRu)

	// --- 4) Unknown код — Seed его НЕ ТРОГАЕТ (он не в словаре). В нашем
	// тестовом INSERT name_ru было задано 'weird_new_code' (legacy);
	// Seed его не меняет. Cleanup для unknown legacy — задача миграции
	// 0009, она работает на момент апгрейда existing инсталляции.
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT name_ru FROM genres WHERE fb2_code = 'weird_new_code'`,
	).Scan(&nameRu))
	require.NotNil(t, nameRu)
	require.Equal(t, "weird_new_code", *nameRu, "Seed не должен изобретать имя для кодов вне словаря")

	// --- 4a) Если importer добавит новый код ПОСЛЕ Seed'а через
	// upsertGenre (с NULL name_ru — см. importer/upsert.go), он останется
	// с NULL — и pickGenreDisplay/COALESCE сфолбэкнет на код.
	_, err = pool.Exec(ctx,
		`INSERT INTO genres (fb2_code) VALUES ('postseed_new_code')`)
	require.NoError(t, err)
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT name_ru FROM genres WHERE fb2_code = 'postseed_new_code'`,
	).Scan(&nameRu))
	require.Nil(t, nameRu, "новые importer-инсёрты должны давать NULL name_ru")

	// --- 5) Идемпотентность: повторный Seed работает + не дублирует
	var cntBefore int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM genres`).Scan(&cntBefore))

	n2, err := genres.Seed(ctx, pool)
	require.NoError(t, err)
	require.Equal(t, n, n2)

	var cntAfter int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM genres`).Scan(&cntAfter))
	require.Equal(t, cntBefore, cntAfter, "Seed must be idempotent (no duplicates)")
}

func startGenresPostgres(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	pgC, err := postgres.Run(ctx,
		"postgres:17-alpine",
		postgres.WithDatabase("skriptes_test"),
		postgres.WithUsername("skriptes"),
		postgres.WithPassword("skriptes"),
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
