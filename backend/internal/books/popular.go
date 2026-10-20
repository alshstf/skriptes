package books

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/meilisearch/meilisearch-go"
)

// Известность работ из индекса works (поле popularity, формула —
// importer/popularity.go) для харнесса оценки рекомендаций (cmd/recseval,
// план recsys/01 1.5) и бейзлайна «популярное». В PG popularity не хранится.

// TopPopularWorkIDs — n самых известных видимых работ, начиная с offset.
func (s *Service) TopPopularWorkIDs(ctx context.Context, offset, n int) ([]int64, error) {
	if s.meili == nil || n <= 0 {
		return nil, nil
	}
	req := &meilisearch.SearchRequest{
		Limit:                int64(n),
		Offset:               int64(offset),
		Sort:                 []string{"popularity:desc"},
		AttributesToRetrieve: []string{"id"},
	}
	res, err := searchVisible(ctx, s.meili.Index(worksIndexName), "", req)
	if err != nil {
		return nil, fmt.Errorf("meili popular: %w", err)
	}
	out := make([]int64, 0, len(res.Hits))
	for _, h := range res.Hits {
		if id := hitID(h); id > 0 {
			out = append(out, id)
		}
	}
	return out, nil
}

// VisibleWorksCount — сколько видимых работ в индексе (знаменатель покрытия).
func (s *Service) VisibleWorksCount(ctx context.Context) (int64, error) {
	if s.meili == nil {
		return 0, nil
	}
	res, err := searchVisible(ctx, s.meili.Index(worksIndexName), "", &meilisearch.SearchRequest{Limit: 1, AttributesToRetrieve: []string{"id"}})
	if err != nil {
		return 0, fmt.Errorf("meili count: %w", err)
	}
	return res.EstimatedTotalHits, nil
}

// WorkPopularities — известность работ по id (документы индекса works; нет в
// индексе — нет в ответе).
func (s *Service) WorkPopularities(ctx context.Context, ids []int64) (map[int64]int64, error) {
	out := map[int64]int64{}
	if s.meili == nil || len(ids) == 0 {
		return out, nil
	}
	const batch = 500
	for start := 0; start < len(ids); start += batch {
		end := min(start+batch, len(ids))
		strs := make([]string, 0, end-start)
		for _, id := range ids[start:end] {
			strs = append(strs, strconv.FormatInt(id, 10))
		}
		var res meilisearch.DocumentsResult
		err := s.meili.Index(worksIndexName).GetDocumentsWithContext(ctx, &meilisearch.DocumentsQuery{
			Limit:  int64(end - start),
			Fields: []string{"id", "popularity"},
			Filter: "id IN [" + strings.Join(strs, ",") + "]",
		}, &res)
		if err != nil {
			return nil, fmt.Errorf("meili documents: %w", err)
		}
		for _, d := range res.Results {
			var doc struct {
				ID         int64 `json:"id"`
				Popularity int64 `json:"popularity"`
			}
			raw, err := json.Marshal(d)
			if err != nil {
				continue
			}
			if err := json.Unmarshal(raw, &doc); err == nil && doc.ID > 0 {
				out[doc.ID] = doc.Popularity
			}
		}
	}
	return out, nil
}

// hitID — id документа из хита Meili (поля хита — сырой JSON).
func hitID(h meilisearch.Hit) int64 {
	raw, ok := h["id"]
	if !ok {
		return 0
	}
	var id int64
	if err := json.Unmarshal(raw, &id); err != nil {
		return 0
	}
	return id
}
