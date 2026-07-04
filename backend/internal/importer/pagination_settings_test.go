package importer_test

import (
	"context"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/stretchr/testify/require"
)

// ConfigureIndex/ConfigureWorksIndex обязаны поднимать maxTotalHits обоих
// индексов: Meili-дефолт 1000 капит total (счётчик «N книг» на /books) и молча
// обрезает deep-paging. На 20-книжной фикстуре кап не воспроизводится, поэтому
// фиксируем саму настройку — она применяется на каждом старте backend.
func TestConfigureIndexes_SetMaxTotalHits(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	mgr := startMeilisearch(t, ctx)
	imp := importer.New(importer.Deps{Meili: mgr})

	require.NoError(t, imp.ConfigureIndex(ctx))
	require.NoError(t, imp.ConfigureWorksIndex(ctx))

	// Настройки Meili применяются асинхронной таской — ждём.
	for _, idx := range []string{"books", "works"} {
		require.Eventually(t, func() bool {
			p, err := mgr.Index(idx).GetPaginationWithContext(ctx)
			return err == nil && p != nil && p.MaxTotalHits == importer.MeiliMaxTotalHits
		}, 30*time.Second, 200*time.Millisecond, "index %s: maxTotalHits не применился", idx)
	}
}
