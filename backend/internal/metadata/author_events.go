package metadata

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// EnsureAuthorEvents — события биографии автора (био-таймлайн, план
// cryptic-roaming-turing). Single-shot по authors.events_fetched_at, зеркало
// EnsureAdaptations: inflight-lock, guard, транзиент не ставит маркер
// (грабля №20). Гейт по renown — таймлайн только для «ядра» (авторы с
// внешней известностью; на проде ~19k из 138k): у безвестных авторов
// событий всё равно нет, а бюджет WDQS не резиновый.
//
// PR-1: только Wikidata-скелет; Wikipedia-вехи из текста — PR-2.
func (e *Enricher) WithAuthorEvents(p *WikidataEventsProvider, occupationGate func(ctx context.Context, qid string) (OccupationVerdict, error)) *Enricher {
	e.wdEvents = p
	e.eventsQIDGate = occupationGate
	return e
}

func (e *Enricher) EnsureAuthorEvents(ctx context.Context, authorID int64) {
	if e.wdEvents == nil || e.pool == nil || authorID <= 0 {
		return
	}
	if !e.tryLock(e.inflightAuthorEvents, authorID) {
		return
	}
	defer e.unlock(e.inflightAuthorEvents, authorID)

	var (
		renown    int64
		fetchedAt *time.Time
		qid       string
		fullName  string
	)
	if err := e.pool.QueryRow(ctx, `
		SELECT renown, events_fetched_at, COALESCE(ext_ids->>'wd_qid', ''),
		       TRIM(CONCAT_WS(' ', last_name, first_name, middle_name))
		FROM authors WHERE id = $1`, authorID,
	).Scan(&renown, &fetchedAt, &qid, &fullName); err != nil {
		e.logger.Warn("metadata: query author for events failed", "author_id", authorID, "err", err)
		return
	}
	if renown == 0 || fetchedAt != nil {
		return // не ядро / уже пробовали
	}

	transient := false
	// QID: приоритет — bio-derived из ext_ids (прошёл имя+P106 гейты);
	// фолбэк — резолв по имени с occupation-гейтом.
	if qid == "" {
		resolved, err := e.wdEvents.ResolveAuthorQID(ctx, fullName, e.eventsQIDGate)
		switch {
		case err == nil && resolved != "":
			qid = resolved
			if _, uerr := e.pool.Exec(ctx, `
				UPDATE authors
				SET ext_ids = jsonb_set(COALESCE(ext_ids, '{}'::jsonb), '{wd_qid}', to_jsonb($1::text))
				WHERE id = $2 AND COALESCE(ext_ids->>'wd_qid', '') = ''`, resolved, authorID); uerr != nil {
				e.logger.Warn("metadata: persist resolved wd_qid failed", "author_id", authorID, "err", uerr)
			}
		case errors.Is(err, ErrNotFound):
			// Честно не нашли писателя с таким именем — событий не будет,
			// маркер поставим (не долбим Wikidata на каждый заход).
		default:
			transient = true
			e.logger.Info("metadata: author qid resolve failed", "author_id", authorID, "err", err)
		}
	}

	var events []AuthorEvent
	if qid != "" && !transient {
		evs, err := e.wdEvents.FetchAuthorEvents(ctx, qid)
		if err != nil {
			transient = true
			e.logger.Info("metadata: author events fetch failed", "author_id", authorID, "qid", qid, "err", err)
		} else {
			events = evs
		}
	}
	if transient {
		return // ретрай следующим lazy-заходом/воркером — маркер не ставим
	}
	if err := e.saveAuthorEvents(ctx, authorID, events); err != nil {
		e.logger.Warn("metadata: save author events failed", "author_id", authorID, "err", err)
		return
	}
	e.logger.Info("metadata: author events saved", "author_id", authorID, "count", len(events))
}

// saveAuthorEvents — транзакционный upsert событий + чистка выпавших +
// маркер. Ключевые инварианты:
//   - hidden ПЕРЕЖИВАЕТ refetch (курирование админа: ON CONFLICT НЕ трогает
//     hidden, DELETE выпавших не касается hidden=true строк);
//   - идемпотентность по UNIQUE(author_id, source, ext_key).
func (e *Enricher) saveAuthorEvents(ctx context.Context, authorID int64, events []AuthorEvent) error {
	tx, err := e.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	keysBySource := map[string][]string{}
	for _, ev := range events {
		if _, err := tx.Exec(ctx, `
			INSERT INTO author_events
				(author_id, source, ext_key, event_type, year_from, year_to,
				 date_from, date_precision, title, quote, place, url, weight)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),NULLIF($11,''),NULLIF($12,''),$13)
			ON CONFLICT (author_id, source, ext_key) DO UPDATE SET
				event_type = EXCLUDED.event_type,
				year_from = EXCLUDED.year_from,
				year_to = EXCLUDED.year_to,
				date_from = EXCLUDED.date_from,
				date_precision = EXCLUDED.date_precision,
				title = EXCLUDED.title,
				quote = COALESCE(EXCLUDED.quote, author_events.quote),
				place = EXCLUDED.place,
				url = EXCLUDED.url,
				weight = EXCLUDED.weight
				-- hidden сознательно НЕ трогаем: скрытие переживает refetch
		`, authorID, ev.Source, ev.ExtKey, ev.Type, ev.YearFrom, ev.YearTo,
			ev.DateFrom, ev.DatePrecision, ev.Title, ev.Quote, ev.Place, ev.URL, ev.Weight); err != nil {
			return fmt.Errorf("upsert event %s: %w", ev.ExtKey, err)
		}
		keysBySource[ev.Source] = append(keysBySource[ev.Source], ev.ExtKey)
	}
	// Выпавшие из нового набора не-hidden строки обработанных источников —
	// удалить (актуализация при изменении данных источника). hidden-строки
	// остаются: их admin скрыл сознательно, потерять пометку нельзя.
	for _, src := range []string{"wikidata"} {
		if _, err := tx.Exec(ctx, `
			DELETE FROM author_events
			WHERE author_id = $1 AND source = $2 AND hidden = false
			  AND NOT (ext_key = ANY($3::text[]))`,
			authorID, src, keysBySource[src]); err != nil {
			return fmt.Errorf("prune stale %s events: %w", src, err)
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE authors SET events_fetched_at = now() WHERE id = $1`, authorID); err != nil {
		return fmt.Errorf("mark events_fetched_at: %w", err)
	}
	return tx.Commit(ctx)
}
