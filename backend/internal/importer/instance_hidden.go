package importer

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/meilisearch/meilisearch-go"

	"github.com/skriptes/skriptes/backend/internal/workauthors"
)

// Скрытие авторов и работ на инстансе (админ, миграция 0064). Явные скрытия —
// hidden_authors / hidden_works; флаги instance_hidden у авторов, работ и книг —
// их материализация для фильтров (PG — bookExclusionClause и др., Meili —
// `instance_hidden != true`). Владелец (2026-10-19): скрыть автора — скрыть всё
// связанное с ним, включая совместные книги и сборники; скрытое недоступно
// никому, кроме админа; из избранного и полок пропадает молча.
//
// Правила:
//   - работа скрыта — явно, или скрытый автор — её автор (workauthors.Core), или у
//     неё нет живого издания без скрытого автора;
//   - книга скрыта — среди её авторов скрытый, или скрыта её работа.
//
// Чужая работа, в одно из изданий которой (антология) попал скрытый автор,
// остаётся: скрывается только это издание.

// recomputeAuthorsSQL — флаг автора = есть ли он в hidden_authors.
const recomputeAuthorsSQL = `
	UPDATE authors a SET instance_hidden = h.hidden
	FROM (SELECT a2.id, (ha.author_id IS NOT NULL) AS hidden
	      FROM authors a2 LEFT JOIN hidden_authors ha ON ha.author_id = a2.id
	      WHERE a2.instance_hidden OR ha.author_id IS NOT NULL) h
	WHERE a.id = h.id AND a.instance_hidden IS DISTINCT FROM h.hidden`

// recomputeWorksSQL — кандидаты: явно скрытые, работы изданий скрытых авторов и
// уже помеченные (для возврата).
var recomputeWorksSQL = `
	WITH cand AS (
		SELECT work_id AS id FROM hidden_works
		UNION
		SELECT b.work_id FROM book_authors ba
		JOIN hidden_authors ha ON ha.author_id = ba.author_id
		JOIN books b ON b.id = ba.book_id
		WHERE b.work_id IS NOT NULL
		UNION
		SELECT id FROM works WHERE instance_hidden
	), calc AS (
		SELECT c.id,
		       EXISTS (SELECT 1 FROM hidden_works hw WHERE hw.work_id = c.id)
		       OR EXISTS (SELECT 1 FROM (` + workauthors.Core("c.id") + `) wa
		                  JOIN hidden_authors ha ON ha.author_id = wa.author_id)
		       OR (EXISTS (SELECT 1 FROM books e WHERE e.work_id = c.id AND e.deleted = false)
		           AND NOT EXISTS (
		               SELECT 1 FROM books e WHERE e.work_id = c.id AND e.deleted = false
		                 AND NOT EXISTS (SELECT 1 FROM book_authors ba
		                                 JOIN hidden_authors ha ON ha.author_id = ba.author_id
		                                 WHERE ba.book_id = e.id)))
		       AS hidden
		FROM cand c
	)
	UPDATE works w SET instance_hidden = calc.hidden FROM calc
	WHERE w.id = calc.id AND w.instance_hidden IS DISTINCT FROM calc.hidden
	RETURNING w.id`

// recomputeBooksSQL — после работ: книга скрыта по автору или по работе.
const recomputeBooksSQL = `
	WITH cand AS (
		SELECT ba.book_id AS id FROM book_authors ba JOIN hidden_authors ha ON ha.author_id = ba.author_id
		UNION
		SELECT b.id FROM books b JOIN works w ON w.id = b.work_id WHERE w.instance_hidden
		UNION
		SELECT id FROM books WHERE instance_hidden
	), calc AS (
		SELECT c.id,
		       EXISTS (SELECT 1 FROM book_authors ba JOIN hidden_authors ha ON ha.author_id = ba.author_id
		               WHERE ba.book_id = c.id)
		       OR EXISTS (SELECT 1 FROM books b JOIN works w ON w.id = b.work_id
		                  WHERE b.id = c.id AND w.instance_hidden)
		       AS hidden
		FROM cand c
	)
	UPDATE books b SET instance_hidden = calc.hidden FROM calc
	WHERE b.id = calc.id AND b.instance_hidden IS DISTINCT FROM calc.hidden
	RETURNING b.id`

