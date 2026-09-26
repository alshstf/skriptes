package metadata

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AuthorQIDSink — колбэк «bio-путь зарезолвил Wikidata QID автора».
// Провайдеры (Wikipedia pageprops, OpenLibrary remote_ids) резолвят QID для
// occupation-гейта и раньше ВЫБРАСЫВАЛИ его; теперь через sink он персистится
// в authors.ext_ids->>'wd_qid' (колонка ext_ids JSONB задумана под внешние id
// с 0001, зеркало works.ext_ids). Bio-derived QID надёжнее самостоятельного
// wbsearchentities-резолва: он прошёл имя-гейт authorNameMatches И
// occupation-гейт P106 (защита от казуса «Q46405 ≠ Пратчетт»).
// QID — сырьё био-таймлайна (EnsureAuthorEvents, план cryptic-roaming-turing).
type AuthorQIDSink func(ctx context.Context, authorID int64, qid string)

// AuthorQIDPersister — стандартный sink: идемпотентный jsonb_set в
// authors.ext_ids. Уже сохранённый QID не перетирается (первый выигрывает —
// wiki-путь идёт раньше OL в цепочке провайдеров и точнее).
func AuthorQIDPersister(pool *pgxpool.Pool, logger *slog.Logger) AuthorQIDSink {
	if logger == nil {
		logger = slog.Default()
	}
	return func(ctx context.Context, authorID int64, qid string) {
		if authorID == 0 || qid == "" {
			return
		}
		if _, err := pool.Exec(ctx, `
			UPDATE authors
			SET ext_ids = jsonb_set(COALESCE(ext_ids, '{}'::jsonb), '{wd_qid}', to_jsonb($1::text))
			WHERE id = $2 AND COALESCE(ext_ids->>'wd_qid', '') = ''`,
			qid, authorID); err != nil {
			logger.Warn("metadata: persist author wd_qid failed", "author_id", authorID, "err", err)
		}
	}
}
