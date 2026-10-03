package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// Разделение автора (#356): под одним именем в INPX бывают два человека без
// уточнения — «Берг Николай»: поэт XIX века и автор сетевой литературы. Импорт
// видит одного автора, карточка и обогащение смешивают обоих. Администратор
// выбирает работы и уточнение («поэт»), и они переходят к автору с тем же
// именем и этим уточнением (новому или уже существующему) — через ручную правку
// авторов работы (setWorkAuthors): она переживает переимпорт
// (ReapplyAfterImport) и откатывается по работе (RevertOverride). Био и фото
// прежнего автора сбрасываются (журнал author_meta_recheck, для отката) — могли
// быть о любом из двоих; обогащение заново найдёт их по разделённым книгам.

var (
	// ErrSplitAuthorNote — нужно уточнение для нового автора (до 100 символов),
	// отличное от уточнения прежнего.
	ErrSplitAuthorNote = errors.New("split author: note required")
	// ErrSplitAuthorWorks — нет работ или среди них есть не этого автора.
	ErrSplitAuthorWorks = errors.New("split author: works must belong to the author")
)

// SplitAuthor переносит работы workIDs автора authorID к автору с тем же именем и
// уточнением note и возвращает его id. setBy — id администратора (журнал правок).
func (c *OverrideController) SplitAuthor(ctx context.Context, authorID int64, note string, workIDs []int64, setBy int64) (int64, error) {
	note = strings.TrimSpace(note)
	if note == "" || utf8.RuneCountInString(note) > 100 {
		return 0, ErrSplitAuthorNote
	}
	works := slices.Compact(slices.Sorted(slices.Values(workIDs)))
	if len(works) == 0 {
		return 0, ErrSplitAuthorWorks
	}
	var last, first, middle, normalized, oldNote, oldBio, oldPhoto string
	if err := c.pool.QueryRow(ctx, `
		SELECT last_name, COALESCE(first_name, ''), COALESCE(middle_name, ''), normalized_name,
		       COALESCE(name_note, ''), COALESCE(bio, ''), COALESCE(photo_path, '')
		FROM authors WHERE id = $1`, authorID).
		Scan(&last, &first, &middle, &normalized, &oldNote, &oldBio, &oldPhoto); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrOverrideTargetNotFound
		}
		return 0, err
	}
	if strings.EqualFold(note, oldNote) {
		return 0, ErrSplitAuthorNote
	}
	var owned int
	if err := c.pool.QueryRow(ctx, `
		SELECT count(DISTINCT b.work_id) FROM books b
		JOIN book_authors ba ON ba.book_id = b.id AND ba.author_id = $2
		WHERE b.work_id = ANY($1) AND b.deleted = false`, works, authorID).Scan(&owned); err != nil {
		return 0, err
	}
	if owned != len(works) {
		return 0, ErrSplitAuthorWorks
	}

	// Автор с тем же именем и уточнением — уникален по (normalized_name, уточнение),
	// миграция 0040; уже есть — переносим к нему.
	var newID int64
	if err := c.pool.QueryRow(ctx, `
		INSERT INTO authors (last_name, first_name, middle_name, normalized_name, name_note)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (normalized_name, lower(COALESCE(name_note, ''))) DO UPDATE SET name_note = authors.name_note
		RETURNING id`, last, first, middle, normalized, note).Scan(&newID); err != nil {
		return 0, fmt.Errorf("create split author: %w", err)
	}
	if newID == authorID {
		return 0, ErrSplitAuthorNote
	}

	for _, w := range works {
		ids, err := workAuthorIDs(ctx, c.pool, w)
		if err != nil {
			return 0, err
		}
		for i, id := range ids {
			if id == authorID {
				ids[i] = newID
			}
		}
		ids = dedupeIDs(ids)
		value, _ := json.Marshal(map[string][]int64{"author_ids": ids})
		if err := c.setWorkAuthors(ctx, w, value, setBy); err != nil {
			return 0, fmt.Errorf("move work %d: %w", w, err)
		}
	}

	// Био и фото прежнего автора могли быть о любом из двоих — сбрасываем, прежние
	// значения в журнал перепроверки (откат); обогащение найдёт обоих заново.
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, f := range []struct{ field, old string }{{"bio", oldBio}, {"photo", oldPhoto}} {
		if f.old == "" {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO author_meta_recheck (author_id, field, action, old_value, new_value, reason)
			VALUES ($1, $2, 'cleared', $3, NULL, $4)`,
			authorID, f.field, f.old, fmt.Sprintf("author split: works moved to author %d (%s)", newID, note)); err != nil {
			return 0, err
		}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE authors SET bio = NULL, photo_path = NULL, metadata_fetched_at = NULL
		WHERE id = ANY($1)`, []int64{authorID, newID}); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}

	// Известность обоих (сортировка /authors): авторы перенесённых работ и одной
	// оставшейся работы прежнего автора, по всему их корпусу.
	if rec, ok := c.resyncer.(AuthorRenownRecomputer); ok {
		recompute := append([]int64(nil), works...)
		var keep int64
		if err := c.pool.QueryRow(ctx, `
			SELECT b.work_id FROM books b JOIN book_authors ba ON ba.book_id = b.id
			WHERE ba.author_id = $1 AND b.deleted = false AND b.work_id IS NOT NULL LIMIT 1`, authorID).Scan(&keep); err == nil {
			recompute = append(recompute, keep)
		}
		spawn(func() {
			if _, err := rec.RecomputeAuthorRenownFor(workersCtx, recompute); err != nil {
				c.logger.Warn("author split: renown recompute failed", "err", err)
			}
		})
	}
	c.logger.Info("author split", "author_id", authorID, "new_author_id", newID, "note", note, "works", len(works))
	return newID, nil
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
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
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
