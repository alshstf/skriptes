package catalog

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/books"
)

// ErrNotFound возвращается когда автор / серия с таким id не существует.
var ErrNotFound = errors.New("not found")

// Service — read-only сервис для авторов и серий. Отдельно от books,
// потому что здесь логика агрегаций и SQL-джойнов, а не Meili-поиска.
type Service struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// GetAuthor собирает Author с агрегатами одной серией пайплайна:
//  1. строка автора
//  2. count книг (без deleted)
//  3. топ-5 жанров (по числу книг этого автора в каждом)
//  4. серии (по числу книг автора в каждой)
//  5. до 50 последних книг (deleted скрыты, отсортированы по date_added desc)
//  6. гистограмма по году добавления (year_stats)
//  7. ReadCount — сколько книг этого автора уже скачивал пользователь;
//     заполняется только если userID > 0.
//
// Каждый шаг — отдельный запрос; для 99% карточек это <10 ms total.
// Если когда-то станет горячо — соберём в один CTE.
func (s *Service) GetAuthor(ctx context.Context, id, userID int64) (Author, error) {
	var a Author
	err := s.pool.QueryRow(ctx, `
		SELECT id, last_name, first_name, middle_name
		FROM authors WHERE id = $1
	`, id).Scan(&a.ID, &a.LastName, &a.FirstName, &a.MiddleName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Author{}, ErrNotFound
		}
		return Author{}, fmt.Errorf("query author: %w", err)
	}
	a.FullName = fullName(a.LastName, a.FirstName, a.MiddleName)

	if err := s.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM book_authors ba
		JOIN books b ON b.id = ba.book_id
		WHERE ba.author_id = $1 AND b.deleted = false
	`, id).Scan(&a.BookCount); err != nil {
		return Author{}, fmt.Errorf("count books: %w", err)
	}
	a.BooksTotal = a.BookCount

	genres, err := s.queryAuthorTopGenres(ctx, id, 5)
	if err != nil {
		return Author{}, err
	}
	a.TopGenres = genres

	series, err := s.queryAuthorSeries(ctx, id)
	if err != nil {
		return Author{}, err
	}
	a.Series = series

	bookList, err := s.queryAuthorBooks(ctx, id, 50)
	if err != nil {
		return Author{}, err
	}
	a.Books = bookList

	years, err := s.queryAuthorYearStats(ctx, id)
	if err != nil {
		return Author{}, err
	}
	a.YearStats = years

	if userID > 0 {
		read, err := s.queryAuthorReadCount(ctx, id, userID)
		if err != nil {
			return Author{}, err
		}
		a.ReadCount = read
	}

	return a, nil
}

// GetSeries возвращает серию + список её книг в порядке ser_no (затем title).
// Удалённые книги скрыты — серия "висит" только пока в ней есть живые тома.
//
// Если userID > 0, дополнительно считается ReadCount (сколько книг серии
// уже скачивал текущий пользователь). YearStats считаются всегда.
func (s *Service) GetSeries(ctx context.Context, id, userID int64) (Series, error) {
	var (
		out      Series
		authorID pgtype.Int8
	)
	err := s.pool.QueryRow(ctx, `
		SELECT s.id, s.title, s.author_id,
		       COALESCE(NULLIF(TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name)), ''), '')
		FROM series s
		LEFT JOIN authors a ON a.id = s.author_id
		WHERE s.id = $1
	`, id).Scan(&out.ID, &out.Title, &authorID, &out.AuthorName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Series{}, ErrNotFound
		}
		return Series{}, fmt.Errorf("query series: %w", err)
	}
	if authorID.Valid {
		v := authorID.Int64
		out.AuthorID = &v
	}

	rows, err := s.pool.Query(ctx, `
		SELECT b.id, b.title, b.lib_id,
		       b.lang, b.date_added,
		       COALESCE(
		           array_agg(DISTINCT TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name))) FILTER (WHERE a.id IS NOT NULL),
		           ARRAY[]::text[]
		       )
		FROM books b
		LEFT JOIN book_authors ba ON ba.book_id = b.id
		LEFT JOIN authors a ON a.id = ba.author_id
		WHERE b.series_id = $1 AND b.deleted = false
		GROUP BY b.id
		ORDER BY b.ser_no NULLS LAST, b.normalized_title
	`, id)
	if err != nil {
		return Series{}, fmt.Errorf("query series books: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			b    books.ListItem
			lang pgtype.Text
			dt   pgtype.Date
			auth []string
		)
		if err := rows.Scan(&b.ID, &b.Title, &b.LibID, &lang, &dt, &auth); err != nil {
			return Series{}, err
		}
		if lang.Valid {
			b.Lang = lang.String
		}
		if dt.Valid {
			y := dt.Time.Year()
			b.Year = &y
		}
		b.Authors = auth
		b.Series = out.Title
		out.Books = append(out.Books, b)
	}
	if err := rows.Err(); err != nil {
		return Series{}, err
	}
	out.BookCount = len(out.Books)

	years, err := s.querySeriesYearStats(ctx, id)
	if err != nil {
		return Series{}, err
	}
	out.YearStats = years

	if userID > 0 {
		read, err := s.querySeriesReadCount(ctx, id, userID)
		if err != nil {
			return Series{}, err
		}
		out.ReadCount = read
	}

	return out, nil
}

func (s *Service) queryAuthorTopGenres(ctx context.Context, authorID int64, limit int) ([]GenreCount, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT g.fb2_code, COALESCE(NULLIF(g.name_ru,''), NULLIF(g.name_en,''), g.fb2_code), count(*) as cnt
		FROM book_authors ba
		JOIN books b      ON b.id = ba.book_id AND b.deleted = false
		JOIN book_genres bg ON bg.book_id = b.id
		JOIN genres g     ON g.id = bg.genre_id
		WHERE ba.author_id = $1
		GROUP BY g.fb2_code, g.name_ru, g.name_en
		ORDER BY cnt DESC, g.fb2_code
		LIMIT $2
	`, authorID, limit)
	if err != nil {
		return nil, fmt.Errorf("query top genres: %w", err)
	}
	defer rows.Close()
	var out []GenreCount
	for rows.Next() {
		var g GenreCount
		if err := rows.Scan(&g.Code, &g.Display, &g.Count); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Service) queryAuthorSeries(ctx context.Context, authorID int64) ([]SeriesWithCount, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT s.id, s.title, count(*) as cnt
		FROM book_authors ba
		JOIN books b ON b.id = ba.book_id AND b.deleted = false
		JOIN series s ON s.id = b.series_id
		WHERE ba.author_id = $1
		GROUP BY s.id, s.title
		ORDER BY cnt DESC, s.normalized_title
	`, authorID)
	if err != nil {
		return nil, fmt.Errorf("query series: %w", err)
	}
	defer rows.Close()
	var out []SeriesWithCount
	for rows.Next() {
		var sc SeriesWithCount
		if err := rows.Scan(&sc.ID, &sc.Title, &sc.Count); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

func (s *Service) queryAuthorBooks(ctx context.Context, authorID int64, limit int) ([]books.ListItem, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT b.id, b.title, b.lib_id, b.lang, b.date_added, ser.title,
		       COALESCE(
		           array_agg(DISTINCT TRIM(CONCAT_WS(' ', a2.last_name, a2.first_name, a2.middle_name))) FILTER (WHERE a2.id IS NOT NULL),
		           ARRAY[]::text[]
		       )
		FROM book_authors ba
		JOIN books b ON b.id = ba.book_id AND b.deleted = false
		LEFT JOIN series ser ON ser.id = b.series_id
		LEFT JOIN book_authors ba2 ON ba2.book_id = b.id
		LEFT JOIN authors a2 ON a2.id = ba2.author_id
		WHERE ba.author_id = $1
		GROUP BY b.id, ser.title
		ORDER BY b.date_added DESC NULLS LAST, b.normalized_title
		LIMIT $2
	`, authorID, limit)
	if err != nil {
		return nil, fmt.Errorf("query author books: %w", err)
	}
	defer rows.Close()
	var out []books.ListItem
	for rows.Next() {
		var (
			b      books.ListItem
			lang   pgtype.Text
			dt     pgtype.Date
			series pgtype.Text
			auth   []string
		)
		if err := rows.Scan(&b.ID, &b.Title, &b.LibID, &lang, &dt, &series, &auth); err != nil {
			return nil, err
		}
		if lang.Valid {
			b.Lang = lang.String
		}
		if dt.Valid {
			y := dt.Time.Year()
			b.Year = &y
		}
		if series.Valid {
			b.Series = series.String
		}
		b.Authors = auth
		out = append(out, b)
	}
	return out, rows.Err()
}

