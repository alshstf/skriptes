// Package authorevents — read-сервис событий биографии автора (био-таймлайн,
// план cryptic-roaming-turing). Зеркало adaptations/service.go: чтение из
// author_events + enrichment_status по authors.events_fetched_at; запись
// делает metadata.Enricher.EnsureAuthorEvents.
package authorevents

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrAuthorNotFound = errors.New("author not found")

// Event — событие таймлайна для API.
type Event struct {
	ID            int64  `json:"id"`
	Source        string `json:"source"`
	Type          string `json:"type"`
	YearFrom      int    `json:"year_from"`
	YearTo        *int   `json:"year_to,omitempty"`
	DateFrom      string `json:"date_from,omitempty"` // ISO-дата при precision month/day
	DatePrecision string `json:"date_precision"`
	Title         string `json:"title"`
	Quote         string `json:"quote,omitempty"`
	Place         string `json:"place,omitempty"`
	URL           string `json:"url,omitempty"`
	Weight        int    `json:"weight"`
	// Hidden отдаётся ТОЛЬКО админу (для секции «Скрытые» и отмены скрытия).
	Hidden *bool `json:"hidden,omitempty"`
}

// Response — события + статус обогащения + критерий показа.
type Response struct {
	Items []Event `json:"items"`
	// EnrichmentStatus: pending (ещё не тянули — фронт поллит) | done.
	EnrichmentStatus string `json:"enrichment_status"`
	// Eligible — «таймлайн не скучен»: ≥ minNontrivialEvents нетривиальных
	// событий И ≥ minBooksWithYear книг с годом написания. false → фронт
	// секцию не рендерит вовсе (сырьё в items всё равно отдаём — админу
	// полезно видеть, почему скучно).
	Eligible bool `json:"eligible"`
}

// Пороги критерия «показывать таймлайн» (валидированы замерами 2026-07:
// средний автор набирает 1–3 нетривиальных события Wikidata-скелета → скрыт;
// классики 13–19 → показаны; «нетривиальное» = weight ≥ 2, т.е. birth/death/
// residence/мелкие награды порог не набивают). Книг с written_year ≥ 2 —
// иначе правой колонке таймлайна нечего показывать (урок v1).
const (
	minNontrivialEvents = 5
	minBooksWithYear    = 2
	nontrivialWeight    = 2
)

type Service struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Service { return &Service{pool: pool} }

// List — события автора (год ↑, вес ↓). isAdmin: скрытые строки включаются
// (с флагом hidden) — для секции «Скрытые (N)» и отмены скрытия.
func (s *Service) List(ctx context.Context, authorID int64, isAdmin bool) (Response, error) {
	var fetchedAt *time.Time
	err := s.pool.QueryRow(ctx,
		`SELECT events_fetched_at FROM authors WHERE id = $1`, authorID).Scan(&fetchedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Response{}, ErrAuthorNotFound
	}
	if err != nil {
		return Response{}, fmt.Errorf("query author: %w", err)
	}

	resp := Response{Items: []Event{}, EnrichmentStatus: "pending"}
	if fetchedAt != nil {
		resp.EnrichmentStatus = "done"
	}

	hiddenCond := "AND hidden = false"
	if isAdmin {
		hiddenCond = ""
	}
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
		SELECT id, source, event_type, year_from, year_to, date_from,
		       date_precision, title, COALESCE(quote,''), COALESCE(place,''),
		       COALESCE(url,''), weight, hidden
		FROM author_events
		WHERE author_id = $1 %s
		ORDER BY year_from, weight DESC, id`, hiddenCond), authorID)
	if err != nil {
		return Response{}, fmt.Errorf("query events: %w", err)
	}
	defer rows.Close()

	nontrivial := 0
	for rows.Next() {
		var (
			ev     Event
			dateAt *time.Time
			hidden bool
		)
		if err := rows.Scan(&ev.ID, &ev.Source, &ev.Type, &ev.YearFrom, &ev.YearTo,
			&dateAt, &ev.DatePrecision, &ev.Title, &ev.Quote, &ev.Place,
			&ev.URL, &ev.Weight, &hidden); err != nil {
			return Response{}, fmt.Errorf("scan event: %w", err)
		}
		if dateAt != nil {
			ev.DateFrom = dateAt.Format("2006-01-02")
		}
		if isAdmin {
			h := hidden
			ev.Hidden = &h
		}
		if !hidden && ev.Weight >= nontrivialWeight {
			nontrivial++
		}
		resp.Items = append(resp.Items, ev)
	}
	if err := rows.Err(); err != nil {
		return Response{}, err
	}

	if nontrivial >= minNontrivialEvents {
		var booksWithYear int
		if err := s.pool.QueryRow(ctx, `
			SELECT count(DISTINCT COALESCE(b.work_id, -b.id))
			FROM book_authors ba
			JOIN books b ON b.id = ba.book_id AND b.deleted = false
			WHERE ba.author_id = $1 AND b.written_year IS NOT NULL`, authorID,
		).Scan(&booksWithYear); err != nil {
			return Response{}, fmt.Errorf("count dated books: %w", err)
		}
		resp.Eligible = booksWithYear >= minBooksWithYear
	}
	return resp, nil
}
