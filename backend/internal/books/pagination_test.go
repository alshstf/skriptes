package books

import (
	"context"
	"testing"

	"github.com/meilisearch/meilisearch-go"
	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/stretchr/testify/require"
)

// Зеркальная константа обязана совпадать с importer.MeiliMaxTotalHits:
// guard глубины offset (здесь) и настройка pagination обоих индексов (importer)
// должны говорить об одном потолке.
func TestMeiliMaxTotalHitsMirrorsImporter(t *testing.T) {
	require.EqualValues(t, importer.MeiliMaxTotalHits, meiliMaxTotalHits)
}

// Guard глубины пагинации срабатывает ДО похода в Meili: клиент указывает на
// закрытый порт — реальный запрос вернул бы ошибку соединения, guard же отдаёт
// пустую страницу без ошибки (не 5xx и не повтор последней страницы, который
// зациклил бы infinite-scroll фронта).
func TestPaginationGuardReturnsEmptyPage(t *testing.T) {
	svc := New(nil, meilisearch.New("http://127.0.0.1:1"), nil)
	ctx := context.Background()

	for _, offset := range []int{meiliMaxTotalHits, meiliMaxTotalHits - 10, meiliMaxTotalHits * 3} {
		res, err := svc.List(ctx, ListParams{Offset: offset, Limit: 20})
		require.NoError(t, err, "offset=%d", offset)
		require.Empty(t, res.Items)
		require.Equal(t, 20, res.Limit)

		wres, err := svc.ListWorks(ctx, ListParams{Offset: offset, Limit: 20})
		require.NoError(t, err, "offset=%d", offset)
		require.Empty(t, wres.Items)
	}
}
