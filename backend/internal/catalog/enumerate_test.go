package catalog_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/catalog"
	"github.com/skriptes/skriptes/backend/internal/db"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestEnumerate — проверяет ListAuthors/ListSeries/ListGenres:
//   - стабильная сортировка по фамилии/title/display
//   - правильный COUNT(*) для total
//   - корректное использование limit/offset
//   - book_count агрегация (LEFT JOIN не теряет авторов с нулём книг,
//     но deleted=true книги в счёт не идут)
//
// Использует только postgres (без meili / fixture INPX) — данные
// сидируем напрямую SQL'ем, чтобы не тащить весь импорт.
func TestEnumerate(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startEnumeratePostgres(t, ctx)

	// Seed: коллекция + архив + 2 автора + 2 серии + 3 жанра + 3 книги.
	var collID, archID, aliceID, bobID, sBlueID, sRedID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t', 't.inpx') RETURNING id`,
	).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1, 'a.zip') RETURNING id`,
		collID).Scan(&archID))

	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO authors (last_name, first_name, middle_name, normalized_name)
		VALUES ('Alice', 'A', '', 'alice a') RETURNING id
	`).Scan(&aliceID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO authors (last_name, first_name, middle_name, normalized_name)
		VALUES ('Bob', 'B', '', 'bob b') RETURNING id
	`).Scan(&bobID))

	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO series (title, normalized_title) VALUES ('Blue', 'blue') RETURNING id`,
	).Scan(&sBlueID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO series (title, normalized_title) VALUES ('Red', 'red') RETURNING id`,
	).Scan(&sRedID))

	// Жанры с обоими name полями и без них — проверим fallback display.
	var gFantasyID, gDramaID int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO genres (fb2_code, name_ru, name_en) VALUES ('sf', 'Фантастика', 'SF') RETURNING id
	`).Scan(&gFantasyID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO genres (fb2_code, name_en) VALUES ('drama', 'Drama') RETURNING id
	`).Scan(&gDramaID))
	_, err := pool.Exec(ctx, `INSERT INTO genres (fb2_code) VALUES ('weird-no-name')`)
	require.NoError(t, err)

	// 3 книги: 2 у Alice (одна в серии Blue, одна в Red, одна deleted),
	// 0 у Bob (проверяем что LEFT JOIN не теряет авторов с нулём книг).
	var b1, b2, b3 int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, series_id)
		VALUES ($1, $2, 'L1', 'f1', 'fb2', 'Book 1', 'book 1', $3) RETURNING id
	`, collID, archID, sBlueID).Scan(&b1))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, series_id)
		VALUES ($1, $2, 'L2', 'f2', 'fb2', 'Book 2', 'book 2', $3) RETURNING id
	`, collID, archID, sRedID).Scan(&b2))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, deleted)
		VALUES ($1, $2, 'L3', 'f3', 'fb2', 'Book 3 deleted', 'book 3 deleted', true) RETURNING id
	`, collID, archID).Scan(&b3))

	_, err = pool.Exec(ctx,
		`INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,0),($3,$2,0),($4,$2,0)`,
		b1, aliceID, b2, b3)
	require.NoError(t, err)
	// Жанры: b1→Fantasy (живая), b2→Drama (живая), b3→Drama (deleted —
	// не должен считаться). Это даёт нам "fantasy=1, drama=1, weird=0".
	_, err = pool.Exec(ctx,
		`INSERT INTO book_genres (book_id, genre_id) VALUES ($1,$2),($3,$4),($5,$4)`,
		b1, gFantasyID, b2, gDramaID, b3)
	require.NoError(t, err)

	svc := catalog.New(pool)

	// ── ListAuthors ──
	authors, total, err := svc.ListAuthors(ctx, 10, 0)
	require.NoError(t, err)
	require.Equal(t, 2, total)
	require.Len(t, authors, 2)
	// Сортировка по фамилии: Alice → Bob
	require.Equal(t, "Alice", firstWord(authors[0].FullName))
	require.Equal(t, "Bob", firstWord(authors[1].FullName))
	// Alice: 2 книги (b1, b2), b3 deleted не считается
	require.Equal(t, 2, authors[0].BookCount)
	// Bob: 0 (нет book_authors → LEFT JOIN отдаёт NULL → COUNT == 0)
	require.Equal(t, 0, authors[1].BookCount)

	// Пагинация: limit=1 offset=0 → только Alice
	page1, total2, err := svc.ListAuthors(ctx, 1, 0)
	require.NoError(t, err)
	require.Equal(t, 2, total2)
	require.Len(t, page1, 1)
	require.Equal(t, "Alice", firstWord(page1[0].FullName))
	page2, _, err := svc.ListAuthors(ctx, 1, 1)
	require.NoError(t, err)
	require.Len(t, page2, 1)
	require.Equal(t, "Bob", firstWord(page2[0].FullName))

	// ── ListSeries ──
	series, total, err := svc.ListSeries(ctx, 10, 0)
	require.NoError(t, err)
	require.Equal(t, 2, total)
	require.Len(t, series, 2)
	// Сортировка по title: Blue, Red
	require.Equal(t, "Blue", series[0].Title)
	require.Equal(t, "Red", series[1].Title)
	require.Equal(t, 1, series[0].BookCount) // только b1
	require.Equal(t, 1, series[1].BookCount) // только b2
	// AuthorName пустой — в тестовых данных series.author_id не выставляли.

	// ── ListGenres ──
	genres, err := svc.ListGenres(ctx, 0)
	require.NoError(t, err)
	require.Len(t, genres, 3)
	// Display fallback: RU → EN → code
	byCode := map[string]catalog.GenreEntry{}
	for _, g := range genres {
		byCode[g.Code] = g
	}
	require.Equal(t, "Фантастика", byCode["sf"].Display)
	require.Equal(t, "Drama", byCode["drama"].Display)
	require.Equal(t, "weird-no-name", byCode["weird-no-name"].Display)
	// Book count: sf=1 (b1), drama=1 (b2 — b3 deleted не считается), weird-no-name=0
	require.Equal(t, 1, byCode["sf"].BookCount)
	require.Equal(t, 1, byCode["drama"].BookCount)
	require.Equal(t, 0, byCode["weird-no-name"].BookCount)
	// CategoryCode/CategoryName — пустые в этом тесте: мы не задавали
	// parent_id (только internal/genres.Seed его заполняет, и здесь
	// его не вызывали). Подтверждаем что nil-parent корректно отдаёт ""
	// без падений из-за NULL JOIN.
	for _, g := range genres {
		require.Empty(t, g.CategoryCode, "no parent_id set in this fixture")
		require.Empty(t, g.CategoryName)
	}
}

