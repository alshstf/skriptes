package catalog_test

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/catalog"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestAuthorYearsActive_ByWorks — годы активности автора (карточка, список
// авторов, подсказка дублей) — по годам РАБОТ, а не изданий: у издания опечатка
// fb2 «1014», а год работы, очищенный правилами правдоподобия, — 1989 (прод
// 2026-10-17: «Лукьяненко Сергей Васильевич · 1014–2026» в подсказке дубля).
func TestAuthorYearsActive_ByWorks(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	seedAuthorsList(t, ctx, pool)
	svc := catalog.New(pool)

	author := func(first, middle, norm string) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO authors (last_name, first_name, middle_name, normalized_name) VALUES ('Лукьяненко', $1, $2, $3)
			RETURNING id`, first, middle, norm).Scan(&id))
		return id
	}
	main := author("Сергей", "Васильевич", "лукьяненко сергей васильевич")
	dup := author("Сергей", "", "лукьяненко сергей")
	n := 0
	book := func(authorID int64, title string, workYear, bookYear int) {
		n++
		var wid, bid int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO works (title, normalized_title, primary_author_id, written_year) VALUES ($1::text, $1::text, $2, $3)
			RETURNING id`, title, authorID, workYear).Scan(&wid))
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, lang, work_id, written_year)
			SELECT b.collection_id, b.archive_id, $1::text, 'f', 'fb2', $2::text, $2::text, 'ru', $3, $4
			FROM books b LIMIT 1 RETURNING id`, "yr-"+string(rune('a'+n)), title, wid, bookYear).Scan(&bid))
		_, err := pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id, position) VALUES ($1, $2, 0)`, bid, authorID)
		require.NoError(t, err)
	}
	book(main, "Нарушение", 1989, 1014) // опечатка года у издания
	book(main, "Черновик", 2005, 2005)
	book(dup, "Испытание", 1990, 1990)
	_, err := pool.Exec(ctx, `UPDATE authors SET book_count = 2 WHERE id IN ($1, $2)`, main, dup)
	require.NoError(t, err)

	card, err := svc.GetAuthor(ctx, main, 0, nil, nil, false)
	require.NoError(t, err)
	require.NotNil(t, card.YearsActive)
	require.Equal(t, catalog.YearsRange{From: 1989, To: 2005}, *card.YearsActive, "карточка — по годам работ")

	list, err := svc.ListAuthorsFiltered(ctx, catalog.AuthorListParams{Query: "лукьяненко сергей васильевич"})
	require.NoError(t, err)
	var found bool
	for _, a := range list.Items {
		if a.ID == main {
			found = true
			require.NotNil(t, a.YearsActive)
			require.Equal(t, 1989, a.YearsActive.From, "список авторов — по годам работ")
		}
	}
	require.True(t, found)

	dups, err := svc.AuthorDuplicates(ctx, dup)
	require.NoError(t, err)
	require.NotEmpty(t, dups)
	require.Equal(t, main, dups[0].ID)
	require.NotNil(t, dups[0].YearsActive)
	require.Equal(t, 1989, dups[0].YearsActive.From, "подсказка дубля — по годам работ")
}
