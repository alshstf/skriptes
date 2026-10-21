package recsys

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Exclusions — скрытое у пользователя: жанры и языки (admin ∪ личные —
// settings.ContentResolver.Exclusions) и «скрывать сборники». Семантика —
// зеркало catalog.bookExclusionClause (грабля 14): издание скрыто, если его
// язык скрыт или у него хоть один скрытый жанр; сборник — по works.kind.
// Пакет catalog сюда не импортировать (цикл через books → history → recsys),
// поэтому условия продублированы в Hydrate и покрыты тестом.
type Exclusions struct {
	Genres           []string
	Langs            []string
	HideCompilations bool
}

// Hydrate — карточки ленты для кандидатов: представительное издание (живое,
// не скрытое ни на инстансе, ни у пользователя; с обложкой — предпочтительно),
// название работы, авторы издания, цикл, обложка (своя или соседнего
// издания). Работы без подходящего издания выпадают; порядок — порядок
// кандидатов; не больше limit карточек.
func Hydrate(ctx context.Context, q Querier, cands []Candidate, ex Exclusions, limit int) ([]Item, error) {
	if len(cands) == 0 || limit <= 0 {
		return []Item{}, nil
	}
	ids := make([]int64, 0, len(cands))
	for _, c := range cands {
		ids = append(ids, c.WorkID)
	}
	// nil-срез уходит в PG как NULL — «фильтра нет».
	var langs, genres []string
	if len(ex.Langs) > 0 {
		langs = ex.Langs
	}
	if len(ex.Genres) > 0 {
		genres = ex.Genres
	}
	items := make(map[int64]Item, len(ids))
	if err := each(ctx, q, `
		WITH rep AS (
			SELECT w.id AS work_id, w.title AS work_title, w.series_id AS work_series, r.id AS book_id
			FROM works w
			JOIN LATERAL (
				SELECT b.id FROM books b
				WHERE b.work_id = w.id AND NOT b.deleted AND NOT b.instance_hidden
				  AND ($2::text[] IS NULL OR b.lang IS NULL OR NOT (b.lang = ANY($2::text[])))
				  AND ($3::text[] IS NULL OR NOT EXISTS (
				      SELECT 1 FROM book_genres bgx JOIN genres gx ON gx.id = bgx.genre_id
				      WHERE bgx.book_id = b.id AND gx.fb2_code = ANY($3::text[])))
				ORDER BY (b.cover_path IS NOT NULL AND b.cover_path <> '') DESC, b.id
				LIMIT 1
			) r ON true
			WHERE w.id = ANY($1) AND NOT w.instance_hidden
			  AND (NOT $4::bool OR COALESCE(w.kind, '') = '')
		)
		SELECT rep.work_id, b.id, rep.work_title, b.lib_id, ser.title,
		       COALESCE(b.cover_path, (
		           SELECT bb.cover_path FROM books bb
		           WHERE bb.work_id = rep.work_id AND bb.deleted = false
		             AND bb.cover_path IS NOT NULL AND bb.cover_path <> ''
		           ORDER BY bb.id LIMIT 1
		       ), ''),
		       COALESCE(
		           array_agg(DISTINCT TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name))) FILTER (WHERE a.id IS NOT NULL),
		           ARRAY[]::text[]
		       )
		FROM rep
		JOIN books b ON b.id = rep.book_id
		-- Подпись — цикл работы (канонический), иначе цикл издания; только циклы (#468).
		LEFT JOIN series ser ON ser.id = COALESCE(rep.work_series, b.series_id) AND ser.kind IS NULL
		LEFT JOIN book_authors ba ON ba.book_id = b.id
		LEFT JOIN authors a ON a.id = ba.author_id
		GROUP BY rep.work_id, b.id, rep.work_title, b.lib_id, ser.title, b.cover_path`,
		[]any{ids, langs, genres, ex.HideCompilations}, func(rows pgx.Rows) error {
			var (
				it     Item
				series *string
			)
			if err := rows.Scan(&it.WorkID, &it.ID, &it.Title, &it.LibID, &series, &it.CoverPath, &it.Authors); err != nil {
				return err
			}
			if series != nil {
				it.Series = *series
			}
			items[it.WorkID] = it
			return nil
		}); err != nil {
		return nil, fmt.Errorf("recsys: hydrate: %w", err)
	}
	out := make([]Item, 0, limit)
	for _, c := range cands {
		it, ok := items[c.WorkID]
		if !ok {
			continue
		}
		it.Reasons = []Reason{c.Reason}
		out = append(out, it)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}