// TestListGenres_WithCategory — проверяет что LEFT JOIN на parent
// корректно отдаёт CategoryCode/CategoryName когда leaf имеет parent_id.
// Сценарий имитирует то что делает internal/genres.Seed.
func TestListGenres_WithCategory(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startEnumeratePostgres(t, ctx)
	svc := catalog.New(pool)

	// Pseudo-родитель + leaf c parent_id
	var parentID int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO genres (fb2_code, name_ru) VALUES ('cat:sf', 'Фантастика') RETURNING id
	`).Scan(&parentID))
	_, err := pool.Exec(ctx, `
		INSERT INTO genres (fb2_code, name_ru, parent_id) VALUES ('sf_action', 'Боевая фантастика', $1)
	`, parentID)
	require.NoError(t, err)

	got, err := svc.ListGenres(ctx, 0)
	require.NoError(t, err)

	// cat:sf скрыт фильтром «NOT LIKE 'cat:%'»; виден только leaf
	require.Len(t, got, 1)
	require.Equal(t, "sf_action", got[0].Code)
	require.Equal(t, "Боевая фантастика", got[0].Display)
	require.Equal(t, "cat:sf", got[0].CategoryCode)
	require.Equal(t, "Фантастика", got[0].CategoryName)
}

// TestListGenres_IsFavorite — userID > 0 проставляет IsFavorite по таблице
// user_favorite_genres; userID == 0 (аноним/OPDS) → всегда false.
func TestListGenres_IsFavorite(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startEnumeratePostgres(t, ctx)
	svc := catalog.New(pool)

	var userID, gFavID, gOtherID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ('f@e.com','F','x','user') RETURNING id`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO genres (fb2_code, name_ru) VALUES ('sf', 'Фантастика') RETURNING id`).Scan(&gFavID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO genres (fb2_code, name_ru) VALUES ('detective', 'Детектив') RETURNING id`).Scan(&gOtherID))
	_, err := pool.Exec(ctx,
		`INSERT INTO user_favorite_genres (user_id, genre_id) VALUES ($1, $2)`, userID, gFavID)
	require.NoError(t, err)

	// userID > 0: один жанр избран, другой нет.
	got, err := svc.ListGenres(ctx, userID)
	require.NoError(t, err)
	byID := map[int64]catalog.GenreEntry{}
	for _, g := range got {
		byID[g.ID] = g
	}
	require.True(t, byID[gFavID].IsFavorite)
	require.False(t, byID[gOtherID].IsFavorite)

	// userID == 0: избранного нет ни у кого.
	anon, err := svc.ListGenres(ctx, 0)
	require.NoError(t, err)
	for _, g := range anon {
		require.False(t, g.IsFavorite, "аноним: is_favorite всегда false")
	}
}

