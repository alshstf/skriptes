package catalog_test

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/catalog"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestGetAuthor_CapCountsWorksNotEditions — #274: предел карточки автора считается
// по РАБОТАМ, и у каждой показанной работы счётчик изданий полный. Раньше LIMIT
// стоял на изданиях до схлопывания — у плодовитых авторов пропадали работы, а
// счётчики изданий занижались.
func TestGetAuthor_CapCountsWorksNotEditions(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	seedAuthorsList(t, ctx, pool) // коллекция и архив для изданий
	svc := catalog.New(pool)

	var authorID int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO authors (last_name, first_name, normalized_name) VALUES ('Плодовитый', 'Автор', 'плодовитый автор')
		RETURNING id`).Scan(&authorID))
	for w := 1; w <= 3; w++ {
		var workID int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO works (title, normalized_title, primary_author_id) VALUES ($1::text, $1::text, $2) RETURNING id`,
			"работа "+string(rune('0'+w)), authorID).Scan(&workID))
		for e := 1; e <= 2; e++ {
			lib := "cap-" + string(rune('0'+w)) + "-" + string(rune('0'+e))
			var bid int64
			require.NoError(t, pool.QueryRow(ctx, `
				INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, lang, work_id, date_added)
				SELECT b.collection_id, b.archive_id, $1::text, 'f', 'fb2', $2::text, $2::text, 'ru', $3, DATE '2020-01-01' + $4::int
				FROM books b LIMIT 1 RETURNING id`, lib, "работа "+string(rune('0'+w)), workID, w*10+e).Scan(&bid))
			_, err := pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id, position) VALUES ($1, $2, 0)`, bid, authorID)
			require.NoError(t, err)
		}
	}

	full, err := svc.GetAuthor(ctx, authorID, 0, nil, nil, false)
	require.NoError(t, err)
	require.Len(t, full.Books, 3)

	restore := catalog.SetAuthorWorksCapForTest(2)
	defer restore()
	capped, err := svc.GetAuthor(ctx, authorID, 0, nil, nil, false)
	require.NoError(t, err)
	require.Len(t, capped.Books, 2, "предел — две РАБОТЫ, а не два издания")
	for _, b := range capped.Books {
		require.Equal(t, 2, b.EditionCount, "у показанной работы учтены все издания: %s", b.Title)
		require.NotEqual(t, "работа 1", b.Title, "показываются самые свежие работы")
	}
}
