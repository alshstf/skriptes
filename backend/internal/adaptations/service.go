// Package adaptations отдаёт записи из book_adaptations (фильмы/сериалы
// по конкретной книге) HTTP-слою. Запись в таблицу происходит в
// metadata.Enricher.EnsureAdaptations; здесь — только чтение.
package adaptations

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrBookNotFound — книги нет в БД.
var ErrBookNotFound = errors.New("book not found")

// Adaptation — одна запись о фильме/сериале + флаг "обогащение завершено".
//
// PosterPath — относительное имя файла в /cache/covers (то же
// хранилище что и обложки книг и фото авторов: content-addressable
// по sha256, коллизий нет). Frontend получает абсолютный URL через
// /api/covers/{name}, как и для обложек.
type Adaptation struct {
	ID         int64   `json:"id"`
	Provider   string  `json:"provider"`
	ExtID      string  `json:"ext_id"`
	Title      string  `json:"title"`
	Year       *int    `json:"year,omitempty"`
	Director   string  `json:"director,omitempty"`
	Kind       string  `json:"kind"`
	PosterPath *string `json:"poster_path,omitempty"`
	ExtURL     string  `json:"ext_url,omitempty"`
}

// ListResult — ответ /api/books/{id}/adaptations. enrichment_status
// показывает, что мы успели сделать:
//   - "pending"  → enrichment ещё в работе или не запущен; фронт может
//     поллить через несколько секунд
//   - "done"     → enrichment отработал, items — финальный список (может
//     быть пустым: "ничего по этой книге не сняли")
//
// Решение по enrichment_status делает Service.List на основании
// books.adaptations_fetched_at: NULL → pending, иначе done.
type ListResult struct {
	Items            []Adaptation `json:"items"`
	EnrichmentStatus string       `json:"enrichment_status"` // "pending" | "done"
}

// Service — read-only сервис.
type Service struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// List возвращает все экранизации для книги + статус enrichment'а.
//
// Сортировка: сначала фильмы с известным годом (по году по возрастанию),
// потом без года в конце. Внутри одного года — стабильный порядок по id.
// Логика: пользователю интересно сначала "первые экранизации", это
// удобнее чем по алфавиту или по дате создания записи.
func (s *Service) List(ctx context.Context, bookID int64) (ListResult, error) {
	// Сначала проверим что книга вообще есть и узнаем статус enrichment'а.
	var fetched *string
	err := s.pool.QueryRow(ctx,
		`SELECT adaptations_fetched_at::text FROM books WHERE id = $1`, bookID,
	).Scan(&fetched)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ListResult{}, ErrBookNotFound
		}
		return ListResult{}, fmt.Errorf("query book: %w", err)
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, provider, ext_id, title, year, director, kind, poster_path, ext_url
		FROM book_adaptations
		WHERE book_id = $1
		ORDER BY (year IS NULL), year, id
	`, bookID)
	if err != nil {
		return ListResult{}, fmt.Errorf("query adaptations: %w", err)
	}
	defer rows.Close()

	items := make([]Adaptation, 0)
	for rows.Next() {
		var a Adaptation
		var year *int16
		var director, poster, extURL *string
		if err := rows.Scan(&a.ID, &a.Provider, &a.ExtID, &a.Title, &year, &director, &a.Kind, &poster, &extURL); err != nil {
			return ListResult{}, fmt.Errorf("scan adaptation: %w", err)
		}
		if year != nil {
			y := int(*year)
			a.Year = &y
		}
		if director != nil {
			a.Director = *director
		}
		if poster != nil {
			a.PosterPath = poster
		}
		if extURL != nil {
			a.ExtURL = *extURL
		}
		items = append(items, a)
	}
	if err := rows.Err(); err != nil {
		return ListResult{}, fmt.Errorf("rows: %w", err)
	}

	status := "pending"
	if fetched != nil {
		status = "done"
	}
	return ListResult{Items: items, EnrichmentStatus: status}, nil
}