// TestListLanguages — языки коллекции с числом книг: deleted и NULL-lang
// не считаются, сортировка по убыванию количества (tiebreak — код),
// display-имя берётся из словаря (fallback — код в верхнем регистре).
func TestListLanguages(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startEnumeratePostgres(t, ctx)

	var collID, archID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t', 't.inpx') RETURNING id`,
	).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1, 'a.zip') RETURNING id`,
		collID).Scan(&archID))

	// ru: 2 живые + 1 deleted (не считается); en: 1; xyz: 1 (неизвестный код
	// → fallback display); 1 книга без lang (NULL → исключается).
	ins := func(lib, lang string, deleted bool) {
		_, err := pool.Exec(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, lang, deleted)
			VALUES ($1, $2, $3, 'f', 'fb2', $4, $5, $6, $7)
		`, collID, archID, lib, lib, lib, lang, deleted)
		require.NoError(t, err)
	}
	ins("r1", "ru", false)
	ins("r2", "ru", false)
	ins("r3", "ru", true)
	ins("e1", "en", false)
	ins("x1", "xyz", false)
	_, err := pool.Exec(ctx, `
		INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, deleted)
		VALUES ($1, $2, 'n1', 'f', 'fb2', 'n1', 'n1', false)
	`, collID, archID)
	require.NoError(t, err)

	langs, err := catalog.New(pool).ListLanguages(ctx)
	require.NoError(t, err)

	byCode := map[string]catalog.LanguageEntry{}
	for _, l := range langs {
		byCode[l.Code] = l
	}
	require.Len(t, langs, 3, "ru, en, xyz; NULL-lang исключён")
	require.Equal(t, 2, byCode["ru"].BookCount, "deleted ru не считается")
	require.Equal(t, 1, byCode["en"].BookCount)
	require.Equal(t, 1, byCode["xyz"].BookCount)
	require.Equal(t, "Русский", byCode["ru"].Display)
	require.Equal(t, "Английский", byCode["en"].Display)
	require.Equal(t, "XYZ", byCode["xyz"].Display, "неизвестный код → код в верхнем регистре")
	// Сортировка: ru(2) первым; en/xyz по 1 — tiebreak по коду (en < xyz).
	require.Equal(t, "ru", langs[0].Code)
	require.Equal(t, "en", langs[1].Code)
	require.Equal(t, "xyz", langs[2].Code)
}

// firstWord — мини-helper для проверки сортировки по фамилии без
// зависимости от форматирования fullName.
func firstWord(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] == ' ' {
			return s[:i]
		}
	}
	return s
}

func startEnumeratePostgres(t *testing.T, ctx context.Context) *pgxpool.Pool {
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