// queryAuthorYearStats — гистограмма по году добавления книг автора.
// Использует date_added: это когда книга попала в нашу коллекцию,
// а не год публикации (его мы пока из fb2 не парсим). Книги без
// date_added отбрасываются — нет смысла рисовать столбик "Unknown".
func (s *Service) queryAuthorYearStats(ctx context.Context, authorID int64) ([]YearCount, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT extract(year from b.date_added)::int AS year, count(*) AS cnt
		FROM book_authors ba
		JOIN books b ON b.id = ba.book_id AND b.deleted = false
		WHERE ba.author_id = $1 AND b.date_added IS NOT NULL
		GROUP BY year
		ORDER BY year
	`, authorID)
	if err != nil {
		return nil, fmt.Errorf("query author year stats: %w", err)
	}
	defer rows.Close()
	var out []YearCount
	for rows.Next() {
		var yc YearCount
		if err := rows.Scan(&yc.Year, &yc.Count); err != nil {
			return nil, err
		}
		out = append(out, yc)
	}
	return out, rows.Err()
}

// queryAuthorReadCount — DISTINCT-count книг автора, которые пользователь
// хотя бы раз скачивал (reads.PK = (user_id, book_id), так что DISTINCT
// тут de facto не нужен, но оставлен для семантической ясности).
func (s *Service) queryAuthorReadCount(ctx context.Context, authorID, userID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM book_authors ba
		JOIN reads r ON r.book_id = ba.book_id
		JOIN books b ON b.id = ba.book_id AND b.deleted = false
		WHERE ba.author_id = $1 AND r.user_id = $2
	`, authorID, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("query author read count: %w", err)
	}
	return n, nil
}

