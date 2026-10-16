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
	// Неоднозначная группа (#527): «Иванов Николай» и два разных «Иванова Николая
	// …» — какой из них тот же человек, по имени не понять.
	ivanov := mk("Иванов", "Николай", "", "", "", 0, 1)
	ivanovF := mk("Иванов", "Николай", "Фёдорович", "", "", 0, 2)
	ivanovS := mk("Иванов", "Николай", "Серафимович", "", "", 0, 1)

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

	got, err = svc.AuthorDuplicates(ctx, ivanov)
	require.NoError(t, err)
	require.Empty(t, got, "неоднозначная группа без доказательств — не кандидат")
	got, err = svc.AuthorDuplicates(ctx, ivanovS)
	require.NoError(t, err)
	require.Empty(t, got)

	pairs, total, err := svc.DuplicatePairs(ctx, 10, 0)
	require.NoError(t, err)
	require.Equal(t, 2, total, "Ивановых нет — неоднозначно")
	require.Equal(t, full, pairs[0].B.ID, "от самых известных: Лукьяненко (1989) первым")
	require.Equal(t, short, pairs[0].A.ID)
	require.ElementsMatch(t, []int64{doyleA, doyleB}, []int64{pairs[1].A.ID, pairs[1].B.ID})

	// «Не дубликат» (#527): пара уходит и с карточки (с обеих сторон), и со
	// страницы пар; повтор — без ошибки; несуществующий автор — ErrNotFound.
	require.NoError(t, svc.RejectDuplicate(ctx, doyleB, doyleA, 0))
	require.NoError(t, svc.RejectDuplicate(ctx, doyleA, doyleB, 0))
	require.ErrorIs(t, svc.RejectDuplicate(ctx, doyleA, 99999999, 0), catalog.ErrNotFound)
	require.Error(t, svc.RejectDuplicate(ctx, doyleA, doyleA, 0))
	got, err = svc.AuthorDuplicates(ctx, doyleA)
	require.NoError(t, err)
	require.Empty(t, got)
	got, err = svc.AuthorDuplicates(ctx, doyleB)
	require.NoError(t, err)
	require.Empty(t, got)
	pairs, total, err = svc.DuplicatePairs(ctx, 10, 0)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Equal(t, short, pairs[0].A.ID)
	got, err = svc.AuthorDuplicates(ctx, short)
	require.NoError(t, err)
	require.Len(t, got, 1, "чужой отказ не трогает другие пары")
	require.Empty(t, got[0].Evidence)

	// Доказательство разрешает неоднозначность: книга «Иванова Николая» — в
	// авторском цикле «Иванова Николая Фёдоровича» → кандидат он, и только он.
	var seriesID int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO series (title, normalized_title) VALUES ('Цикл','цикл') RETURNING id`).Scan(&seriesID))
	_, err = pool.Exec(ctx, `UPDATE books SET series_id = $1 WHERE id IN (
		SELECT book_id FROM book_authors WHERE author_id = ANY($2))`, seriesID, []int64{ivanov, ivanovF})
	require.NoError(t, err)
	got, err = svc.AuthorDuplicates(ctx, ivanov)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, ivanovF, got[0].ID)
	require.Equal(t, "series", got[0].Evidence)
	got, err = svc.AuthorDuplicates(ctx, ivanovF)
	require.NoError(t, err)
	require.Len(t, got, 1, "с карточки полной записи — та же пара")
	require.Equal(t, ivanov, got[0].ID)
	got, err = svc.AuthorDuplicates(ctx, ivanovS)
	require.NoError(t, err)
	require.Empty(t, got)
	pairs, total, err = svc.DuplicatePairs(ctx, 10, 0)
	require.NoError(t, err)
	require.Equal(t, 2, total, "Лукьяненко + Иванов (по циклу)")
	var found bool
	for _, p := range pairs {
		if p.A.ID == ivanov && p.B.ID == ivanovF {
			found = true
			require.Equal(t, "series", p.A.Evidence)
		}
	}
	require.True(t, found)
}
