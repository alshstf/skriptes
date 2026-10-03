package catalog_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/catalog"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestAuthorDuplicates — кандидаты в дубли (#308): «ФИ» ↔ «ФИО», одинаковое
// латинское имя; тёзки с уточнением, служебные и авторы без книг — нет.
func TestAuthorDuplicates(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	var collID, archID int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	lib := 0
	mk := func(last, first, middle, note, latin string, renown int, books int) int64 {
		var id int64
		norm := last + " " + first + " " + middle + note
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO authors (last_name, first_name, middle_name, normalized_name, name_note, latin_name, renown)
			VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), $7) RETURNING id`,
			last, first, middle, norm, note, latin, renown).Scan(&id))
		for i := 0; i < books; i++ {
			lib++
			var b int64
			require.NoError(t, pool.QueryRow(ctx, `
				INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, written_year)
				VALUES ($1, $2, $3, 'f', 'fb2', 'T', 't', 2000 + $4) RETURNING id`, collID, archID, strconv.Itoa(lib), i).Scan(&b))
			_, err := pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id, position) VALUES ($1, $2, 0)`, b, id)
			require.NoError(t, err)
		}
		return id
	}
	short := mk("Лукьяненко", "Сергей", "", "", "", 0, 1)
	full := mk("Лукьяненко", "Сергей", "Васильевич", "", "", 1989, 3)
	mk("Лукьяненко", "Сергей", "Иванович", "фантаст", "", 0, 1) // тёзка с уточнением — разделён намеренно
	mk("Лукьяненко", "Сергей", "Петрович", "", "", 0, 0)        // без книг
	doyleA := mk("Дойль", "Артур", "Конан", "", "doyle arthur conan", 1300, 2)
	doyleB := mk("Конан Дойл", "Артур", "", "", "doyle arthur conan", 0, 1)

	svc := catalog.New(pool)
	_, err := catalog.RecomputeAuthorStats(ctx, pool)
	require.NoError(t, err)
	// RecomputeAuthorStats пересчитал latin_name по переводам — их у фикстуры нет; вернём.
	_, err = pool.Exec(ctx, `UPDATE authors SET latin_name = 'doyle arthur conan' WHERE id = ANY($1)`, []int64{doyleA, doyleB})
	require.NoError(t, err)

	got, err := svc.AuthorDuplicates(ctx, short)
	require.NoError(t, err)
	require.Len(t, got, 1, "только «ФИО» с книгами и без уточнения")
	require.Equal(t, full, got[0].ID)
	require.Equal(t, "middle_name", got[0].Reason)
	require.Equal(t, 3, got[0].BookCount)
	require.NotNil(t, got[0].YearsActive)

	got, err = svc.AuthorDuplicates(ctx, doyleA)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, doyleB, got[0].ID)
	require.Equal(t, "latin_name", got[0].Reason)

	pairs, total, err := svc.DuplicatePairs(ctx, 10, 0)
	require.NoError(t, err)
	require.Equal(t, 2, total)
	require.Equal(t, full, pairs[0].B.ID, "от самых известных: Лукьяненко (1989) первым")
	require.Equal(t, short, pairs[0].A.ID)
	require.ElementsMatch(t, []int64{doyleA, doyleB}, []int64{pairs[1].A.ID, pairs[1].B.ID})
}
