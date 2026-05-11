package books

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meilisearch/meilisearch-go"
)

// ErrNotFound возвращается из Get когда книги с таким id нет (или она удалена).
var ErrNotFound = errors.New("book not found")

// Service — read-side сервис каталога книг.
type Service struct {
	pool  *pgxpool.Pool
	meili meilisearch.ServiceManager
}

// New собирает Service. meili может быть nil — тогда List вернёт пустой
// результат вместо ошибки (полезно для unit-тестов handlers без Meili).
func New(pool *pgxpool.Pool, meili meilisearch.ServiceManager) *Service {
	return &Service{pool: pool, meili: meili}
}

// List — поиск книг через Meilisearch.
// Если params.Query пустая — возвращает первые limit/offset (Meili
// сортирует по дефолтным правилам).
func (s *Service) List(ctx context.Context, params ListParams) (ListResponse, error) {
	if s.meili == nil {
		return ListResponse{Items: []ListItem{}, Limit: params.Limit, Offset: params.Offset}, nil
	}
	limit := clampInt(params.Limit, 1, 100, 20)
	offset := clampInt(params.Offset, 0, 10_000, 0)

	req := &meilisearch.SearchRequest{
		Limit:  int64(limit),
		Offset: int64(offset),
	}
	res, err := s.meili.Index("books").SearchWithContext(ctx, params.Query, req)
	if err != nil {
		return ListResponse{}, fmt.Errorf("meili search: %w", err)
	}

	// JSON-теги ListItem совпадают со структурой docs из importer.bookDoc,
	// поэтому декодируем прямо в неё. Битые hits пропускаем (без падения
	// всего запроса) — лучше отдать частичный результат, чем 502.
	items := make([]ListItem, 0, len(res.Hits))
	for _, h := range res.Hits {
		var item ListItem
		if err := h.DecodeInto(&item); err != nil {
			continue
		}
		items = append(items, item)
	}

	total := res.EstimatedTotalHits
	if total == 0 && res.TotalHits > 0 {
		total = res.TotalHits
	}
	return ListResponse{
		Items:       items,
		Total:       total,
		Limit:       limit,
		Offset:      offset,
		Query:       params.Query,
		ProcessTime: res.ProcessingTimeMs,
	}, nil
}

