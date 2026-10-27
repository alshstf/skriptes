package metadata

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// editionKeysBatch — QID на один SPARQL-запрос P629 (как у меток авторов).
const editionKeysBatch = 300

// RepairEditionWorkKeys — разовый догон #574. До 1.71.0 поиск книги в Wikidata
// принимал элемент-издание («2001 edition of The Godfather») за произведение, и
// QID издания осел ключом группировки (book_work_lookups) и QID работы
// (works.ext_ids.wd_qid): на проде 2026-10-27 — 1108 из 24,5 тыс. ключей, 1014
// работ. У издания нет экранизаций (P144 ссылается на произведение), его P577 —
// дата издания, а не первой публикации («The Vegetarian» — 2016 вместо 2007),
// sitelinks — свои, почти пустые.
//
// Что делает: QID изданий (P629 → ровно одно произведение; омнибусы не трогаем)
// заменяет QID произведения в ключах и работах; год изданий из Wikidata стирает
// вместе с попыткой (воркер года спросит произведение) и пересчитывает год работ;
// попытки известности из Wikidata стирает; изданиям без экранизаций сбрасывает
// маркер экранизаций, всем — work_scanned_at (группировка перечитает ключи).
// Возвращает работы с изменившимся годом (для works-индекса), число исправленных
// ключей и затронутых изданий. Сбой Wikidata — ошибка без записи: догон
// повторится целиком.
func RepairEditionWorkKeys(ctx context.Context, pool *pgxpool.Pool, p *WikidataAdaptationsProvider, rpm int) (changed []int64, editions int, books int64, err error) {
	rows, err := pool.Query(ctx, `
		SELECT work_key FROM book_work_lookups WHERE source = 'wikidata' AND outcome = 'found'
		UNION
		SELECT ext_ids->>'wd_qid' FROM works WHERE ext_ids ? 'wd_qid'`)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("edition keys: load: %w", err)
	}
	var qids []string
	for rows.Next() {
		var q string
		if err := rows.Scan(&q); err != nil {
			rows.Close()
			return nil, 0, 0, fmt.Errorf("edition keys: scan: %w", err)
		}
		if isQID(q) {
			qids = append(qids, q)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, 0, err
	}

	gate := &rateGate{}
	gate.setRPM(rpm)
	worksOf := map[string][]string{}
	for i := 0; i < len(qids); i += editionKeysBatch {
		if err := gate.wait(ctx); err != nil {
			return nil, 0, 0, err
		}
		batch := qids[i:min(i+editionKeysBatch, len(qids))]
		bindings, err := p.sparqlBindings(ctx, fmt.Sprintf(
			`SELECT ?e ?w WHERE { VALUES ?e { wd:%s } ?e wdt:P629 ?w }`, strings.Join(batch, " wd:")))
		if err != nil {
			return nil, 0, 0, fmt.Errorf("edition keys: P629: %w", err)
		}
		for _, b := range bindings {
			e, w := extractQID(b["e"]), extractQID(b["w"])
			if e != "" && w != "" && w != e {
				worksOf[e] = append(worksOf[e], w)
			}
		}
	}
	var eds, wks []string
	for e, ws := range worksOf {
		if len(ws) == 1 {
			eds, wks = append(eds, e), append(wks, ws[0])
		}
	}
	if len(eds) == 0 {
		return nil, 0, 0, nil
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, q := range []string{
		`CREATE TEMP TABLE ed_map ON COMMIT DROP AS
		   SELECT * FROM unnest($1::text[], $2::text[]) AS m(e, w)`,
		// Работы и издания, где QID издания — ключ или QID работы.
		`CREATE TEMP TABLE ed_works ON COMMIT DROP AS
		   SELECT w.id FROM works w JOIN ed_map m ON m.e = w.ext_ids->>'wd_qid'`,
		`CREATE TEMP TABLE ed_books ON COMMIT DROP AS
		   SELECT l.book_id AS id FROM book_work_lookups l JOIN ed_map m ON m.e = l.work_key WHERE l.source = 'wikidata'
		   UNION
		   SELECT b.id FROM books b JOIN ed_works w ON w.id = b.work_id`,
	} {
		args := []any{}
		if strings.Contains(q, "$1") {
			args = []any{eds, wks}
		}
		if _, err := tx.Exec(ctx, q, args...); err != nil {
			return nil, 0, 0, fmt.Errorf("edition keys: prepare: %w", err)
		}
	}
	for _, q := range []string{
		`UPDATE book_work_lookups l SET work_key = m.w FROM ed_map m WHERE l.source = 'wikidata' AND l.work_key = m.e`,
		`UPDATE works w SET ext_ids = jsonb_set(w.ext_ids, '{wd_qid}', to_jsonb(m.w)) FROM ed_map m WHERE w.ext_ids->>'wd_qid' = m.e`,
		`UPDATE books SET written_year = NULL, written_year_source = NULL
		   WHERE id IN (SELECT id FROM ed_books) AND written_year_source = 'wikidata'`,
		`DELETE FROM book_year_lookups WHERE source = 'wikidata' AND book_id IN (SELECT id FROM ed_books)`,
		`DELETE FROM work_renown_lookups WHERE source = 'wikidata' AND work_id IN (SELECT id FROM ed_works)`,
		`UPDATE books b SET adaptations_fetched_at = NULL
		   WHERE b.id IN (SELECT id FROM ed_books) AND b.adaptations_fetched_at IS NOT NULL
		     AND NOT EXISTS (SELECT 1 FROM book_adaptations x WHERE x.book_id = b.id)`,
	} {
		if _, err := tx.Exec(ctx, q); err != nil {
			return nil, 0, 0, fmt.Errorf("edition keys: repair: %w", err)
		}
	}
	tag, err := tx.Exec(ctx, `UPDATE books SET work_scanned_at = NULL WHERE id IN (SELECT id FROM ed_books) AND deleted = false`)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("edition keys: rescan: %w", err)
	}
	books = tag.RowsAffected()
	var workIDs []int64
	idRows, err := tx.Query(ctx, `SELECT DISTINCT b.work_id FROM books b JOIN ed_books e ON e.id = b.id WHERE b.work_id IS NOT NULL`)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("edition keys: works: %w", err)
	}
	for idRows.Next() {
		var id int64
		if err := idRows.Scan(&id); err != nil {
			idRows.Close()
			return nil, 0, 0, err
		}
		workIDs = append(workIDs, id)
	}
	idRows.Close()
	if err := idRows.Err(); err != nil {
		return nil, 0, 0, err
	}
	if len(workIDs) > 0 {
		if changed, err = recomputeWorkYears(ctx, tx, workIDs, false); err != nil {
			return nil, 0, 0, fmt.Errorf("edition keys: work years: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, 0, 0, err
	}
	return changed, len(eds), books, nil
}
