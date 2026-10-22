package metadata

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgxpool"
)

// adaptationRecheckLabelBatch — сколько QID авторов спрашивать одним SPARQL.
const adaptationRecheckLabelBatch = 300

// RecheckAdaptationsByAuthorQID — разовый перепроход промахов экранизаций после
// проверки автора книги по QID (#570). До неё кандидата-книгу подтверждали
// буквами имени (legacyAuthorNameMatches), и автор, которого это правило не
// находило в его же метках Wikidata («Херберт Фрэнк» против «Фрэнк Герберт»,
// псевдоним против настоящего имени), оставался без экранизаций навсегда —
// маркер single-shot. Только у книг таких авторов новая проверка что-то меняет:
// метки авторов с QID, у которых есть книги без экранизаций, спрашиваются у
// Wikidata (пачками по 300, не чаще rpm запросов в минуту), и у книг авторов,
// которых старое правило пропускало, сбрасывается adaptations_fetched_at —
// перепросят воркер «Экранизации» и открытие карточки. Прод 2026-10-23: 4,8
// тыс. авторов из 33,9 тыс., 32 тыс. книг; сброс по одному «автор с QID» задел
// бы 227 тыс., и в выборке 250 работ из него старое правило находило книгу у 129
// из 135 найденных. Сбой Wikidata — ошибка без сброса (всё или ничего): повторить
// позже. Возвращает число авторов и книг к перепросу.
func RecheckAdaptationsByAuthorQID(ctx context.Context, pool *pgxpool.Pool, p *WikidataAdaptationsProvider, rpm int) (authors int, books int64, err error) {
	rows, err := pool.Query(ctx, `
		SELECT a.id, TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name)), a.ext_ids->>'wd_qid'
		FROM authors a
		WHERE a.ext_ids ? 'wd_qid'
		  AND EXISTS (SELECT 1 FROM book_authors ba JOIN books b ON b.id = ba.book_id
		              WHERE ba.author_id = a.id AND b.deleted = false AND b.adaptations_fetched_at IS NOT NULL
		                AND NOT EXISTS (SELECT 1 FROM book_adaptations x WHERE x.book_id = b.id))
		ORDER BY a.id`)
	if err != nil {
		return 0, 0, fmt.Errorf("adaptations recheck: authors: %w", err)
	}
	type author struct {
		id        int64
		name, qid string
	}
	var cands []author
	seen := map[string]bool{}
	var qids []string
	for rows.Next() {
		var a author
		if err := rows.Scan(&a.id, &a.name, &a.qid); err != nil {
			rows.Close()
			return 0, 0, fmt.Errorf("adaptations recheck: scan: %w", err)
		}
		if !isQID(a.qid) {
			continue
		}
		cands = append(cands, a)
		if !seen[a.qid] {
			seen[a.qid] = true
			qids = append(qids, a.qid)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}

	gate := &rateGate{}
	gate.setRPM(rpm)
	labels := map[string][]string{}
	for i := 0; i < len(qids); i += adaptationRecheckLabelBatch {
		if err := gate.wait(ctx); err != nil {
			return 0, 0, err
		}
		got, err := p.authorLabels(ctx, qids[i:min(i+adaptationRecheckLabelBatch, len(qids))])
		if err != nil {
			return 0, 0, fmt.Errorf("adaptations recheck: labels: %w", err)
		}
		for q, ls := range got {
			labels[q] = ls
		}
	}

	var missed []int64
	for _, a := range cands {
		if !legacyAuthorNameMatches(a.name, labels[a.qid]) {
			missed = append(missed, a.id)
		}
	}
	if len(missed) == 0 {
		return 0, 0, nil
	}
	tag, err := pool.Exec(ctx, `
		UPDATE books b SET adaptations_fetched_at = NULL
		WHERE b.deleted = false AND b.adaptations_fetched_at IS NOT NULL
		  AND NOT EXISTS (SELECT 1 FROM book_adaptations x WHERE x.book_id = b.id)
		  AND EXISTS (SELECT 1 FROM book_authors ba WHERE ba.book_id = b.id AND ba.author_id = ANY($1))`, missed)
	if err != nil {
		return 0, 0, fmt.Errorf("adaptations recheck: reset marker: %w", err)
	}
	return len(missed), tag.RowsAffected(), nil
}

// authorLabels — метки rdfs:label авторов на ru и en: по ним до #570
// подтверждали книгу (RecheckAdaptationsByAuthorQID ищет, кого это пропускало).
func (p *WikidataAdaptationsProvider) authorLabels(ctx context.Context, qids []string) (map[string][]string, error) {
	bindings, err := p.sparqlBindings(ctx, fmt.Sprintf(
		`SELECT ?a ?l WHERE { VALUES ?a { wd:%s } ?a rdfs:label ?l . FILTER(LANG(?l) IN ("ru","en")) }`,
		strings.Join(qids, " wd:")))
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, b := range bindings {
		if q := extractQID(b["a"]); q != "" && b["l"] != "" {
			out[q] = append(out[q], b["l"])
		}
	}
	return out, nil
}

// legacyAuthorNameMatches — правило подтверждения автора до 1.64.0: ≥ 2 слов
// имени (или все, если слово одно; без инициалов) подстрокой в метке, без
// транслитерации. Нужно только перепроходу — найти авторов, которых оно пропускало.
func legacyAuthorNameMatches(name string, labels []string) bool {
	var words []string
	for _, w := range strings.Fields(strings.NewReplacer(",", " ", ".", " ", "_", " ").Replace(strings.ToLower(name))) {
		if utf8.RuneCountInString(w) >= 2 {
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		return false
	}
	needed := min(2, len(words))
	for _, l := range labels {
		low, n := strings.ToLower(l), 0
		for _, w := range words {
			if strings.Contains(low, w) {
				n++
			}
		}
		if n >= needed {
			return true
		}
	}
	return false
}
