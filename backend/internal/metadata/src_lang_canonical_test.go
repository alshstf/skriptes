package metadata

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestCanonicalizeSrcLangs — #287: записанные коды языка оригинала приводятся к
// ISO 639-1, мусор обнуляется; возвращаются работы только изменённых изданий.
func TestCanonicalizeSrcLangs(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	collID, archID := seedTitleFixture(t, ctx, pool)
	author := seedGroupAuthor(t, ctx, pool, "Тест", "тест автор")

	ids := map[string]int64{}
	for i, code := range []string{"spa", "jp", "английски", "?", "en", "grc"} {
		ids[code] = seedGroupBook(t, ctx, pool, collID, archID, author, "L"+string(rune('a'+i)),
			"Книга "+code, "книга "+code, "ru", "Original", code, "")
	}
	works, err := CanonicalizeSrcLangs(ctx, pool)
	require.NoError(t, err)

	srcLang := func(id int64) string {
		var v *string
		require.NoError(t, pool.QueryRow(ctx, `SELECT src_lang FROM books WHERE id = $1`, id).Scan(&v))
		if v == nil {
			return "<nil>"
		}
		return *v
	}
	require.Equal(t, "es", srcLang(ids["spa"]))
	require.Equal(t, "ja", srcLang(ids["jp"]))
	require.Equal(t, "en", srcLang(ids["английски"]))
	require.Equal(t, "<nil>", srcLang(ids["?"]))
	require.Equal(t, "en", srcLang(ids["en"]))
	require.Equal(t, "grc", srcLang(ids["grc"]))
	require.ElementsMatch(t, []int64{
		workIDOf(t, ctx, pool, ids["spa"]), workIDOf(t, ctx, pool, ids["jp"]),
		workIDOf(t, ctx, pool, ids["английски"]), workIDOf(t, ctx, pool, ids["?"]),
	}, works)

	works, err = CanonicalizeSrcLangs(ctx, pool)
	require.NoError(t, err)
	require.Empty(t, works, "повторный проход ничего не меняет")
}
