package metadata

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CleanStubSrcTitles обнуляет src_title-заглушки («(no data for original
// title)», «???») в базе: группировка считала их оригиналом и склеивала по ним
// разные тексты, внешний поиск искал по ним книги (#279). Возвращает число строк.
func CleanStubSrcTitles(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	rows, err := pool.Query(ctx, `SELECT DISTINCT src_title FROM books WHERE src_title IS NOT NULL`)
	if err != nil {
		return 0, fmt.Errorf("list src titles: %w", err)
	}
	values, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, fmt.Errorf("list src titles: %w", err)
	}
	var stubs []string
	for _, v := range values {
		if isStubSrcTitle(v) {
			stubs = append(stubs, v)
		}
	}
	if len(stubs) == 0 {
		return 0, nil
	}
	tag, err := pool.Exec(ctx, `UPDATE books SET src_title = NULL WHERE src_title = ANY($1)`, stubs)
	if err != nil {
		return 0, fmt.Errorf("clean stub src titles: %w", err)
	}
	return tag.RowsAffected(), nil
}

// TitleConflictWorks — работы, которые Tier-2 склеил бы уже не стал: в одном
// языке у изданий разные названия без src-свидетельства или разные номера
// тома/книги/части (#279; прод 2026-09 — 2,6 тыс. работ: «Танька» Бунина с 37
// рассказами). Правило то же, что у гейтов группировки (sameLangTitleConflict,
// tier2BucketConflicts), поэтому считаем в Go, а не в SQL.
func TitleConflictWorks(ctx context.Context, pool *pgxpool.Pool) ([]int64, error) {
	rows, err := pool.Query(ctx, `
		SELECT b.work_id, b.title, b.normalized_title::text, COALESCE(b.lang, ''),
		       COALESCE(b.src_title, ''), COALESCE(b.ser_no, 0)
		FROM books b
		WHERE b.deleted = false AND b.work_id IN (
			SELECT work_id FROM books WHERE deleted = false AND work_id IS NOT NULL
			GROUP BY work_id HAVING count(*) >= 2)
		ORDER BY b.work_id`)
	if err != nil {
		return nil, fmt.Errorf("load multi-edition works: %w", err)
	}
	defer rows.Close()
	var out []int64
	var cur int64
	var books []groupBook
	flush := func() {
		if len(books) < 2 {
			return
		}
		idxs := make([]int, len(books))
		for i := range idxs {
			idxs[i] = i
		}
		if sameLangTitleConflict(books, idxs) || volumeConflict(books) {
			out = append(out, cur)
		}
	}
	for rows.Next() {
		var wid int64
		var b groupBook
		var src string
		if err := rows.Scan(&wid, &b.title, &b.normTitle, &b.lang, &src, &b.serNo); err != nil {
			return nil, err
		}
		if wid != cur {
			flush()
			cur, books = wid, books[:0]
		}
		if !isStubSrcTitle(src) {
			b.srcTitleNorm = normalizePersonKey(src)
		}
		books = append(books, b)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	flush()
	return out, nil
}

// volumeConflict — у изданий работы разные номера тома/книги/части в названии.
func volumeConflict(books []groupBook) bool {
	vols := map[string]struct{}{}
	for _, b := range books {
		if v := volumeNumber(b.title); v != "" {
			vols[v] = struct{}{}
		}
	}
	return len(vols) > 1
}