// RecomputeInstanceHidden — пересчёт флагов instance_hidden в PG (одной
// транзакцией: авторы → работы → книги). Возвращает id работ и книг, у которых
// флаг поменялся.
func (im *Importer) RecomputeInstanceHidden(ctx context.Context) (books, works []int64, err error) {
	err = pgx.BeginFunc(ctx, im.deps.Pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, recomputeAuthorsSQL); err != nil {
			return fmt.Errorf("authors: %w", err)
		}
		var err error
		if works, err = collectIDs(ctx, tx, recomputeWorksSQL); err != nil {
			return fmt.Errorf("works: %w", err)
		}
		if books, err = collectIDs(ctx, tx, recomputeBooksSQL); err != nil {
			return fmt.Errorf("books: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("instance hidden: recompute %w", err)
	}
	return books, works, nil
}

// SyncInstanceHidden — пересчёт флагов и синхронизация Meili. Вызывать после
// скрытия/возврата и после импорта INPX (новые издания скрытого автора, документы
// изменившихся книг пересобраны без флага). В Meili уходят изменившиеся и все
// скрытые документы (скрытых мало). Возвращает число изменившихся книг и работ.
func (im *Importer) SyncInstanceHidden(ctx context.Context) (books, works int, err error) {
	changedBooks, changedWorks, err := im.RecomputeInstanceHidden(ctx)
	if err != nil {
		return 0, 0, err
	}
	hiddenBooks, err := collectIDs(ctx, im.deps.Pool, `SELECT id FROM books WHERE instance_hidden AND deleted = false`)
	if err != nil {
		return 0, 0, fmt.Errorf("instance hidden: hidden books: %w", err)
	}
	hiddenWorks, err := collectIDs(ctx, im.deps.Pool, `SELECT id FROM works WHERE instance_hidden`)
	if err != nil {
		return 0, 0, fmt.Errorf("instance hidden: hidden works: %w", err)
	}
	if err := im.pushBookHidden(ctx, union(changedBooks, hiddenBooks)); err != nil {
		return 0, 0, err
	}
	if err := im.UpsertWorksToIndex(ctx, union(changedWorks, hiddenWorks)); err != nil {
		return 0, 0, fmt.Errorf("instance hidden: works index: %w", err)
	}
	return len(changedBooks), len(changedWorks), nil
}

func union(a, b []int64) []int64 {
	seen := make(map[int64]struct{}, len(a)+len(b))
	out := make([]int64, 0, len(a)+len(b))
	for _, list := range [][]int64{a, b} {
		for _, id := range list {
			if _, ok := seen[id]; !ok {
				seen[id] = struct{}{}
				out = append(out, id)
			}
		}
	}
	return out
}

// pushBookHidden — частичное обновление поля instance_hidden в индексе books.
// Только живые издания (удалённых в индексе нет, а частичное обновление
// несуществующего документа создало бы огрызок).
func (im *Importer) pushBookHidden(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	rows, err := im.deps.Pool.Query(ctx,
		`SELECT id, instance_hidden FROM books WHERE id = ANY($1) AND deleted = false`, ids)
	if err != nil {
		return fmt.Errorf("instance hidden: books: %w", err)
	}
	type doc struct {
		ID     int64 `json:"id"`
		Hidden bool  `json:"instance_hidden"`
	}
	docs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (doc, error) {
		var d doc
		return d, r.Scan(&d.ID, &d.Hidden)
	})
	if err != nil {
		return fmt.Errorf("instance hidden: books scan: %w", err)
	}
	pk := "id"
	const batchSize = 1000
	for i := 0; i < len(docs); i += batchSize {
		batch := docs[i:min(i+batchSize, len(docs))]
		task, err := im.deps.Meili.Index(booksIndex).UpdateDocumentsWithContext(ctx, batch, &meilisearch.DocumentOptions{PrimaryKey: &pk})
		if err != nil {
			return fmt.Errorf("instance hidden: meili books: %w", err)
		}
		final, err := im.deps.Meili.WaitForTaskWithContext(ctx, task.TaskUID, 0)
		if err != nil {
			return fmt.Errorf("instance hidden: wait books task %d: %w", task.TaskUID, err)
		}
		if final.Status != meilisearch.TaskStatusSucceeded {
			return fmt.Errorf("instance hidden: books task %d status %s: %v", final.UID, final.Status, final.Error)
		}
	}
	return nil
}
