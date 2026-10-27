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
// ненайденные запросы (hits = 0, не старше RetentionDays) и список желаний
// (ненайденное из импорта истории, wishlist, #597) с новыми работами.
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
// (последняя строка по norm) и желания — поиск среди newWorks; попадания — в
// search_matches (повтор — no-op), исполненное желание вычёркивается. Лог поиска
// выключен — желания всё равно сопоставляются: у них нет срока. Возвращает число
// новых записей.
func (m *Matcher) MatchNewWorks(ctx context.Context, newWorks []int64) (int, error) {
	if m == nil || len(newWorks) == 0 {
		return 0, nil
	}
	logOn := m.log.Enabled()
	days := 0
	if logOn {
		days = m.log.RetentionDays
	}
	rows, err := m.pool.Query(ctx, `
		SELECT DISTINCT ON (user_id, norm) user_id, norm, q, at, wish FROM (
			SELECT user_id, norm, q, at, false AS wish FROM search_log
			WHERE $3 AND hits = 0 AND length(norm) >= $1 AND at > now() - make_interval(days => $2)
			UNION ALL
			SELECT user_id, norm, q, added_at, true FROM wishlist
		) m
		ORDER BY user_id, norm, wish DESC, at DESC`, MissMinLen, days, logOn)
	if err != nil {
		return 0, fmt.Errorf("search misses for match: %w", err)
	}
	type miss struct {
		userID int64
		norm   string
		q      string
		at     time.Time
		wish   bool
	}
	var misses []miss
	for rows.Next() {
		var ms miss
		if err := rows.Scan(&ms.userID, &ms.norm, &ms.q, &ms.at, &ms.wish); err != nil {
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
		matched := false
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
				matched = true
			}
		}
		if ms.wish && matched {
			if _, err := m.pool.Exec(ctx, `DELETE FROM wishlist WHERE user_id = $1 AND norm = $2`, ms.userID, ms.norm); err != nil {
				return n, fmt.Errorf("wishlist fulfilled: %w", err)
			}
		}
	}
	return n, nil
}
