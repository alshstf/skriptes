package genres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/genres"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

func TestCanonicalCodes(t *testing.T) {
	require.Equal(t, "adventure", genres.Canonical("adv_all"))
	require.Equal(t, "nonf_biography", genres.Canonical("Биографии и мемуары"))
	require.Equal(t, "sf_fantasy_city", genres.Canonical("urban-fantasy"))
	require.Equal(t, "wuxia", genres.Canonical("wuxia"), "неочевидные не сводим")
	require.Equal(t, []string{"adventure", "sf_litrpg"}, genres.CanonicalCodes([]string{"adv_all", "adventure", "litrpg"}))
}

// Каждый алиас должен вести в код словаря, и сам не быть кодом словаря.
func TestAliases_PointIntoDictionary(t *testing.T) {
	entries, err := genres.Dictionary()
	require.NoError(t, err)
	known := map[string]bool{}
	for _, e := range entries {
		known[e.Code] = true
	}
	for _, alias := range []string{"adv_all", "painting", "popadancy", "humor_all", "litrpg", "шпионский детектив"} {
		canon := genres.Canonical(alias)
		require.NotEqual(t, alias, canon)
		require.Truef(t, known[canon], "%q → %q нет в словаре", alias, canon)
		require.Falsef(t, known[alias], "%q — сам код словаря", alias)
	}
}

// TestMergeAliases_Integration — #286: книги, избранное, скрытые жанры и ручные
// правки переезжают с алиаса на код словаря, строка алиаса удаляется.
func TestMergeAliases_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	_, err := genres.Seed(ctx, pool)
	require.NoError(t, err)

	id := func(sql string, args ...any) int64 {
		t.Helper()
		var v int64
		require.NoError(t, pool.QueryRow(ctx, sql, args...).Scan(&v))
		return v
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		_, err := pool.Exec(ctx, sql, args...)
		require.NoError(t, err)
	}
	alias := id(`INSERT INTO genres (fb2_code, name_ru) VALUES ('adv_all', 'adv_all') RETURNING id`)
	canon := id(`SELECT id FROM genres WHERE fb2_code = 'adventure'`)
	coll := id(`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`)
	arch := id(`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, coll)
	work := id(`INSERT INTO works (title, normalized_title) VALUES ('w','w') RETURNING id`)
	book := id(`INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id)
		VALUES ($1,$2,'1','f','fb2','b','b',$3) RETURNING id`, coll, arch, work)
	both := id(`INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id)
		VALUES ($1,$2,'2','f','fb2','c','c',$3) RETURNING id`, coll, arch, work)
	exec(`INSERT INTO book_genres (book_id, genre_id) VALUES ($1,$2), ($3,$2), ($3,$4)`, book, alias, both, canon)
	user := id(`INSERT INTO users (email, display_name, password_hash, role) VALUES ('u@x','U','h','user') RETURNING id`)
	exec(`INSERT INTO user_favorite_genres (user_id, genre_id) VALUES ($1,$2)`, user, alias)
	exec(`INSERT INTO app_settings (key, value) VALUES ('content', '{"hidden_genres":["adv_all","adventure"],"hidden_languages":["en"]}')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`)
	exec(`INSERT INTO metadata_overrides (target_kind, target_id, field, override_value, original_value)
		VALUES ('work', $1, 'genres', '{"codes":["adv_all","sf"]}', '[{"book_id":1,"codes":["adv_all"]}]')`, work)

	works, merged, err := genres.MergeAliases(ctx, pool)
	require.NoError(t, err)
	require.Equal(t, 1, merged)
	require.Equal(t, []int64{work}, works)

	require.Equal(t, int64(0), id(`SELECT count(*) FROM genres WHERE fb2_code = 'adv_all'`))
	require.Equal(t, int64(2), id(`SELECT count(*) FROM book_genres WHERE genre_id = $1`, canon))
	require.Equal(t, int64(1), id(`SELECT count(*) FROM user_favorite_genres WHERE user_id = $1 AND genre_id = $2`, user, canon))
	var content struct {
		HiddenGenres    []string `json:"hidden_genres"`
		HiddenLanguages []string `json:"hidden_languages"`
	}
	var raw []byte
	require.NoError(t, pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = 'content'`).Scan(&raw))
	require.NoError(t, json.Unmarshal(raw, &content))
	require.Equal(t, []string{"adventure"}, content.HiddenGenres, "алиас сведён, повтор убран")
	require.Equal(t, []string{"en"}, content.HiddenLanguages, "остальное не тронуто")
	var ov, orig string
	require.NoError(t, pool.QueryRow(ctx, `SELECT override_value::text, original_value::text FROM metadata_overrides WHERE target_id = $1`, work).Scan(&ov, &orig))
	require.JSONEq(t, `{"codes":["adventure","sf"]}`, ov)
	require.JSONEq(t, `[{"book_id":1,"codes":["adventure"]}]`, orig)

	works, merged, err = genres.MergeAliases(ctx, pool)
	require.NoError(t, err)
	require.Zero(t, merged)
	require.Empty(t, works)
}
