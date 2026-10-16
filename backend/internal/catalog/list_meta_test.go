package catalog_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/catalog"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestService_ListGenresAndCover — #518: строки работ на карточках автора и серии
// несут жанры (объединение по видимым изданиям работы, как genres работы в
// Meili) и обложку (издание, у которого она уже извлечена); издание со скрытым
// жанром не протаскивает ни жанр, ни обложку.
func TestService_ListGenresAndCover(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)

	var collID, archID, authorID, seriesID, workID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO authors (last_name, normalized_name) VALUES ('Брэдбери','брэдбери рэй') RETURNING id`).Scan(&authorID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO series (title, normalized_title, author_id) VALUES ('Марс','марс',$1) RETURNING id`, authorID).Scan(&seriesID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO works (title, normalized_title, primary_author_id, series_id, ser_no) VALUES ('Марсианские хроники','марсианские хроники',$1,$2,1) RETURNING id`,
		authorID, seriesID).Scan(&workID))
	genre := func(code string) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO genres (fb2_code) VALUES ($1) ON CONFLICT (fb2_code) DO UPDATE SET fb2_code = EXCLUDED.fb2_code RETURNING id`,
			code).Scan(&id))
		return id
	}
	edition := func(lib, cover string, genres ...string) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, lang, series_id, ser_no, work_id, cover_path)
			VALUES ($1,$2,$3,'f','fb2','Марсианские хроники','марсианские хроники','ru',$4,1,$5,NULLIF($6,'')) RETURNING id`,
			collID, archID, lib, seriesID, workID, cover).Scan(&id))
		_, err := pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,0)`, id, authorID)
		require.NoError(t, err)
		for _, g := range genres {
			_, err := pool.Exec(ctx, `INSERT INTO book_genres (book_id, genre_id) VALUES ($1,$2)`, id, genre(g))
			require.NoError(t, err)
		}
		return id
	}
	// Издания идут в списке свежими первыми (date_added DESC NULLS LAST, затем
	// normalized_title) — представитель здесь любой из трёх; проверяем свойства.
	edition("E1", "", "sf_social")
	withCover := edition("E2", "covers/e2.jpg", "sf", "sf_social")
	edition("E3", "covers/e3.jpg", "sf_horror")

	svc := catalog.New(pool)
	a, err := svc.GetAuthor(ctx, authorID, 0, nil, nil, false)
	require.NoError(t, err)
	require.Len(t, a.Books, 1)
	b := a.Books[0]
	require.Equal(t, []string{"sf", "sf_horror", "sf_social"}, b.Genres, "объединение по изданиям, по коду")
	require.NotEmpty(t, b.CoverPath)

	// Скрыт жанр sf_horror — издание E3 не видно: ни его жанра, ни его обложки.
	a, err = svc.GetAuthor(ctx, authorID, 0, []string{"sf_horror"}, nil, false)
	require.NoError(t, err)
	require.Len(t, a.Books, 1)
	b = a.Books[0]
	require.Equal(t, []string{"sf", "sf_social"}, b.Genres)
	require.Equal(t, "covers/e2.jpg", b.CoverPath)
	if b.ID != withCover {
		require.Equal(t, withCover, b.CoverEditionID, "обложка — с издания, где она извлечена")
	}

	s, err := svc.GetSeries(ctx, seriesID, 0, []string{"sf_horror"}, nil, false)
	require.NoError(t, err)
	require.Len(t, s.Books, 1)
	require.Equal(t, []string{"sf", "sf_social"}, s.Books[0].Genres)
	require.Equal(t, "covers/e2.jpg", s.Books[0].CoverPath)
	if s.Books[0].ID != withCover {
		require.Equal(t, withCover, s.Books[0].CoverEditionID)
	}
}