// querySeriesYearStats — то же самое для серии: распределение книг в
// серии по году добавления.
func (s *Service) querySeriesYearStats(ctx context.Context, seriesID int64) ([]YearCount, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT extract(year from b.date_added)::int AS year, count(*) AS cnt
		FROM books b
		WHERE b.series_id = $1 AND b.deleted = false AND b.date_added IS NOT NULL
		GROUP BY year
		ORDER BY year
	`, seriesID)
	if err != nil {
		return nil, fmt.Errorf("query series year stats: %w", err)
	}
	defer rows.Close()
	var out []YearCount
	for rows.Next() {
		var yc YearCount
		if err := rows.Scan(&yc.Year, &yc.Count); err != nil {
			return nil, err
		}
		out = append(out, yc)
	}
	return out, rows.Err()
}

// querySeriesReadCount — сколько книг серии уже скачивал пользователь.
func (s *Service) querySeriesReadCount(ctx context.Context, seriesID, userID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*)
		FROM books b
		JOIN reads r ON r.book_id = b.id
		WHERE b.series_id = $1 AND b.deleted = false AND r.user_id = $2
	`, seriesID, userID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("query series read count: %w", err)
	}
	return n, nil
}

func fullName(last, first, middle string) string {
	parts := make([]string, 0, 3)
	if last != "" {
		parts = append(parts, last)
	}
	if first != "" {
		parts = append(parts, first)
	}
	if middle != "" {
		parts = append(parts, middle)
	}
	return strings.Join(parts, " ")
}
