package importer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/meilisearch/meilisearch-go"
)

// ReconcileResult — что сверка индексов с PG исправила.
type ReconcileResult struct {
	WorksRemoved int // документы works без живой работы (удалены импортом, слиты группировкой)
	WorksAdded   int // живые работы без документа (досинкованы)
	BooksRemoved int // документы books (OPDS) удалённых книг
	BooksMissing int // живые книги без документа — только в лог: их пишет импорт
}

// reconcilePageSize — сколько id за запрос выкачивать из Meili.
const reconcilePageSize = 100_000

// ReconcileIndexes сверяет документы Meili с живыми записями PG и чинит
// расхождения (#283): импорт не удалял документы книг, ставших удалёнными, и
// работ, у которых не осталось живых изданий; группировка синкает поиск в
// конце прохода и теряет его при остановке (#270) — слитые работы оставались в
// поиске, клик вёл на 404. Лишние документы удаляются, недостающие работы
// досинкиваются — в том числе весь works-индекс, если Meili пуст после
// восстановления базы (#305). Дёшево: id выкачиваются порциями по 100 тыс.
//
// Предохранители: пустая выборка PG при непустом индексе и «лишних» больше
// половины индекса — отказ удалять (сбой базы не должен стирать поиск).
func (im *Importer) ReconcileIndexes(ctx context.Context) (ReconcileResult, error) {
	var res ReconcileResult

	liveWorks, err := im.pgIDs(ctx, `SELECT DISTINCT work_id FROM books WHERE deleted = false AND work_id IS NOT NULL`)
	if err != nil {
		return res, fmt.Errorf("live works: %w", err)
	}
	indexedWorks, err := im.indexIDs(ctx, worksIndex)
	if err != nil {
		return res, err
	}
	extra, missing := diffIDs(indexedWorks, liveWorks)
	if err := guardRemoval(worksIndex, len(extra), len(indexedWorks), len(liveWorks)); err != nil {
		return res, err
	}
	if err := im.DeleteWorksFromIndex(ctx, extra); err != nil {
		return res, err
	}
	res.WorksRemoved = len(extra)
	for start := 0; start < len(missing); start += 5000 {
		end := min(start+5000, len(missing))
		if err := im.UpsertWorksToIndex(ctx, missing[start:end]); err != nil {
			return res, err
		}
	}
	res.WorksAdded = len(missing)

	liveBooks, err := im.pgIDs(ctx, `SELECT id FROM books WHERE deleted = false`)
	if err != nil {
		return res, fmt.Errorf("live books: %w", err)
	}
	indexedBooks, err := im.indexIDs(ctx, booksIndex)
	if err != nil {
		return res, err
	}
	extra, missing = diffIDs(indexedBooks, liveBooks)
	if err := guardRemoval(booksIndex, len(extra), len(indexedBooks), len(liveBooks)); err != nil {
		return res, err
	}
	if err := im.deleteDocs(ctx, booksIndex, extra); err != nil {
		return res, err
	}
	res.BooksRemoved = len(extra)
	res.BooksMissing = len(missing)
	return res, nil
}

// guardRemoval — отказ удалять, если выглядит как сбой, а не как мусор.
func guardRemoval(index string, extra, indexed, live int) error {
	if extra == 0 {
		return nil
	}
	if live == 0 {
		return fmt.Errorf("reconcile %s: PG вернул 0 живых записей при %d документах — удаление отменено", index, indexed)
	}
	if extra*2 > indexed {
		return fmt.Errorf("reconcile %s: лишних %d из %d документов (больше половины) — удаление отменено", index, extra, indexed)
	}
	return nil
}

// pgIDs — множество id из запроса PG.
func (im *Importer) pgIDs(ctx context.Context, query string) (map[int64]struct{}, error) {
	rows, err := im.deps.Pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]struct{}{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

// indexIDs — все id документов индекса (только поле id, порциями).
func (im *Importer) indexIDs(ctx context.Context, index string) (map[int64]struct{}, error) {
	out := map[int64]struct{}{}
	for offset := int64(0); ; offset += reconcilePageSize {
		var page meilisearch.DocumentsResult
		if err := im.deps.Meili.Index(index).GetDocumentsWithContext(ctx, &meilisearch.DocumentsQuery{
			Fields: []string{"id"}, Limit: reconcilePageSize, Offset: offset,
		}, &page); err != nil {
			if isMeiliIndexNotFound(err) {
				return out, nil
			}
			return nil, fmt.Errorf("meili %s document ids: %w", index, err)
		}
		for _, h := range page.Results {
			var id int64
			if raw, ok := h["id"]; ok && json.Unmarshal(raw, &id) == nil {
				out[id] = struct{}{}
			}
		}
		if int64(len(page.Results)) < reconcilePageSize {
			return out, nil
		}
	}
}

// diffIDs — что есть в индексе, но не в PG (extra), и наоборот (missing).
func diffIDs(indexed, live map[int64]struct{}) (extra, missing []int64) {
	for id := range indexed {
		if _, ok := live[id]; !ok {
			extra = append(extra, id)
		}
	}
	for id := range live {
		if _, ok := indexed[id]; !ok {
			missing = append(missing, id)
		}
	}
	return extra, missing
}
