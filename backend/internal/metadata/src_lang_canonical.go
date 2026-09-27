package metadata

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/langcode"
)

// CanonicalizeSrcLangs приводит уже записанные books.src_lang к ISO 639-1
// (langcode.Canonical): spa→es, jp→ja, «английски»→en, нераспознанное → NULL
// (#287). Разовый идемпотентный шаг старта — схема не меняется, поэтому не
// миграция (и правило одно с записью в EnsureEditionMeta). Различных значений
// сотни, поэтому UPDATE по каждому, а не построчно. Возвращает работы изменённых
// изданий — для ресинка works-индекса (фасет «Язык оригинала»).
func CanonicalizeSrcLangs(ctx context.Context, pool *pgxpool.Pool) ([]int64, error) {
	rows, err := pool.Query(ctx, `SELECT DISTINCT src_lang FROM books WHERE src_lang IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("list src_lang values: %w", err)
	}
	values, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("list src_lang values: %w", err)
	}
	works := map[int64]struct{}{}
	for _, v := range values {
		c := langcode.Canonical(v)
		if c == v {
			continue
		}
		ids, err := scanInt64s(ctx, pool, `
			UPDATE books SET src_lang = NULLIF($2, '') WHERE src_lang = $1
			RETURNING COALESCE(work_id, 0)`, v, c)
		if err != nil {
			return nil, fmt.Errorf("canonicalize src_lang %q: %w", v, err)
		}
		for _, id := range ids {
			if id != 0 {
				works[id] = struct{}{}
			}
		}
	}
	return keysOf(works), nil
}
