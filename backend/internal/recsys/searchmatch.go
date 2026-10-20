package recsys

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Searcher — поиск работ по тексту среди заданных id (books.Service.SearchWorkIDs):
// возвращает id работ из within, которые Meili считает уверенным совпадением.
type Searcher interface {
	SearchWorkIDs(ctx context.Context, q string, within []int64) ([]int64, error)
}

// Matcher — «появилось то, что вы искали»: после импорта сопоставляет
// ненайденные запросы (hits = 0, не старше RetentionDays) с новыми работами.
type Matcher struct {
	pool     *pgxpool.Pool
	searcher Searcher
	log      *SearchLog
}

func NewMatcher(pool *pgxpool.Pool, searcher Searcher, log *SearchLog) *Matcher {
	return &Matcher{pool: pool, searcher: searcher, log: log}
}

// matchBatch — работ в одном фильтре Meili (id IN [...]).
const matchBatch = 500

// MatchNewWorks — для каждого пользователя и каждого его ненайденного запроса
// (последняя строка по norm) — поиск среди newWorks; попадания — в
// search_matches (повтор — no-op). Возвращает число новых записей.
func (m *Matcher) MatchNewWorks(ctx context.Context, newWorks []int64) (int, error) {
	if m == nil || !m.log.Enabled() || len(newWorks) == 0 {
		return 0, nil
	}
	rows, err := m.pool.Query(ctx, `
		SELECT DISTINCT ON (user_id, norm) user_id, q, at
		FROM search_log
		WHERE hits = 0 AND length(norm) >= $1 AND at > now() - make_interval(days => $2)
		ORDER BY user_id, norm, at DESC`, MissMinLen, m.log.RetentionDays)
	if err != nil {
		return 0, fmt.Errorf("search misses for match: %w", err)
	}
	type miss struct {
		userID int64
		q      string
		at     time.Time
	}
	var misses []miss
	for rows.Next() {
		var ms miss
		if err := rows.Scan(&ms.userID, &ms.q, &ms.at); err != nil {
			rows.Close()
			return 0, err
		}
		misses = append(misses, ms)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	n := 0
	for _, ms := range misses {
		for start := 0; start < len(newWorks); start += matchBatch {
			end := min(start+matchBatch, len(newWorks))
			found, err := m.searcher.SearchWorkIDs(ctx, ms.q, newWorks[start:end])
			if err != nil {
				return n, fmt.Errorf("match %q: %w", ms.q, err)
			}
			for _, wid := range found {
				tag, err := m.pool.Exec(ctx, `
					INSERT INTO search_matches (user_id, work_id, q, searched_at)
					VALUES ($1, $2, $3, $4) ON CONFLICT (user_id, work_id) DO NOTHING`, ms.userID, wid, ms.q, ms.at)
				if err != nil {
					return n, fmt.Errorf("search match insert: %w", err)
				}
				n += int(tag.RowsAffected())
			}
		}
	}
	return n, nil
}