// TestService_MergeRejections — #517: пары «не объединять» среди работ списка
// приходят в ответах автора и серии (подсказки слияния их не предлагают).
func TestService_MergeRejections(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)

	var collID, archID, authorID, seriesID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO authors (last_name, normalized_name) VALUES ('Гэлбрейт','гэлбрейт роберт') RETURNING id`).Scan(&authorID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO series (title, normalized_title, author_id) VALUES ('Страйк','страйк',$1) RETURNING id`, authorID).Scan(&seriesID))
	work := func(lib, title string) int64 {
		var workID, bookID int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO works (title, normalized_title, primary_author_id, series_id, ser_no) VALUES ($1, lower($1), $2, $3, 7) RETURNING id`,
			title, authorID, seriesID).Scan(&workID))
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, lang, series_id, ser_no, work_id)
			VALUES ($1,$2,$3,'f','fb2',$4,lower($4),'ru',$5,7,$6) RETURNING id`,
			collID, archID, lib, title, seriesID, workID).Scan(&bookID))
		_, err := pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,0)`, bookID, authorID)
		require.NoError(t, err)
		return workID
	}
	a := work("A", "Развороченная могила")
	b := work("B", "Неизбежная могила")
	work("C", "Чужая книга") // третья работа серии — без отказов

	svc := catalog.New(pool)
	s, err := svc.GetSeries(ctx, seriesID, 0, nil, nil, false)
	require.NoError(t, err)
	require.Empty(t, s.MergeRejections)

	_, err = pool.Exec(ctx, `INSERT INTO work_merge_rejections (work_a, work_b) VALUES ($1, $2)`, min(a, b), max(a, b))
	require.NoError(t, err)
	s, err = svc.GetSeries(ctx, seriesID, 0, nil, nil, false)
	require.NoError(t, err)
	require.Equal(t, [][2]int64{{min(a, b), max(a, b)}}, s.MergeRejections)
	au, err := svc.GetAuthor(ctx, authorID, 0, nil, nil, false)
	require.NoError(t, err)
	require.Equal(t, [][2]int64{{min(a, b), max(a, b)}}, au.MergeRejections)
}

// TestService_YearStatsByWork — #529: гистограмма «по годам написания» считает
// РАБОТЫ: издания одной работы с разными fb2-годами — одна книга в год работы.
func TestService_YearStatsByWork(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)

	var collID, archID, authorID, seriesID, hp1, hp2 int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO authors (last_name, normalized_name) VALUES ('Роулинг','роулинг джоан') RETURNING id`).Scan(&authorID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO series (title, normalized_title, author_id) VALUES ('Гарри Поттер','гарри поттер',$1) RETURNING id`, authorID).Scan(&seriesID))
	work := func(title string, year any, serNo int) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO works (title, normalized_title, primary_author_id, series_id, ser_no, written_year) VALUES ($1, lower($1), $2, $3, $4, $5) RETURNING id`,
			title, authorID, seriesID, serNo, year).Scan(&id))
		return id
	}
	hp1 = work("Гарри Поттер и философский камень", 1997, 1)
	hp2 = work("Гарри Поттер и Тайная комната", nil, 2) // год работы не известен
	n := 0
	edition := func(workID int64, year int) {
		n++
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, lang, series_id, work_id, written_year)
			VALUES ($1,$2,$3,'f','fb2','Издание','издание','ru',$4,$5,NULLIF($6, 0)) RETURNING id`,
			collID, archID, fmt.Sprintf("L%d", n), seriesID, workID, year).Scan(&id))
		_, err := pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,0)`, id, authorID)
		require.NoError(t, err)
	}
	edition(hp1, 1997)
	edition(hp1, 2002) // fb2 издателя вписал год издания
	edition(hp1, 2012)
	edition(hp2, 1999)
	edition(hp2, 1998)
	noYear := work("Сказки барда Бидля", nil, 3) // ни у работы, ни у изданий года нет
	edition(noYear, 0)

	want := []catalog.YearCount{
		{Year: 1997, Count: 1, Books: []catalog.YearBook{{WorkID: hp1, Title: "Гарри Поттер и философский камень"}}},
		{Year: 1998, Count: 1, Books: []catalog.YearBook{{WorkID: hp2, Title: "Гарри Поттер и Тайная комната"}}},
	}
	strip := func(ys []catalog.YearCount) []catalog.YearCount {
		for i := range ys {
			for j := range ys[i].Books {
				require.NotZero(t, ys[i].Books[j].ID, "id издания для совместимости")
				ys[i].Books[j].ID = 0
			}
		}
		return ys
	}
	svc := catalog.New(pool)
	a, err := svc.GetAuthor(ctx, authorID, 0, nil, nil, false)
	require.NoError(t, err)
	require.Equal(t, want, strip(a.YearStats), "работа один раз, в год работы; без года работы — самый ранний год изданий")
	require.Equal(t, 1, a.YearUnknown, "работа без года — в подписи покрытия, не на гистограмме")
	s, err := svc.GetSeries(ctx, seriesID, 0, nil, nil, false)
	require.NoError(t, err)
	require.Equal(t, want, strip(s.YearStats))
	require.Equal(t, 1, s.YearUnknown)
}
