package histimport

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/skriptes/skriptes/backend/internal/recsys"
	"github.com/skriptes/skriptes/backend/internal/workauthors"
)

// Экспорт своей истории (recsys/04, 4.5): оценки, прочитанное, «читаю», «хочу
// прочитать» и подчёркивания — CSV в формате нашего шаблона (CSVTemplate),
// поэтому выгрузка импортируется обратно и сюда, и на другой инстанс.
// Строка на работу и строка на каждое подчёркивание (импорт сливает их по
// названию и автору); work_id в конце — для этого же инстанса и recseval,
// разбор шаблона лишние колонки пропускает.

// exportHeader — колонки шаблона плюс work_id.
var exportHeader = []string{"title", "author", "rating", "read_at", "shelf", "highlight", "note", "isbn", "work_id"}

type exportRow struct {
	workID  int64
	title   string
	authors string
	isbn    string
	rating  int
	readAt  *time.Time
	reading bool
	toRead  bool
}

// Export пишет историю пользователя CSV (с BOM — чтобы Excel открыл кириллицу).
func Export(ctx context.Context, q recsys.Querier, userID int64, w io.Writer) error {
	rows := map[int64]*exportRow{}
	row := func(id int64) *exportRow {
		r := rows[id]
		if r == nil {
			r = &exportRow{workID: id}
			rows[id] = r
		}
		return r
	}
	if err := each(ctx, q, `SELECT work_id, rating FROM book_ratings WHERE user_id = $1`, []any{userID}, func(r pgx.Rows) error {
		var id int64
		var rating int
		if err := r.Scan(&id, &rating); err != nil {
			return err
		}
		row(id).rating = rating
		return nil
	}); err != nil {
		return err
	}
	// Прочитано — по работе (любое издание); «читаю» — открыта в ридере, не дочитана.
	if err := each(ctx, q, `
		SELECT b.work_id, max(r.completed_at), bool_or(r.completed_at IS NULL AND COALESCE(r.fraction, 0) > 0)
		FROM reads r JOIN books b ON b.id = r.book_id
		WHERE r.user_id = $1 AND b.work_id IS NOT NULL
		  AND (r.completed_at IS NOT NULL OR COALESCE(r.fraction, 0) > 0)
		GROUP BY b.work_id`, []any{userID}, func(r pgx.Rows) error {
		var id int64
		var at *time.Time
		var reading bool
		if err := r.Scan(&id, &at, &reading); err != nil {
			return err
		}
		x := row(id)
		x.readAt, x.reading = at, reading && at == nil
		return nil
	}); err != nil {
		return err
	}
	if err := each(ctx, q, `
		SELECT DISTINCT b.work_id FROM user_collection_books cb
		JOIN user_collections c ON c.id = cb.collection_id JOIN books b ON b.id = cb.book_id
		WHERE c.user_id = $1 AND c.kind = 'to_read' AND b.work_id IS NOT NULL`, []any{userID}, func(r pgx.Rows) error {
		var id int64
		if err := r.Scan(&id); err != nil {
			return err
		}
		row(id).toRead = true
		return nil
	}); err != nil {
		return err
	}
	type highlight struct {
		workID     int64
		text, note string
	}
	hs := map[int64][]highlight{}
	if err := each(ctx, q, `
		SELECT b.work_id, a.excerpt, a.note FROM annotations a JOIN books b ON b.id = a.book_id
		WHERE a.user_id = $1 AND a.kind = 'highlight' AND b.work_id IS NOT NULL
		ORDER BY b.work_id, a.book_id, a.fraction NULLS LAST, a.created_at`, []any{userID}, func(r pgx.Rows) error {
		var h highlight
		if err := r.Scan(&h.workID, &h.text, &h.note); err != nil {
			return err
		}
		row(h.workID)
		hs[h.workID] = append(hs[h.workID], h)
		return nil
	}); err != nil {
		return err
	}

	ids := make([]int64, 0, len(rows))
	for id := range rows {
		ids = append(ids, id)
	}
	// Название, авторы работы (то же правило, что у карточки — workauthors) и
	// первый ISBN изданий.
	if err := each(ctx, q, `
		SELECT w.id, w.title,
		       COALESCE((SELECT string_agg(TRIM(CONCAT_WS(' ', a.first_name, a.last_name)), '; ' ORDER BY c.minpos, a.id)
		                 FROM (`+workauthors.Core("w.id")+`) c JOIN authors a ON a.id = c.author_id), ''),
		       COALESCE((SELECT b.isbn FROM books b WHERE b.work_id = w.id AND NOT b.deleted AND b.isbn IS NOT NULL
		                 ORDER BY b.id LIMIT 1), '')
		FROM works w WHERE w.id = ANY($1)`, []any{ids}, func(r pgx.Rows) error {
		var id int64
		var title, authors, isbn string
		if err := r.Scan(&id, &title, &authors, &isbn); err != nil {
			return err
		}
		x := rows[id]
		x.title, x.authors, x.isbn = title, authors, isbn
		return nil
	}); err != nil {
		return err
	}

	if _, err := io.WriteString(w, "\ufeff"); err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	if err := cw.Write(exportHeader); err != nil {
		return err
	}
	list := make([]*exportRow, 0, len(rows))
	for _, r := range rows {
		if r.title != "" {
			list = append(list, r)
		}
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].authors != list[j].authors {
			return list[i].authors < list[j].authors
		}
		return list[i].title < list[j].title
	})
	for _, r := range list {
		rating, readAt, shelf := "", "", ""
		if r.rating > 0 {
			rating = strconv.Itoa(r.rating)
		}
		switch {
		case r.readAt != nil:
			readAt, shelf = r.readAt.UTC().Format("2006-01-02"), "read"
		case r.reading:
			shelf = "reading"
		case r.toRead:
			shelf = "to_read"
		}
		id := strconv.FormatInt(r.workID, 10)
		if rating != "" || shelf != "" {
			if err := cw.Write([]string{r.title, r.authors, rating, readAt, shelf, "", "", r.isbn, id}); err != nil {
				return err
			}
		}
		for _, h := range hs[r.workID] {
			if err := cw.Write([]string{r.title, r.authors, "", "", "", h.text, h.note, "", id}); err != nil {
				return err
			}
		}
	}
	cw.Flush()
	return cw.Error()
}

func each(ctx context.Context, q recsys.Querier, sql string, args []any, fn func(pgx.Rows) error) error {
	rows, err := q.Query(ctx, sql, args...)
	if err != nil {
		return fmt.Errorf("histimport: экспорт: %w", err)
	}
	return forEach(rows, fn)
}