// Suggest — компактный typeahead по индексу books.
// Возвращает срезанный набор ListItem (без total/pagination), в порядке,
// который Meili даёт по умолчанию (с учётом ranking rules + popularity).
// Если meili не сконфигурирован — пустой срез без ошибки (для unit-тестов).
func (s *Service) Suggest(ctx context.Context, query string, limit int) ([]ListItem, error) {
	if s.meili == nil || strings.TrimSpace(query) == "" {
		return []ListItem{}, nil
	}
	limit = clampInt(limit, 1, 20, 5)
	res, err := s.meili.Index("books").SearchWithContext(ctx, query, &meilisearch.SearchRequest{
		Limit: int64(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("meili search: %w", err)
	}
	out := make([]ListItem, 0, len(res.Hits))
	for _, h := range res.Hits {
		var item ListItem
		if err := h.DecodeInto(&item); err != nil {
			continue
		}
		out = append(out, item)
	}
	return out, nil
}

// Get возвращает детальную карточку книги по id.
// Удалённые (deleted=true) тоже возвращаются — frontend сам решит как их
// показать. Это симметрично с импортёром, который их сохраняет в PG.
func (s *Service) Get(ctx context.Context, id int64) (Book, error) {
	var b Book
	var (
		dateAdded   pgtype.Date
		rating      pgtype.Int2
		annotation  pgtype.Text
		coverPath   pgtype.Text
		serNo       pgtype.Int4
		seriesID    pgtype.Int8
		seriesTitle pgtype.Text
		archive     string
		lang        pgtype.Text
	)
	err := s.pool.QueryRow(ctx, `
		SELECT
			b.id, b.lib_id, b.title,
			b.lang, b.date_added, b.rating, b.annotation, b.cover_path,
			b.ser_no, b.series_id, s.title,
			b.file_name, b.ext, b.size_bytes, b.deleted,
			a.filename
		FROM books b
		LEFT JOIN series s   ON s.id = b.series_id
		JOIN archives a      ON a.id = b.archive_id
		WHERE b.id = $1
	`, id).Scan(
		&b.ID, &b.LibID, &b.Title,
		&lang, &dateAdded, &rating, &annotation, &coverPath,
		&serNo, &seriesID, &seriesTitle,
		&b.FileName, &b.Ext, &b.SizeBytes, &b.Deleted,
		&archive,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Book{}, ErrNotFound
		}
		return Book{}, fmt.Errorf("query book: %w", err)
	}

	if lang.Valid {
		b.Lang = lang.String
	}
	if dateAdded.Valid {
		t := dateAdded.Time
		b.DateAdded = &t
	}
	if rating.Valid {
		v := int(rating.Int16)
		b.Rating = &v
	}
	if annotation.Valid {
		b.Annotation = annotation.String
	}
	if coverPath.Valid {
		b.CoverPath = coverPath.String
	}
	if serNo.Valid {
		v := int(serNo.Int32)
		b.SerNo = &v
	}
	if seriesID.Valid && seriesTitle.Valid {
		b.Series = &SeriesRef{ID: seriesID.Int64, Title: seriesTitle.String}
	}
	b.Archive = archive

	authors, err := s.queryAuthors(ctx, b.ID)
	if err != nil {
		return Book{}, err
	}
	b.Authors = authors

	genres, err := s.queryGenres(ctx, b.ID)
	if err != nil {
		return Book{}, err
	}
	b.Genres = genres

	return b, nil
}

func (s *Service) queryAuthors(ctx context.Context, bookID int64) ([]AuthorRef, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.last_name, a.first_name, a.middle_name
		FROM book_authors ba
		JOIN authors a ON a.id = ba.author_id
		WHERE ba.book_id = $1
		ORDER BY ba.position
	`, bookID)
	if err != nil {
		return nil, fmt.Errorf("query authors: %w", err)
	}
	defer rows.Close()
	var out []AuthorRef
	for rows.Next() {
		var a AuthorRef
		if err := rows.Scan(&a.ID, &a.LastName, &a.FirstName, &a.MiddleName); err != nil {
			return nil, err
		}
		a.FullName = fullName(a)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Service) queryGenres(ctx context.Context, bookID int64) ([]GenreRef, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT g.id, g.fb2_code, COALESCE(g.name_ru,''), COALESCE(g.name_en,'')
		FROM book_genres bg
		JOIN genres g ON g.id = bg.genre_id
		WHERE bg.book_id = $1
		ORDER BY g.fb2_code
	`, bookID)
	if err != nil {
		return nil, fmt.Errorf("query genres: %w", err)
	}
	defer rows.Close()
	var out []GenreRef
	for rows.Next() {
		var g GenreRef
		if err := rows.Scan(&g.ID, &g.Code, &g.NameRu, &g.NameEn); err != nil {
			return nil, err
		}
		g.Display = pickGenreDisplay(g)
		out = append(out, g)
	}
	return out, rows.Err()
}

// fullName собирает "Lastname Firstname Middlename" пропуская пустые куски.
func fullName(a AuthorRef) string {
	parts := make([]string, 0, 3)
	if a.LastName != "" {
		parts = append(parts, a.LastName)
	}
	if a.FirstName != "" {
		parts = append(parts, a.FirstName)
	}
	if a.MiddleName != "" {
		parts = append(parts, a.MiddleName)
	}
	return strings.Join(parts, " ")
}

func pickGenreDisplay(g GenreRef) string {
	switch {
	case g.NameRu != "" && g.NameRu != g.Code:
		return g.NameRu
	case g.NameEn != "" && g.NameEn != g.Code:
		return g.NameEn
	default:
		return g.Code
	}
}

func clampInt(v, lo, hi, def int) int {
	if v == 0 {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
