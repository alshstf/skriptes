package importer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/meilisearch/meilisearch-go"
)

// worksRebuildIndex — временный индекс, в котором RebuildWorksIndex собирает
// новую версию works перед подменой.
const worksRebuildIndex = worksIndex + "_rebuild"

// RebuildWorksIndex собирает индекс works заново во временном индексе — сразу с
// настройками текущей схемы (свёрнутые поисковые поля) — и атомарно подменяет им
// живой (swap). Поиск всё это время работает по прежнему индексу.
//
// Зачем не ресинк на месте: смена searchableAttributes в Meili 1.13 переиндексирует
// документы, но близость слов у документов, где свёрнутое слово отличается от
// прежнего, остаётся битой — на проде после 1.15.3 у 39 из 40 работ с «ё» в
// названии proximity 0,25 вместо 1,0, и «Три мушкетёра» Дюма стояли 40-ми после
// всех «Три мушкетера». Документы, добавленные в индекс с готовыми настройками,
// считаются верно.
//
// Изменения работ, пришедшие в живой индекс во время сборки, досылаются после
// подмены (works.updated_at ≥ начала); удалённые за это время уберёт
// ReconcileIndexes. Возвращает число документов.
func (im *Importer) RebuildWorksIndex(ctx context.Context) (int, error) {
	m := im.deps.Meili
	started := time.Now()
	if err := im.deleteIndex(ctx, worksRebuildIndex); err != nil {
		return 0, fmt.Errorf("drop stale rebuild index: %w", err)
	}
	if err := configureWorksIndex(ctx, m, worksRebuildIndex, WorksIndexSchemaVersion); err != nil {
		return 0, fmt.Errorf("configure rebuild index: %w", err)
	}
	n, err := im.resyncWorksInto(ctx, worksRebuildIndex)
	if err != nil {
		return n, fmt.Errorf("fill rebuild index: %w", err)
	}
	// Живой индекс должен существовать для swap (свежая установка).
	if err := im.ConfigureWorksIndex(ctx); err != nil {
		return n, fmt.Errorf("configure works index: %w", err)
	}
	taskUID, err := im.swapIndexes(ctx, worksIndex, worksRebuildIndex)
	if err != nil {
		return n, fmt.Errorf("swap works index: %w", err)
	}
	if err := im.waitTask(ctx, taskUID); err != nil {
		return n, fmt.Errorf("swap works index: %w", err)
	}
	changed, err := im.worksUpdatedSince(ctx, started)
	if err != nil {
		return n, err
	}
	if err := im.UpsertWorksToIndex(ctx, changed); err != nil {
		return n, fmt.Errorf("catch up works changed during rebuild: %w", err)
	}
	// Во временном индексе теперь прежние данные — не нужны.
	if err := im.deleteIndex(ctx, worksRebuildIndex); err != nil {
		return n, fmt.Errorf("drop old works index: %w", err)
	}
	return n, nil
}

// swapIndexes — POST /swap-indexes напрямую: SwapIndexesWithContext клиента
// meilisearch-go (≤0.36.3) всегда шлёт поле rename (нет omitempty), и Meili 1.13
// отвечает 400 «Unknown field `rename`».
func (im *Importer) swapIndexes(ctx context.Context, a, b string) (int64, error) {
	if im.deps.MeiliURL == "" {
		return 0, errors.New("meili url is not configured")
	}
	body, err := json.Marshal([]map[string][]string{{"indexes": {a, b}}})
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(im.deps.MeiliURL, "/")+"/swap-indexes", bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if im.deps.MeiliAPIKey != "" {
		req.Header.Set("Authorization", "Bearer "+im.deps.MeiliAPIKey)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		TaskUID int64  `json:"taskUid"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return 0, fmt.Errorf("swap-indexes: status %d: %w", resp.StatusCode, err)
	}
	if resp.StatusCode != http.StatusAccepted {
		return 0, fmt.Errorf("swap-indexes: status %d: %s", resp.StatusCode, out.Message)
	}
	return out.TaskUID, nil
}

func (im *Importer) worksUpdatedSince(ctx context.Context, since time.Time) ([]int64, error) {
	rows, err := im.deps.Pool.Query(ctx, `SELECT id FROM works WHERE updated_at >= $1`, since)
	if err != nil {
		return nil, fmt.Errorf("works updated during rebuild: %w", err)
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

// deleteIndex удаляет индекс и ждёт задачу; отсутствующий индекс — не ошибка.
func (im *Importer) deleteIndex(ctx context.Context, uid string) error {
	task, err := im.deps.Meili.DeleteIndexWithContext(ctx, uid)
	if err != nil {
		if isMeiliIndexNotFound(err) {
			return nil
		}
		return err
	}
	final, err := im.deps.Meili.WaitForTaskWithContext(ctx, task.TaskUID, 0)
	if err != nil {
		return err
	}
	if final.Status != meilisearch.TaskStatusSucceeded && final.Error.Code != "index_not_found" {
		return fmt.Errorf("delete index %s: task %d %s: %v", uid, final.UID, final.Status, final.Error)
	}
	return nil
}

func (im *Importer) waitTask(ctx context.Context, uid int64) error {
	final, err := im.deps.Meili.WaitForTaskWithContext(ctx, uid, 0)
	if err != nil {
		return err
	}
	if final.Status != meilisearch.TaskStatusSucceeded {
		return fmt.Errorf("task %d %s: %v", final.UID, final.Status, final.Error)
	}
	return nil
}
