package importer

import (
	"context"
	"fmt"

	"github.com/meilisearch/meilisearch-go"
)

const booksIndex = "books"

// bookDoc — документ для индекса "books" в Meilisearch.
// id используется как primary key (совпадает с books.id в Postgres).
type bookDoc struct {
	ID              int64    `json:"id"`
	Title           string   `json:"title"`
	NormalizedTitle string   `json:"normalized_title"`
	Authors         []string `json:"authors"`
	AuthorIDs       []int64  `json:"author_ids"`
	Series          string   `json:"series,omitempty"`
	SeriesID        *int64   `json:"series_id,omitempty"`
	Genres          []string `json:"genres"`
	Year            *int     `json:"year,omitempty"` // = written_year; синкается ResyncYears (НЕ date_added)
	Lang            string   `json:"lang,omitempty"`
	Popularity      int64    `json:"popularity"` // обновляется отдельным процессом
	LibID           string   `json:"lib_id"`
	Archive         string   `json:"archive"`
}

// configureIndex применяет настройки к индексу books идемпотентно.
// Запускается один раз в начале каждого импорта.
func configureIndex(ctx context.Context, m meilisearch.ServiceManager) error {
	idx := m.Index(booksIndex)

	// Создание индекса с указанием primary key. Если уже есть — это no-op.
	if _, err := m.CreateIndexWithContext(ctx, &meilisearch.IndexConfig{
		Uid:        booksIndex,
		PrimaryKey: "id",
	}); err != nil {
		// 'index_already_exists' — это нормально для повторных запусков.
		if !isMeiliAlreadyExists(err) {
			return fmt.Errorf("create index: %w", err)
		}
	}

	// Настройки настраиваем по отдельности, а не через UpdateSettings одной
	// большой структурой: SDK добавляет в Settings поля новее серверной
	// версии (например disableOnNumbers в TypoTolerance), и сервер ругается
	// на неизвестные поля. Поэтому шлём только то, что нам нужно.
	if _, err := idx.UpdateSearchableAttributesWithContext(ctx, &[]string{"title", "authors", "series"}); err != nil {
		return fmt.Errorf("update searchable: %w", err)
	}
	filterable := []any{"genres", "lang", "year", "series_id", "author_ids"}
	if _, err := idx.UpdateFilterableAttributesWithContext(ctx, &filterable); err != nil {
		return fmt.Errorf("update filterable: %w", err)
	}
	if _, err := idx.UpdateSortableAttributesWithContext(ctx, &[]string{"year", "popularity"}); err != nil {
		return fmt.Errorf("update sortable: %w", err)
	}
	if _, err := idx.UpdateRankingRulesWithContext(ctx,
		&[]string{"words", "typo", "proximity", "attribute", "sort", "exactness", "popularity:desc"}); err != nil {
		return fmt.Errorf("update ranking: %w", err)
	}
	return nil
}

// indexer аккумулирует документы в батч и flush-ит их в Meilisearch.
// Не потокобезопасен — используется одним горутином в Importer.
type indexer struct {
	mgr   meilisearch.ServiceManager
	batch []bookDoc
	limit int
}

func newIndexer(m meilisearch.ServiceManager, batchSize int) *indexer {
	if batchSize <= 0 {
		batchSize = 1000
	}
	return &indexer{mgr: m, limit: batchSize, batch: make([]bookDoc, 0, batchSize)}
}

func (i *indexer) add(doc bookDoc) error {
	i.batch = append(i.batch, doc)
	if len(i.batch) >= i.limit {
		return i.flush(context.Background())
	}
	return nil
}

// flush отправляет накопленные документы в Meili (upsert по id) и
// дожидается завершения соответствующей задачи Meili.
// Без ожидания таски выполняются асинхронно — это создаёт гонку с
// последующим поиском (например, в тестах сразу после flush).
func (i *indexer) flush(ctx context.Context) error {
	if len(i.batch) == 0 {
		return nil
	}
	idx := i.mgr.Index(booksIndex)
	pk := "id"
	task, err := idx.AddDocumentsWithContext(ctx, i.batch, &meilisearch.DocumentOptions{PrimaryKey: &pk})
	if err != nil {
		return fmt.Errorf("meili add documents: %w", err)
	}
	final, err := i.mgr.WaitForTaskWithContext(ctx, task.TaskUID, 0)
	if err != nil {
		return fmt.Errorf("wait meili task %d: %w", task.TaskUID, err)
	}
	if final.Status != meilisearch.TaskStatusSucceeded {
		return fmt.Errorf("meili task %d ended with status %s: %v", final.UID, final.Status, final.Error)
	}
	i.batch = i.batch[:0]
	return nil
}

// isMeiliAlreadyExists возвращает true если ошибка Meili — "index_already_exists".
func isMeiliAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	mErr := &meilisearch.Error{}
	if as := errorsAs(err, mErr); as && mErr.MeilisearchApiError.Code == "index_already_exists" {
		return true
	}
	// fallback по строке (на случай иной упаковки ошибки)
	return contains(err.Error(), "index_already_exists")
}

// маленькие inline-обёртки чтобы не тащить fmt/strings в caller.
func contains(s, sub string) bool {
	return len(sub) <= len(s) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// errorsAs — обёртка вокруг errors.As, чтобы не тащить пакет в файл,
// который и так обильно нагружен. Чисто для краткости.
func errorsAs(err error, target any) bool {
	type asErr interface{ As(target any) bool }
	if x, ok := err.(asErr); ok {
		return x.As(target)
	}
	return false
}
