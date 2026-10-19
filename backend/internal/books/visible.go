package books

import (
	"context"
	"fmt"

	"github.com/meilisearch/meilisearch-go"
)

// visibleFilter — скрытое на инстансе (админ, миграция 0064) не выдаёт никакой
// поиск: список, подсказки, подборки, «известные совпадения», OPDS. Документ
// без поля (никогда не скрывался) проходит — включение фичи не требует
// переиндексации.
//
// Именно `NOT … = true`, не `!= true`: в Meili 1.13 `instance_hidden != true`
// на индексе, где поля нет НИ В ОДНОМ документе (прод сразу после деплоя),
// возвращает пустоту — каталог исчез бы целиком (поймано тестом OPDS2,
// проба на тест-контейнере 2026-10-19). `NOT instance_hidden = true` верно и
// без поля, и когда оно есть у части документов.
const visibleFilter = "NOT instance_hidden = true"

// searchVisible — SearchWithContext с обязательным фильтром видимости. Все
// запросы к Meili в пакете идут через него: новый поиск без фильтра скрытого
// не появится.
func searchVisible(ctx context.Context, idx meilisearch.IndexManager, q string, req *meilisearch.SearchRequest) (*meilisearch.SearchResponse, error) {
	switch f := req.Filter.(type) {
	case nil:
		req.Filter = visibleFilter
	case string:
		req.Filter = andFilter(f, visibleFilter)
	default:
		return nil, fmt.Errorf("searchVisible: фильтр %T не поддержан", f)
	}
	return idx.SearchWithContext(ctx, q, req)
}
