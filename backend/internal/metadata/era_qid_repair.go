package metadata

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ClearEraRejectedQIDs — разовый догон #572 для 1.71.0: точечная перепроверка
// (author_recheck_era_v1) очистила чужие био по гейту «умер за 200+ лет до книг
// современных жанров», но QID и годы жизни того же отвергнутого кандидата
// оставила (снимать их перепроверка научилась позже). Снимает QID и годы у
// авторов без био, чья био очищена именно этим гейтом (причина в журнале
// author_meta_recheck). Возвращает число авторов.
func ClearEraRejectedQIDs(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE authors a SET ext_ids = a.ext_ids - 'wd_qid', born_year = NULL, died_year = NULL
		WHERE COALESCE(a.bio, '') = '' AND a.ext_ids ? 'wd_qid'
		  AND EXISTS (SELECT 1 FROM author_meta_recheck r
		              WHERE r.author_id = a.id AND r.field = 'bio' AND r.action = 'cleared'
		                AND r.reason LIKE '%reject policy «died %, only modern genres from %')`)
	if err != nil {
		return 0, fmt.Errorf("era rejected qids: %w", err)
	}
	return tag.RowsAffected(), nil
}
