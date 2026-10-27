package hiding_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skriptes/skriptes/backend/internal/hiding"
	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/skriptes/skriptes/backend/internal/testpg"
)

// StatusOf: автор и работа — явное скрытие и флаг; книга явно не скрывается —
// только флаг и «из-за чего» (автор, работа). Для книги запрос явного скрытия
// раньше уходил с лишним аргументом, и статус издания всегда был 500 (#611).
func TestStatusOf(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool, _ := testpg.Start(t, ctx)
	store := hiding.New(pool)
	imp := importer.New(importer.Deps{Pool: pool})

	var collID, archID, author, other, work, hiddenWork, book, bookOfHiddenWork int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO authors (last_name, normalized_name) VALUES ('Скрытый', 'скрытый') RETURNING id`).Scan(&author))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO authors (last_name, normalized_name) VALUES ('Другой', 'другой') RETURNING id`).Scan(&other))
	mkWork := func(title string, primary int64) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO works (title, normalized_title, primary_author_id) VALUES ($1, lower($1), $2) RETURNING id`,
			title, primary).Scan(&id))
		return id
	}
	mkBook := func(lib string, workID, authorID int64) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id)
			VALUES ($1,$2,$3::text,'f','fb2',$3::text,$3::text,$4) RETURNING id`,
			collID, archID, lib, workID).Scan(&id))
		_, err := pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,0)`, id, authorID)
		require.NoError(t, err)
		return id
	}
	work = mkWork("Своя", author)
	book = mkBook("b1", work, author)
	hiddenWork = mkWork("Явно скрытая", other)
	bookOfHiddenWork = mkBook("b2", hiddenWork, other)

	// Ничего не скрыто: книга видна, без ошибки.
	st, err := store.StatusOf(ctx, hiding.KindBook, book)
	require.NoError(t, err)
	require.Equal(t, hiding.Status{Via: []hiding.Ref{}}, st)

	require.NoError(t, store.Hide(ctx, hiding.KindAuthor, author, 0, ""))
	require.NoError(t, store.Hide(ctx, hiding.KindWork, hiddenWork, 0, ""))
	_, _, err = imp.RecomputeInstanceHidden(ctx)
	require.NoError(t, err)

	st, err = store.StatusOf(ctx, hiding.KindAuthor, author)
	require.NoError(t, err)
	require.True(t, st.Hidden)
	require.True(t, st.Explicit)

	st, err = store.StatusOf(ctx, hiding.KindWork, work)
	require.NoError(t, err)
	require.True(t, st.Hidden)
	require.False(t, st.Explicit, "работа скрыта из-за автора, не сама")
	require.Equal(t, []hiding.Ref{{Kind: "author", ID: author, Name: "Скрытый"}}, st.Via)

	st, err = store.StatusOf(ctx, hiding.KindBook, book)
	require.NoError(t, err)
	require.True(t, st.Hidden)
	require.False(t, st.Explicit)
	require.ElementsMatch(t, []hiding.Ref{
		{Kind: "author", ID: author, Name: "Скрытый"},
		{Kind: "work", ID: work, Name: "Своя"},
	}, st.Via)

	st, err = store.StatusOf(ctx, hiding.KindBook, bookOfHiddenWork)
	require.NoError(t, err)
	require.True(t, st.Hidden)
	require.Equal(t, []hiding.Ref{{Kind: "work", ID: hiddenWork, Name: "Явно скрытая"}}, st.Via)
}
