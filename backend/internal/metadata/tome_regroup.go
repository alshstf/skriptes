package metadata

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ResetTomeEditionsForRegroup — разовый догон #537: «Том N. <роман>» до 1.71.0
// группировка не склеивала с романом, а эти издания уже помечены просмотренными
// и в Tier-1 сами не вернутся. Сбрасывает work_scanned_at у томов, которые
// склеит новое правило (tomeTitle: одно название без жанрового слова, у того же
// автора есть издание того же языка с ровно этим названием в другой работе), —
// группировка (на проде включена) перечитает их авторов. На проде 2026-10-27 —
// 181 издание. Возвращает число сброшенных.
func ResetTomeEditionsForRegroup(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	tag, err := pool.Exec(ctx, `
		WITH t AS (
		    SELECT b.id, b.work_id, b.lang,
		           btrim(regexp_replace(b.normalized_title, '^\s*том\s+\d+\s*[.:]\s*', '')) AS rest
		    FROM books b
		    WHERE NOT b.deleted AND b.work_scanned_at IS NOT NULL
		      AND b.normalized_title ~ '^\s*том\s+\d+\s*[.:]'
		)
		UPDATE books b SET work_scanned_at = NULL
		FROM t
		WHERE b.id = t.id AND t.rest <> '' AND t.rest !~ '[.;]'
		  AND t.rest !~ '\m(рассказы|повести|стихотворения|стихи|поэмы|письма|статьи|произведения|пьесы|драмы|драматические|очерки|проза|сочинения|публицистика|дневники?|записные книжки|критика|переводы|фельетоны|сказки|эссе|речи|заметки|воспоминания)\M'
		  AND EXISTS (
		      SELECT 1 FROM book_authors ta
		      JOIN book_authors oa ON oa.author_id = ta.author_id
		      JOIN books o ON o.id = oa.book_id
		      WHERE ta.book_id = t.id AND NOT o.deleted AND o.lang = t.lang
		        AND o.normalized_title = t.rest AND o.work_id IS DISTINCT FROM t.work_id)`)
	if err != nil {
		return 0, fmt.Errorf("tome regroup: %w", err)
	}
	return tag.RowsAffected(), nil
}
