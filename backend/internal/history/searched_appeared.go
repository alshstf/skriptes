package history

import (
	"context"
	"fmt"
	"time"

	"github.com/skriptes/skriptes/backend/internal/recsys"
)

// SearchedAppeared — секция «Появилось то, что вы искали» (план recsys/01, 1.4):
// работы из search_matches (recsys.Matcher после импорта), пока не скрыты из
// ленты и не открыты после совпадения. Представитель — первое живое издание
// работы (обложка, ссылка), как у других секций.
func (s *Service) SearchedAppeared(ctx context.Context, userID int64, limit int) ([]recsys.Item, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		WITH m AS (
			SELECT m.work_id, m.q, m.searched_at, m.matched_at
			FROM search_matches m
			JOIN works w ON w.id = m.work_id AND NOT w.instance_hidden
			WHERE m.user_id = $1
			  AND NOT EXISTS (SELECT 1 FROM feed_dismissals fd WHERE fd.user_id = $1 AND fd.work_id = m.work_id)
			  AND NOT EXISTS (SELECT 1 FROM views v JOIN books vb ON vb.id = v.book_id
			                  WHERE v.user_id = $1 AND vb.work_id = m.work_id AND v.viewed_at > m.matched_at)
			ORDER BY m.matched_at DESC
			LIMIT $2
		)
		SELECT m.work_id, m.q, m.searched_at, b.id, b.title, b.lib_id, ser.title,
		       COALESCE(b.cover_path, (
		           SELECT bb.cover_path FROM books bb
		           WHERE bb.work_id = m.work_id AND bb.deleted = false
		             AND bb.cover_path IS NOT NULL AND bb.cover_path <> ''
		           ORDER BY bb.id LIMIT 1
		       ), ''),
		       COALESCE(
		           array_agg(DISTINCT TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name))) FILTER (WHERE a.id IS NOT NULL),
		           ARRAY[]::text[]
		       )
		FROM m
		JOIN LATERAL (SELECT bb.id FROM books bb
		              WHERE bb.work_id = m.work_id AND bb.deleted = false AND NOT bb.instance_hidden
		              ORDER BY bb.id LIMIT 1) rep ON true
		JOIN books b ON b.id = rep.id
		LEFT JOIN series ser ON ser.id = b.series_id AND ser.kind IS NULL
		LEFT JOIN book_authors ba ON ba.book_id = b.id
		LEFT JOIN authors a ON a.id = ba.author_id
		GROUP BY m.work_id, m.q, m.searched_at, m.matched_at, b.id, b.title, b.lib_id, ser.title, b.cover_path
		ORDER BY m.matched_at DESC
	`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("query searched appeared: %w", err)
	}
	defer rows.Close()
	out := make([]recsys.Item, 0)
	for rows.Next() {
		var (
			it         recsys.Item
			q          string
			searchedAt time.Time
			series     *string
		)
		if err := rows.Scan(&it.WorkID, &q, &searchedAt, &it.ID, &it.Title, &it.LibID, &series, &it.CoverPath, &it.Authors); err != nil {
			return nil, err
		}
		if series != nil {
			it.Series = *series
		}
		at := searchedAt
		it.Reasons = []recsys.Reason{{Kind: recsys.ReasonSearchedAppeared, Query: q, At: &at}}
		out = append(out, it)
	}
	return out, rows.Err()
}
