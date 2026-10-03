package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
)

// Слияние авторов (#308): один человек под двумя записями — «Лукьяненко Сергей»
// (8 работ) и «Лукьяненко Сергей Васильевич» (314), «Конан Дойл Артур» и
// «Дойль Артур Конан». Автоматически не различить (бывают и разные люди с
// одинаковыми ФИ), поэтому только вручную, администратором.
//
// Работы источника переходят к цели правкой авторов работы (setWorkAuthors:
// ledger metadata_overrides, переживает ре-импорт, откатывается по работе на
// карточке книги); соавторы на месте. Подписки на источник переносятся на цель,
// био и фото источника берутся, если у цели их нет. Слияние помнит author_merges
// (миграция 0047): новые книги источника из следующих INPX тоже переезжают —
// ReapplyAuthorMerges после импорта.

var (
	// ErrMergeSameAuthor — источник и цель совпадают или цель уже слита в источник.
	ErrMergeSameAuthor = errors.New("merge authors: same author")
	// ErrMergeAuthorNotFound — нет такого автора.
	ErrMergeAuthorNotFound = errors.New("merge authors: author not found")
)

// MergeAuthors переносит работы, подписки и (при пустых у цели) био/фото автора
// sourceID к targetID и запоминает слияние. Возвращает затронутые работы.
// setBy — id администратора (0 — системный перенос после импорта).
func (c *OverrideController) MergeAuthors(ctx context.Context, sourceID, targetID, setBy int64) ([]int64, error) {
	if sourceID == targetID {
		return nil, ErrMergeSameAuthor
	}
	var n int
	if err := c.pool.QueryRow(ctx, `SELECT count(*) FROM authors WHERE id = ANY($1)`,
		[]int64{sourceID, targetID}).Scan(&n); err != nil {
		return nil, err
	}
	if n != 2 {
		return nil, ErrMergeAuthorNotFound
	}
	// Цель сама не должна быть слита в источник (цикл).
	var cycle bool
	if err := c.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM author_merges WHERE source_id = $1 AND target_id = $2)`,
		targetID, sourceID).Scan(&cycle); err != nil {
		return nil, err
	}
	if cycle {
		return nil, ErrMergeSameAuthor
	}
	works, err := c.moveAuthorWorks(ctx, sourceID, targetID, setBy)
	if err != nil {
		return works, err
	}

	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return works, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO favorite_authors (user_id, author_id, added_at)
		SELECT user_id, $2, added_at FROM favorite_authors WHERE author_id = $1
		ON CONFLICT DO NOTHING`, sourceID, targetID); err != nil {
		return works, fmt.Errorf("move subscriptions: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM favorite_authors WHERE author_id = $1`, sourceID); err != nil {
		return works, fmt.Errorf("drop source subscriptions: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE authors t SET bio = s.bio, photo_path = s.photo_path, metadata_fetched_at = s.metadata_fetched_at
		FROM authors s
		WHERE t.id = $2 AND s.id = $1
		  AND COALESCE(t.bio, '') = '' AND COALESCE(t.photo_path, '') = ''
		  AND (COALESCE(s.bio, '') <> '' OR COALESCE(s.photo_path, '') <> '')`, sourceID, targetID); err != nil {
		return works, fmt.Errorf("take source bio: %w", err)
	}
	// Слитые в источник раньше теперь сливаются в цель — без цепочек.
	if _, err := tx.Exec(ctx, `UPDATE author_merges SET target_id = $2 WHERE target_id = $1`, sourceID, targetID); err != nil {
		return works, fmt.Errorf("repoint merges: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO author_merges (source_id, target_id, merged_by) VALUES ($1, $2, NULLIF($3, 0)::bigint)
		ON CONFLICT (source_id) DO UPDATE SET target_id = EXCLUDED.target_id, merged_by = EXCLUDED.merged_by, merged_at = now()`,
		sourceID, targetID, setBy); err != nil {
		return works, fmt.Errorf("remember merge: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return works, err
	}
	c.recomputeRenown(works)
	c.logger.Info("authors merged", "source_id", sourceID, "target_id", targetID, "works", len(works))
	return works, nil
}

// ReapplyAuthorMerges — после импорта: новые книги слитых авторов (импорт
// заводит их на прежнюю запись) переезжают к цели. Работы, уже переведённые
// правкой авторов, импорт не трогает — их держит ReapplyAfterImport.
func (c *OverrideController) ReapplyAuthorMerges(ctx context.Context) (int, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT m.source_id, m.target_id FROM author_merges m
		WHERE EXISTS (SELECT 1 FROM book_authors ba JOIN books b ON b.id = ba.book_id AND b.deleted = false
		              WHERE ba.author_id = m.source_id)`)
	if err != nil {
		return 0, err
	}
	type pair struct{ source, target int64 }
	pairs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (pair, error) {
		var p pair
		return p, r.Scan(&p.source, &p.target)
	})
	if err != nil {
		return 0, err
	}
	moved := 0
	for _, p := range pairs {
		works, err := c.moveAuthorWorks(ctx, p.source, p.target, 0)
		if err != nil {
			return moved, fmt.Errorf("reapply merge %d → %d: %w", p.source, p.target, err)
		}
		c.recomputeRenown(works)
		moved += len(works)
	}
	return moved, nil
}

// moveAuthorWorks — все работы с изданиями автора sourceID: источник в списке
// авторов работы заменяется целью (порядок сохраняется, повтор убирается).
func (c *OverrideController) moveAuthorWorks(ctx context.Context, sourceID, targetID, setBy int64) ([]int64, error) {
	rows, err := c.pool.Query(ctx, `
		SELECT DISTINCT b.work_id FROM books b
		JOIN book_authors ba ON ba.book_id = b.id AND ba.author_id = $1
		WHERE b.deleted = false AND b.work_id IS NOT NULL
		ORDER BY 1`, sourceID)
	if err != nil {
		return nil, err
	}
	works, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, err
	}
	for _, w := range works {
		ids, err := workAuthorIDs(ctx, c.pool, w)
		if err != nil {
			return works, err
		}
		for i, id := range ids {
			if id == sourceID {
				ids[i] = targetID
			}
		}
		value, _ := json.Marshal(map[string][]int64{"author_ids": dedupeIDs(ids)})
		if err := c.setWorkAuthors(ctx, w, value, setBy); err != nil {
			return works, fmt.Errorf("move work %d: %w", w, err)
		}
	}
	return works, nil
}

// recomputeRenown — известность авторов перенесённых работ (сортировка /authors).
func (c *OverrideController) recomputeRenown(works []int64) {
	rec, ok := c.resyncer.(AuthorRenownRecomputer)
	if !ok || len(works) == 0 {
		return
	}
	spawn(func() {
		if _, err := rec.RecomputeAuthorRenownFor(workersCtx, works); err != nil {
			c.logger.Warn("author merge: renown recompute failed", "err", err)
		}
	})
}

// workAuthorIDs — авторы работы по порядку (по живым изданиям, минимальная позиция).
func workAuthorIDs(ctx context.Context, ex pgxExec, workID int64) ([]int64, error) {
	rows, err := ex.Query(ctx, `
		SELECT ba.author_id FROM books b
		JOIN book_authors ba ON ba.book_id = b.id
		WHERE b.work_id = $1 AND b.deleted = false
		GROUP BY ba.author_id
		ORDER BY min(ba.position), ba.author_id`, workID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[int64])
}

func dedupeIDs(ids []int64) []int64 {
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}
