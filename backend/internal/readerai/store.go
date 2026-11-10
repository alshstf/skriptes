package readerai

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PGSummaries — кэш пересказов в таблице book_summaries.
type PGSummaries struct{ Pool *pgxpool.Pool }

// Get — готовые пересказы кусков chunks этой версии текста.
func (s PGSummaries) Get(ctx context.Context, bookID int64, version string, chunks []int) (map[int]string, error) {
	want := make([]int32, len(chunks))
	for i, c := range chunks {
		want[i] = int32(c) // #nosec G115 — номер куска книги, тысячи
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT chunk, summary FROM book_summaries
		WHERE book_id = $1 AND text_version = $2 AND chunk_runes = $3 AND chunk = ANY($4)`,
		bookID, version, chunkRunes, want)
	if err != nil {
		return nil, fmt.Errorf("read book summaries: %w", err)
	}
	defer rows.Close()
	out := map[int]string{}
	for rows.Next() {
		var c int
		var text string
		if err := rows.Scan(&c, &text); err != nil {
			return nil, err
		}
		out[c] = text
	}
	return out, rows.Err()
}

// Put сохраняет пересказ куска; уже есть (другой читатель успел) — оставляет прежний.
func (s PGSummaries) Put(ctx context.Context, bookID int64, version string, chunk int, text string) error {
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO book_summaries (book_id, text_version, chunk_runes, chunk, summary)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING`,
		bookID, version, chunkRunes, chunk, text)
	if err != nil {
		return fmt.Errorf("save book summary: %w", err)
	}
	return nil
}
