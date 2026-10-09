package metadata

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Ключи группировки изданий из поиска экранизаций (#467). Провайдер экранизаций
// находит QID книги в Wikidata и проверяет его по автору — тот же резолв, что у
// Tier-2 группировки, но Tier-2 спрашивает только одиночные издания: у работ из
// нескольких изданий («Скотный двор» — 6, «Animal Farm» — 2) QID не искался, и
// переводы разных языков так и не склеивались (прод 2026-10: 873 группы, 2079
// работ с одинаковыми экранизациями). Найденный QID пишется в book_work_lookups
// как найденный ключ, а группировка (Tier-1, без сети) склеивает издания с
// одинаковым ключом — с теми же гейтами против мега-склеек, что у Tier-2.

// adaptationQIDProvider — провайдер экранизаций, который отдаёт и QID книги.
type adaptationQIDProvider interface {
	FetchAdaptationsWithQID(ctx context.Context, q BookQuery) ([]Adaptation, string, error)
}

// RecordBookWorkKey — найденный ключ работы source/key для издания bookID. Новый
// или изменённый ключ сбрасывает work_scanned_at: группировка пересмотрит
// издания автора и склеит по ключу.
func RecordBookWorkKey(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger, bookID int64, source, key string) {
	tag, err := pool.Exec(ctx, `
		WITH up AS (
			INSERT INTO book_work_lookups (book_id, source, outcome, work_key, checked_at)
			VALUES ($1, $2, 'found', $3, now())
			ON CONFLICT (book_id, source) DO UPDATE SET outcome = 'found', work_key = EXCLUDED.work_key, checked_at = now()
			WHERE book_work_lookups.outcome <> 'found' OR book_work_lookups.work_key IS DISTINCT FROM EXCLUDED.work_key
			RETURNING book_id
		)
		UPDATE books SET work_scanned_at = NULL WHERE id IN (SELECT book_id FROM up)`, bookID, source, key)
	if err != nil {
		logger.Warn("metadata: record book work key failed", "book_id", bookID, "source", source, "err", err)
		return
	}
	if tag.RowsAffected() > 0 {
		logger.Info("metadata: book work key recorded", "book_id", bookID, "source", source, "key", key)
	}
}

// BackfillAdaptationWorkKeys — разовый догон ключей (#467) для работ, у которых
// экранизации из Wikidata уже найдены, а ключа wikidata нет ни у одного
// издания (поиск экранизаций раньше QID не сохранял; прод 2026-10 — ~4,9 тыс.
// изданий в ~4,5 тыс. работ). Одно издание на работу — то же, что спрашивал
// поиск экранизаций (название + авторы, без запроса экранизаций), не чаще rpm в
// минуту. «Не найдено» пишется как у Tier-2. Возвращает число записанных ключей
// и done — кандидатов не осталось; сбой источника (пауза, 429) — выход без done,
// догон продолжится на следующем старте.
func BackfillAdaptationWorkKeys(ctx context.Context, pool *pgxpool.Pool, p *WikidataAdaptationsProvider, rpm int, logger *slog.Logger) (found int, done bool, err error) {
	gate := &rateGate{}
	gate.setRPM(rpm)
	var after int64
	for {
		rows, err := pool.Query(ctx, `
			SELECT DISTINCT ON (b.work_id) b.work_id, b.id, b.title, COALESCE(b.lang, ''),
			       COALESCE((SELECT array_agg(TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name)) ORDER BY ba.position)
			                 FROM book_authors ba JOIN authors a ON a.id = ba.author_id WHERE ba.book_id = b.id), '{}')
			FROM book_adaptations x
			JOIN books b ON b.id = x.book_id AND b.deleted = false
			WHERE x.provider = $3 AND b.work_id > $1
			  AND NOT EXISTS (SELECT 1 FROM books bb JOIN book_work_lookups l ON l.book_id = bb.id
			                  WHERE bb.work_id = b.work_id AND l.source = $3 AND l.outcome = 'found')
			  AND NOT EXISTS (SELECT 1 FROM book_work_lookups l WHERE l.book_id = b.id AND l.source = $3)
			ORDER BY b.work_id, b.id
			LIMIT $2`, after, 200, wikidataSource)
		if err != nil {
			return found, false, fmt.Errorf("adaptation work keys: candidates: %w", err)
		}
		type cand struct {
			work, book int64
			title      string
			lang       string
			authors    []string
		}
		var batch []cand
		for rows.Next() {
			var c cand
			if err := rows.Scan(&c.work, &c.book, &c.title, &c.lang, &c.authors); err != nil {
				rows.Close()
				return found, false, fmt.Errorf("adaptation work keys: scan: %w", err)
			}
			batch = append(batch, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return found, false, err
		}
		if len(batch) == 0 {
			return found, true, nil
		}
		for _, c := range batch {
			after = c.work
			if err := gate.wait(ctx); err != nil {
				return found, false, err
			}
			taskCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			qid, rerr := p.resolveBookQID(taskCtx, BookQuery{ID: c.book, Title: c.title, Authors: c.authors, Lang: c.lang})
			cancel()
			switch {
			case rerr == nil && qid != "":
				RecordBookWorkKey(ctx, pool, logger, c.book, wikidataSource, qid)
				found++
			case errors.Is(rerr, ErrNotFound) || (rerr == nil && qid == ""):
				if _, err := pool.Exec(ctx, `
					INSERT INTO book_work_lookups (book_id, source, outcome, checked_at) VALUES ($1, $2, 'not_found', now())
					ON CONFLICT (book_id, source) DO NOTHING`, c.book, wikidataSource); err != nil {
					return found, false, fmt.Errorf("adaptation work keys: not found: %w", err)
				}
			default:
				return found, false, rerr // пауза источника, 429, сеть — продолжим на следующем старте
			}
		}
	}
}
