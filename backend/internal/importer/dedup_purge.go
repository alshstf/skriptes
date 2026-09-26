package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"
)

// dedupPurgeKey — запись миграции 0039 в app_settings: какие книги и работы она
// удалила при схлопывании дублей и какие работы изменила. Есть только на базе,
// где дубли были.
const dedupPurgeKey = "book_identity_dedup_v1"

// dedupPurgeBatch — сколько id за один запрос к Meili.
const dedupPurgeBatch = 5000

// PurgeDedupedDocs доводит миграцию 0039 до поиска: удаляет из индекса books
// схлопнутые книги, пере-собирает документы затронутых работ в индексе works
// (опустевшие работы UpsertWorksToIndex сам удаляет). Запись снимается только
// после удачного прохода — при ошибке повторится на следующем старте.
// Возвращает число удалённых книг; без записи — 0.
func (im *Importer) PurgeDedupedDocs(ctx context.Context) (int, error) {
	var raw []byte
	err := im.deps.Pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, dedupPurgeKey).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", dedupPurgeKey, err)
	}
	var p struct {
		Books       []int64 `json:"books"`
		WorksGone   []int64 `json:"works_gone"`
		WorksUpdate []int64 `json:"works_update"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return 0, fmt.Errorf("parse %s: %w", dedupPurgeKey, err)
	}
	for ids := range slices.Chunk(p.Books, dedupPurgeBatch) {
		if err := im.deleteDocs(ctx, booksIndex, ids); err != nil {
			return 0, err
		}
	}
	for ids := range slices.Chunk(append(p.WorksGone, p.WorksUpdate...), dedupPurgeBatch) {
		if err := im.UpsertWorksToIndex(ctx, ids); err != nil {
			return 0, err
		}
	}
	if _, err := im.deps.Pool.Exec(ctx, `DELETE FROM app_settings WHERE key = $1`, dedupPurgeKey); err != nil {
		return 0, fmt.Errorf("clear %s: %w", dedupPurgeKey, err)
	}
	return len(p.Books), nil
}
