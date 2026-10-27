package catalog

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Современники автора на карточке (#505): известные авторы библиотеки, чья
// взрослая жизнь пересекалась с его жизнью. Только арифметика по годам жизни
// (authors.born_year / died_year — их пишет био-пайплайн из Wikidata), без
// интерпретаций. «Взрослая» — с contemporaryAdultAge лет: иначе Достоевский
// (1821) — «современник» Пушкина (1837), хотя ему было 16, а Лермонтов (1814)
// с восемью общими взрослыми годами — да.
const (
	contemporaryAdultAge     = 15
	contemporaryMinOverlap   = 5
	contemporaryAliveSince   = 1900 // без года смерти жив только родившийся с этого года; раньше — год неизвестен
	contemporariesMaxResults = 8
)

// Contemporary — современник: ссылка на карточку и годы жизни (для подсказки).
type Contemporary struct {
	ID        int64  `json:"id"`
	LastName  string `json:"last_name"`
	FirstName string `json:"first_name,omitempty"`
	BornYear  int    `json:"born_year"`
	DiedYear  *int   `json:"died_year,omitempty"`
}

// Contemporaries — до contemporariesMaxResults самых известных (renown)
// современников автора id. Авторы без года рождения, служебные, скрытые на
// инстансе и без известности не берутся; у самого автора нет годов жизни —
// пусто.
func (s *Service) Contemporaries(ctx context.Context, id int64) ([]Contemporary, error) {
	rows, err := s.pool.Query(ctx, `
		WITH me AS (
		    SELECT born_year AS b,
		           COALESCE(died_year, CASE WHEN born_year >= $2 THEN extract(year FROM now())::int END) AS d
		    FROM authors WHERE id = $1 AND born_year IS NOT NULL
		)
		SELECT a.id, a.last_name, COALESCE(a.first_name, ''), a.born_year, a.died_year
		FROM authors a, me
		WHERE me.d IS NOT NULL AND a.id <> $1 AND a.born_year IS NOT NULL
		  AND a.renown > 0 AND NOT a.is_service AND NOT a.instance_hidden
		  -- Год смерти неизвестен у родившегося до 1900 — не «жив по сей день»
		  -- (LEAST пропустил бы NULL и взял год смерти автора).
		  AND (a.died_year IS NOT NULL OR a.born_year >= $2)
		  AND LEAST(me.d, COALESCE(a.died_year, extract(year FROM now())::int))
		      - GREATEST(me.b, a.born_year) - $3 >= $4
		ORDER BY a.renown DESC, a.id
		LIMIT $5`, id, contemporaryAliveSince, contemporaryAdultAge, contemporaryMinOverlap, contemporariesMaxResults)
	if err != nil {
		return nil, fmt.Errorf("contemporaries: %w", err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Contemporary, error) {
		var c Contemporary
		return c, r.Scan(&c.ID, &c.LastName, &c.FirstName, &c.BornYear, &c.DiedYear)
	})
	if err != nil {
		return nil, fmt.Errorf("contemporaries: %w", err)
	}
	return out, nil
}
