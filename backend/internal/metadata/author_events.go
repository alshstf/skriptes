package metadata

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// EnsureAuthorEvents — события биографии автора (био-таймлайн, план
// cryptic-roaming-turing). Single-shot по authors.events_fetched_at, зеркало
// EnsureAdaptations: inflight-lock, guard, транзиент не ставит маркер
// (грабля №20). Гейт по renown — таймлайн только для «ядра» (авторы с
// внешней известностью; на проде ~19k из 138k): у безвестных авторов
// событий всё равно нет, а бюджет WDQS не резиновый.
//
// Два источника: Wikidata-скелет (обязательный) + Wikipedia-вехи из текста
// биографии (опционально, WithAuthorEventsWiki).
func (e *Enricher) WithAuthorEvents(p *WikidataEventsProvider, occupationGate func(ctx context.Context, qid string) (OccupationVerdict, error)) *Enricher {
	e.wdEvents = p
	e.eventsQIDGate = occupationGate
	return e
}

// WithAuthorEventsWiki — Wikipedia-экстрактор вех (rule-based «предложение с
// годом» по биозоне статьи; статья резолвится sitelink'ом QID, не поиском по
// имени). Обычно тот же инстанс WikipediaProvider, что и у bio-пути.
func (e *Enricher) WithAuthorEventsWiki(p *WikipediaProvider) *Enricher {
	e.wikiEvents = p
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
		renown        int64
		fetchedAt     *time.Time
		qid           string
		lastN, firstN string
		middleN       string
	)
	if err := e.pool.QueryRow(ctx, `
		SELECT renown, events_fetched_at, COALESCE(ext_ids->>'wd_qid', ''),
		       COALESCE(last_name,''), COALESCE(first_name,''), COALESCE(middle_name,'')
		FROM authors WHERE id = $1`, authorID,
	).Scan(&renown, &fetchedAt, &qid, &lastN, &firstN, &middleN); err != nil {
		e.logger.Warn("metadata: query author for events failed", "author_id", authorID, "err", err)
		return
	}
	if renown == 0 || fetchedAt != nil {
		return // не ядро / уже пробовали
	}

	transient := false
	// QID: приоритет — bio-derived из ext_ids (прошёл имя+P106 гейты);
	// фолбэк — резолв по имени с occupation-гейтом. Два порядка имени:
	// wbsearchentities матчит лейблы, а они в естественном порядке — по
	// «Иггульден Конн» (наш «Фамилия Имя») поиск даёт 0, по «Конн Иггульден»
	// находит (живой замер 2026-07-19).
	if qid == "" {
		names := []string{strings.TrimSpace(strings.Join([]string{lastN, firstN, middleN}, " "))}
		if firstN != "" && lastN != "" {
			names = append(names, strings.TrimSpace(firstN+" "+lastN))
		}
		for _, name := range names {
			resolved, err := e.wdEvents.ResolveAuthorQID(ctx, name, e.eventsQIDGate)
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
				// Честно не нашли — пробуем следующий порядок имени; после
				// последнего маркер ставится (не долбим Wikidata на каждый заход).
				continue
			default:
				transient = true
				e.logger.Info("metadata: author qid resolve failed", "author_id", authorID, "err", err)
			}
			break
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
	// Wikipedia-вехи: биозона статьи (по sitelink QID) → предложения с годом.
	// Транзиент любого шага = маркер не ставим (следующая попытка добьёт);
	// честное отсутствие статьи/вех — не транзиент, скелет сохраняем как есть.
	if qid != "" && !transient && e.wikiEvents != nil {
		wiki, werr := e.fetchWikiMilestones(ctx, qid, events)
		if werr != nil {
			transient = true
			e.logger.Info("metadata: wiki milestones fetch failed", "author_id", authorID, "qid", qid, "err", werr)
		} else {
			events = mergeAuthorEvents(events, wiki)
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

// fetchWikiMilestones — статья через sitelink QID (ru приоритетно, затем en —
// порядок как у bio), годы жизни для lifespan-фильтра — из Wikidata-скелета.
// Возвращает (nil, nil) при честном отсутствии статьи или вех; ошибка =
// транзиент (не ставить маркер).
func (e *Enricher) fetchWikiMilestones(ctx context.Context, qid string, wdEvents []AuthorEvent) ([]AuthorEvent, error) {
	links, err := e.wdEvents.FetchSitelinks(ctx, qid)
	if err != nil {
		return nil, err
	}
	birth, death := 0, 0
	for _, ev := range wdEvents {
		switch ev.Type {
		case EventBirth:
			birth = ev.YearFrom
		case EventDeath:
			death = ev.YearFrom
		}
	}
	for _, lang := range []string{"ru", "en"} {
		title := links[lang]
		if title == "" {
			continue
		}
		milestones, merr := e.wikiEvents.FetchAuthorMilestones(ctx, lang, title, birth, death)
		if errors.Is(merr, ErrNotFound) {
			continue // статья-призрак — пробуем следующий язык
		}
		if merr != nil {
			return nil, merr
		}
		return milestones, nil // первая статья с текстом; дедуп ×2 — сознательно нет
	}
	return nil, nil
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
	for _, src := range []string{"wikidata", "wikipedia"} {
		keys := keysBySource[src]
		if keys == nil {
			// nil-слайс pgx кодирует NULL-массивом → NOT(x=ANY(NULL)) = NULL →
			// prune молча не удалял бы ничего; пустой источник = чистим всё
			// не-hidden (актуализация).
			keys = []string{}
		}
		if _, err := tx.Exec(ctx, `
			DELETE FROM author_events
			WHERE author_id = $1 AND source = $2 AND hidden = false
			  AND NOT (ext_key = ANY($3::text[]))`,
			authorID, src, keys); err != nil {
			return fmt.Errorf("prune stale %s events: %w", src, err)
		}
	}
	if _, err := tx.Exec(ctx,
		`UPDATE authors SET events_fetched_at = now() WHERE id = $1`, authorID); err != nil {
		return fmt.Errorf("mark events_fetched_at: %w", err)
	}
	return tx.Commit(ctx)
}
