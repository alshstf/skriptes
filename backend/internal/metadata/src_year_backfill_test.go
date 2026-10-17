package metadata

import (
	"archive/zip"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestBackfillSrcYears — разовый догон года оригинала (#533 этап 4): переводы
// архива получают books.src_year из <src-title-info><date>, год работы
// пересчитывается (раньше fb2-года переиздания — побеждает, источник fb2_src);
// год до 1450 у нестаринной книги не берётся; повторный запуск — ничего не делает.
func TestBackfillSrcYears(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	collID, archID := seedTitleFixture(t, ctx, pool)
	author := seedGroupAuthor(t, ctx, pool, "Дик", "дик филип")

	fb2 := func(srcDate string) string {
		return `<?xml version="1.0"?><FictionBook><description>
			<title-info><book-title>Перевод</book-title><date>2005</date></title-info>
			<src-title-info><book-title>Original</book-title>` + srcDate + `</src-title-info>
			</description><body><p>t</p></body></FictionBook>`
	}
	root := t.TempDir()
	f, err := os.Create(filepath.Join(root, "a.zip"))
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	for name, body := range map[string]string{
		"1.fb2": fb2(`<date value="1962-01-01">1962</date>`),
		"2.fb2": fb2(`<date>1073</date>`), // опечатка/заглушка — не берём
		"3.fb2": fb2(``),                  // даты оригинала нет
	} {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(body))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	require.NoError(t, f.Close())

	book := func(lib, file, title string) int64 {
		id := seedGroupBook(t, ctx, pool, collID, archID, author, lib, title, title, "ru", "Original "+lib, "en", "")
		_, err := pool.Exec(ctx, `UPDATE books SET file_name = $2, written_year = 2005, written_year_source = 'fb2_title' WHERE id = $1`, id, file)
		require.NoError(t, err)
		return id
	}
	castle := book("B1", "1", "Человек в высоком замке")
	typo := book("B2", "2", "Убик")
	none := book("B3", "3", "Валис")
	for _, id := range []int64{castle, typo, none} {
		_, err := RecomputeWorkYears(ctx, pool, []int64{workIDOf(t, ctx, pool, id)})
		require.NoError(t, err)
	}

	n, err := BackfillSrcYears(ctx, pool, root, nil, quiet)
	require.NoError(t, err)
	require.Equal(t, 2, n, "год оригинала есть у двух изданий (1962 и 1073)")

	workYear := func(book int64) (y int, src string) {
		require.NoError(t, pool.QueryRow(ctx, `SELECT written_year, written_year_source FROM works WHERE id = $1`,
			workIDOf(t, ctx, pool, book)).Scan(&y, &src))
		return
	}
	y, src := workYear(castle)
	require.Equal(t, 1962, y, "год оригинала раньше fb2-года переиздания")
	require.Equal(t, "fb2_src", src)
	y, src = workYear(typo)
	require.Equal(t, 2005, y, "1073 у нестаринной книги — не год")
	require.Equal(t, "fb2_title", src)
	y, _ = workYear(none)
	require.Equal(t, 2005, y)

	st, err := loadSrcYearsState(ctx, pool)
	require.NoError(t, err)
	require.True(t, st.Done)
	n, err = BackfillSrcYears(ctx, pool, root, nil, quiet)
	require.NoError(t, err)
	require.Zero(t, n, "второй запуск — догон уже сделан")
}
