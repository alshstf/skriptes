package metadata

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Дата выхода экранизации (#581, миграция 0070). SPARQL экранизаций берёт из
// P577 только год (wdt:, без точности): по нему подборка «Экранизации этого и
// следующего года» не могла ни сортировать по дате, ни писать «1 мая 2026».
// Даты нужны лишь будущим фильмам — их немного (прод 2026-10-23: 38 фильмов на
// 2026–2027), и они переносятся, поэтому это отдельная фаза воркера
// «Экранизации» с TTL на фильм, а не поле основного SPARQL (там P577 у фильмов
// — по дате на страну: строк кратно больше, LIMIT 200 срезал бы фильмы).

const (
	// releaseDateTTL — как часто перепроверять дату одного фильма.
	releaseDateTTL = 3 * 24 * time.Hour
	// releaseDateBatch — фильмов в одном SPARQL (VALUES) и за цикл воркера.
	releaseDateBatch = 200
	// releaseDateYearsAhead — окно: этот год … через столько лет (подборка —
	// этот и следующий; запас — чтобы к новому году даты уже были).
	releaseDateYearsAhead = 2

	precisionMonth = 10 // wikibase:timePrecision — месяц
	precisionDay   = 11 // день
)

// ReleaseDate — дата выхода с точностью: день или месяц (у месяца On — 1-е число).
type ReleaseDate struct {
	On        time.Time
	Precision int
}

// releaseValue — одно значение P577 фильма (премьера на фестивале, прокат в стране…).
type releaseValue struct {
	on        time.Time
	precision int
}

// pickReleaseDate — дата выхода фильма из значений P577 в пределах его года
// (book_adaptations.year): у фильма их по стране и фестивалю, и самое раннее
// часто — фестиваль прошлого года («Скотный двор» Серкиса: Анси 9 июня 2025,
// прокат 1 мая 2026, год в базе 2026). Внутри года — самая точная, затем самая
// ранняя; точность «год» — не дата (false).
func pickReleaseDate(vals []releaseValue, year int) (ReleaseDate, bool) {
	var best *releaseValue
	for i := range vals {
		v := &vals[i]
		if v.on.Year() != year || v.precision < precisionMonth {
			continue
		}
		p := min(v.precision, precisionDay)
		if best == nil || p > best.precision || (p == best.precision && v.on.Before(best.on)) {
			best = &releaseValue{on: v.on, precision: p}
		}
	}
	if best == nil {
		return ReleaseDate{}, false
	}
	on := time.Date(best.on.Year(), best.on.Month(), best.on.Day(), 0, 0, 0, 0, time.UTC)
	if best.precision == precisionMonth {
		on = time.Date(on.Year(), on.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	return ReleaseDate{On: on, Precision: best.precision}, true
}

// releaseDates — значения P577 фильмов (не deprecated) с точностью, одним SPARQL.
func (p *WikidataAdaptationsProvider) releaseDates(ctx context.Context, qids []string) (map[string][]releaseValue, error) {
	bindings, err := p.sparqlBindings(ctx, fmt.Sprintf(`SELECT ?film ?date ?prec WHERE {
  VALUES ?film { wd:%s }
  ?film p:P577 ?st .
  ?st wikibase:rank ?rank . FILTER(?rank != wikibase:DeprecatedRank)
  ?st psv:P577 ?v .
  ?v wikibase:timeValue ?date ; wikibase:timePrecision ?prec .
}`, strings.Join(qids, " wd:")))
	if err != nil {
		return nil, err
	}
	out := make(map[string][]releaseValue, len(qids))
	for _, b := range bindings {
		q := extractQID(b["film"])
		on, err := time.Parse(time.RFC3339, b["date"])
		prec, perr := strconv.Atoi(b["prec"])
		if q == "" || err != nil || perr != nil {
			continue // даты до н. э. и прочие нестандартные значения — мимо
		}
		out[q] = append(out[q], releaseValue{on: on.UTC(), precision: prec})
	}
	return out, nil
}

// wikidataAdaptations — провайдер экранизаций Wikidata среди подключённых (nil — нет).
func (e *Enricher) wikidataAdaptations() *WikidataAdaptationsProvider {
	for _, p := range e.adaptationProviders {
		if w, ok := p.(*WikidataAdaptationsProvider); ok {
			return w
		}
	}
	return nil
}

// RefreshReleaseDates — фаза воркера «Экранизации»: даты выхода фильмов этого
// года и на releaseDateYearsAhead вперёд, которых пора спросить (TTL на фильм,
// давно не спрошенные — первыми). Дата пишется на все строки фильма; нет даты
// в пределах его года — NULL, следующая попытка через TTL. Сбой Wikidata —
// checked_at не трогаем (ретрай в следующем цикле). Возвращает (сколько фильмов
// проверено, у скольких есть дата).
func (e *Enricher) RefreshReleaseDates(ctx context.Context, now time.Time) (int, int, error) {
	p := e.wikidataAdaptations()
	if p == nil || e.pool == nil {
		return 0, 0, nil
	}
	return refreshReleaseDates(ctx, e.pool, p, now)
}

func refreshReleaseDates(ctx context.Context, pool *pgxpool.Pool, p *WikidataAdaptationsProvider, now time.Time) (int, int, error) {
	rows, err := pool.Query(ctx, `
		SELECT ext_id, min(year)::int
		FROM book_adaptations
		WHERE provider = 'wikidata' AND year BETWEEN $1 AND $2
		GROUP BY ext_id
		HAVING bool_or(released_checked_at IS NULL OR released_checked_at < $3)
		ORDER BY min(released_checked_at) NULLS FIRST, ext_id
		LIMIT $4`, now.Year(), now.Year()+releaseDateYearsAhead, now.Add(-releaseDateTTL), releaseDateBatch)
	if err != nil {
		return 0, 0, fmt.Errorf("release dates: candidates: %w", err)
	}
	years := map[string]int{}
	var qids []string
	for rows.Next() {
		var (
			q string
			y int
		)
		if err := rows.Scan(&q, &y); err != nil {
			rows.Close()
			return 0, 0, err
		}
		if isQID(q) {
			years[q] = y
			qids = append(qids, q)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	if len(qids) == 0 {
		return 0, 0, nil
	}
	vals, err := p.releaseDates(ctx, qids)
	if errors.Is(err, ErrSourcePaused) {
		return 0, 0, nil // Wikidata на паузе (#299) — до следующего цикла
	}
	if err != nil {
		return 0, 0, fmt.Errorf("release dates: sparql: %w", err)
	}
	ons := make([]*time.Time, len(qids))
	precs := make([]*int16, len(qids))
	dated := 0
	for i, q := range qids {
		if d, ok := pickReleaseDate(vals[q], years[q]); ok {
			on, pr := d.On, int16(precisionDay)
			if d.Precision == precisionMonth {
				pr = precisionMonth
			}
			ons[i], precs[i] = &on, &pr
			dated++
		}
	}
	if _, err := pool.Exec(ctx, `
		UPDATE book_adaptations ba
		SET released_on = u.rel_on, released_precision = u.rel_prec, released_checked_at = now()
		FROM unnest($1::text[], $2::date[], $3::smallint[]) AS u(ext_id, rel_on, rel_prec)
		WHERE ba.provider = 'wikidata' AND ba.ext_id = u.ext_id`, qids, ons, precs); err != nil {
		return 0, 0, fmt.Errorf("release dates: update: %w", err)
	}
	return len(qids), dated, nil
}
