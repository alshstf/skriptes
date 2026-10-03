package books

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/meilisearch/meilisearch-go"

	"github.com/skriptes/skriptes/backend/internal/textnorm"
)

// Запрос по имени автора (#290). Правило ранжирования attribute ставит
// совпадение в названии выше совпадения в авторе, поэтому «толстой» выдавал
// сначала книги о Толстом («Лев Толстой: Бегство из рая»), а «Война и мир» была
// 105-й; «пушкин» → «Евгений Онегин» 404-й. Если запрос — имя известного автора,
// выдача идёт двумя частями: сначала его работы (тот же запрос с фильтром
// author_ids), потом всё остальное (фильтр NOT). Каждая часть — в порядке Meili,
// страницы стыкуются без потерь и повторов; общее число и фасеты — по всему
// запросу. Над выдачей — плашка автора (MatchedAuthors).
//
// Не автор, а книга: если среди первых результатов есть книга другого автора с
// точно таким названием, известнее самого автора («Кармен» Мериме при авторе
// Кармен, «Дар» Набокова при авторе Дар), — обычный поиск.

// authorQueryMinRenown — порог известности автора (authors.renown): ниже —
// обычный поиск. Это ~11 тыс. авторов из 140 тыс.; у безвестных тёзок слово
// чаще оказывается названием книги, а не именем.
const authorQueryMinRenown = 150

// authorQueryMaxWords — запрос длиннее не считается именем (ФИО — до трёх слов).
const authorQueryMaxWords = 3

// titleProbeHits — сколько первых результатов всего запроса проверять на книгу с
// точно таким названием (заодно этот запрос даёт общее число и фасеты).
const titleProbeHits = 5

// maxMatchedAuthors — сколько авторов показывать на плашке.
const maxMatchedAuthors = 3

// matchedRenownShare — из тёзок по запросу берутся известные хотя бы на треть от
// самого известного: «кинг» — Стивен (2408) и Оуэн (944), без Дэнни (680),
// Джона (590) и Росса (488); «толстой» — Лев и Алексей Николаевич.
const matchedRenownShare = 3

// MatchedAuthor — автор, на имя которого похож запрос (плашка над выдачей /books).
type MatchedAuthor struct {
	ID        int64  `json:"id"`
	FullName  string `json:"full_name"`
	Note      string `json:"note,omitempty"`
	BookCount int    `json:"book_count"` // работ автора (authors.book_count, без сборников)
	renown    int64
}

// nameParticles — частицы фамилии, без которых она узнаётся («гуин» = «Ле Гуин»
// не считаем: частица часть фамилии, но и без неё запрос не обязан её писать).
var nameParticles = map[string]bool{
	"де": true, "ле": true, "ла": true, "фон": true, "ван": true, "дер": true,
	"да": true, "ди": true, "дю": true, "ибн": true, "бен": true, "аль": true, "эль": true,
}

// queryWords — слова запроса для сравнения с именем: нижний регистр, «ё»→«е»,
// знаки препинания по краям слов отброшены.
func queryWords(q string) []string {
	fields := strings.Fields(strings.ToLower(textnorm.FoldYo(q)))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.Trim(f, `.,;:!?"'«»()`)
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// nameCoversQuery — запрос называет автора: каждое слово запроса — слово его
// ФИО, и все слова фамилии (кроме частиц) в запросе есть. «толстой», «лев
// толстой», «толстой лев николаевич» — да; «лев», «толстой война» — нет.
func nameCoversQuery(last, first, middle string, words []string) bool {
	if len(words) == 0 || len(words) > authorQueryMaxWords {
		return false
	}
	lastWords := queryWords(last)
	nameWords := append(append(append([]string(nil), lastWords...), queryWords(first)...), queryWords(middle)...)
	for _, w := range words {
		if !slices.Contains(nameWords, w) {
			return false
		}
	}
	hasLast := false
	for _, lw := range lastWords {
		if nameParticles[lw] {
			continue
		}
		if !slices.Contains(words, lw) {
			return false
		}
		hasLast = true
	}
	return hasLast
}

// latinCoversQuery — запрос называет автора латиницей (authors.latin_name из fb2
// переводов, «doyle arthur conan»): каждое слово запроса — слово латинского
// имени, и фамилия (первое слово) среди них. «doyle», «arthur conan doyle» — да.
func latinCoversQuery(latin string, words []string) bool {
	lw := strings.Fields(latin)
	if len(lw) == 0 || len(words) == 0 || len(words) > authorQueryMaxWords || !slices.Contains(words, lw[0]) {
		return false
	}
	for _, w := range words {
		if !slices.Contains(lw, w) {
			return false
		}
	}
	return true
}

// matchQueryAuthors — известные авторы, которых называет запрос, по убыванию
// известности. Кандидатов отбирают trigram-индексы по свёрнутому имени
// (миграция 0042) и по латинскому имени (0046) — по самому длинному слову
// запроса; точное правило — nameCoversQuery или latinCoversQuery.
func (s *Service) matchQueryAuthors(ctx context.Context, query string) ([]MatchedAuthor, error) {
	words := queryWords(query)
	if s.pool == nil || len(words) == 0 || len(words) > authorQueryMaxWords {
		return nil, nil
	}
	longest := ""
	for _, w := range words {
		if len([]rune(w)) > len([]rune(longest)) {
			longest = w
		}
	}
	if len([]rune(longest)) < 3 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.last_name, COALESCE(a.first_name, ''), COALESCE(a.middle_name, ''),
		       COALESCE(a.name_note, ''), a.book_count, a.renown, COALESCE(a.latin_name, '')
		FROM authors a
		WHERE NOT a.is_service AND a.renown >= $2 AND a.book_count > 0
		  AND (replace(a.normalized_name::text, 'ё', 'е') ILIKE '%' || $1 || '%' ESCAPE '\'
		       OR a.latin_name ILIKE '%' || $1 || '%' ESCAPE '\')
		ORDER BY a.renown DESC, a.id
		LIMIT 20`, escapeLikePattern(longest), authorQueryMinRenown)
	if err != nil {
		return nil, fmt.Errorf("match query authors: %w", err)
	}
	defer rows.Close()
	var out []MatchedAuthor
	for rows.Next() {
		var (
			m                          MatchedAuthor
			last, first, middle, latin string
		)
		if err := rows.Scan(&m.ID, &last, &first, &middle, &m.Note, &m.BookCount, &m.renown, &latin); err != nil {
			return nil, err
		}
		if !nameCoversQuery(last, first, middle, words) && !latinCoversQuery(latin, words) {
			continue
		}
		if len(out) > 0 && m.renown*matchedRenownShare < out[0].renown {
			break // дальше по убыванию известности — только менее известные
		}
		m.FullName = fullName(AuthorRef{LastName: last, FirstName: first, MiddleName: middle})
		m.Note = DisplayNote(m.Note)
		out = append(out, m)
	}
	return out, rows.Err()
}

// escapeLikePattern экранирует спецсимволы LIKE — слово ищется как текст.
func escapeLikePattern(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// authorQueryEligible — можно ли делить выдачу на «работы автора» и остальное:
// поиск с запросом, порядок по релевантности, без фильтра по автору или серии и
// страница с границы окна (сегменты листаются постранично).
func authorQueryEligible(p ListParams, offset, limit int) bool {
	return strings.TrimSpace(p.Query) != "" && p.Sort == "" && p.AuthorID == 0 && p.SeriesID == 0 &&
		limit > 0 && offset%limit == 0
}

// titleOutranksAuthors — среди первых результатов всего запроса есть книга не
// этих авторов с точно таким названием и известнее любого из них: запрос — это
// книга («Кармен», «Дар»), а не автор.
func titleOutranksAuthors(hits meilisearch.Hits, query string, authors []MatchedAuthor) bool {
	q := strings.Join(queryWords(query), " ")
	var maxRenown int64
	ids := make([]int64, 0, len(authors))
	for _, a := range authors {
		maxRenown = max(maxRenown, a.renown)
		ids = append(ids, a.ID)
	}
	for _, h := range hits {
		var wh workHit
		if err := h.DecodeInto(&wh); err != nil {
			continue
		}
		if strings.Join(queryWords(wh.Title), " ") != q {
			continue
		}
		if slices.ContainsFunc(wh.AuthorIDs, func(id int64) bool { return slices.Contains(ids, id) }) {
			continue
		}
		if wh.Popularity > maxRenown {
			return true
		}
	}
	return false
}

// authorIDsFilter — Meili-фильтр «работа одного из авторов».
func authorIDsFilter(authors []MatchedAuthor) string {
	parts := make([]string, 0, len(authors))
	for _, a := range authors {
		parts = append(parts, strconv.FormatInt(a.ID, 10))
	}
	return "author_ids IN [" + strings.Join(parts, ", ") + "]"
}

// andFilter — соединить непустые части фильтра через AND.
func andFilter(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, "("+p+")")
		}
	}
	return strings.Join(out, " AND ")
}

// listWorksByAuthor — выдача «сначала работы автора» (см. doc файла). ok=false —
// запрос не про автора (или известнее книга с таким названием): нужен обычный поиск.
func (s *Service) listWorksByAuthor(ctx context.Context, params ListParams, offset, limit int, rerank bool, visibleLangs []string) (ListResponse, bool, error) {
	authors, err := s.matchQueryAuthors(ctx, params.Query)
	if err != nil || len(authors) == 0 {
		return ListResponse{}, false, nil // сбой PG — не повод ронять поиск
	}
	index := s.meili.Index(worksIndexName)
	q := textnorm.FoldYo(params.Query)
	base := buildWorksFilter(params, visibleLangs)

	// Весь запрос: общее число и фасеты (как у обычного поиска) и проверка, не
	// книга ли это с таким названием.
	all := &meilisearch.SearchRequest{MatchingStrategy: meilisearch.All, HitsPerPage: titleProbeHits, Page: 1}
	if base != "" {
		all.Filter = base
	}
	if len(params.Facets) > 0 {
		all.Facets = params.Facets
	}
	resAll, err := index.SearchWithContext(ctx, q, all)
	if err != nil {
		return ListResponse{}, false, fmt.Errorf("meili works search: %w", err)
	}
	if titleOutranksAuthors(resAll.Hits, params.Query, authors) {
		return ListResponse{}, false, nil
	}

	byAuthor := authorIDsFilter(authors)
	// Работы автора: страница в режиме Page — точное число работ автора.
	reqA := &meilisearch.SearchRequest{
		MatchingStrategy: meilisearch.All, ShowRankingScore: rerank,
		HitsPerPage: int64(limit), Page: int64(offset/limit) + 1,
		Filter: andFilter(base, byAuthor),
	}
	resA, err := index.SearchWithContext(ctx, q, reqA)
	if err != nil {
		return ListResponse{}, false, fmt.Errorf("meili works search (author): %w", err)
	}
	segA := scoreWorkHits(resA.Hits, rerank)
	var segB []scoredItem
	if len(segA) < limit {
		// Остальное: продолжаем с того места, где кончились работы автора.
		reqB := &meilisearch.SearchRequest{
			MatchingStrategy: meilisearch.All, ShowRankingScore: rerank,
			Limit: int64(limit - len(segA)), Offset: int64(max(0, offset-int(resA.TotalHits))),
			Filter: andFilter(base, "NOT "+byAuthor),
		}
		resB, err := index.SearchWithContext(ctx, q, reqB)
		if err != nil {
			return ListResponse{}, false, fmt.Errorf("meili works search (rest): %w", err)
		}
		segB = scoreWorkHits(resB.Hits, rerank)
	}
	if rerank {
		// Пересортировка — внутри каждой части: работы автора остаются первыми.
		s.rerankScored(ctx, params.UserID, segA)
		s.rerankScored(ctx, params.UserID, segB)
	}
	items := make([]ListItem, 0, len(segA)+len(segB))
	for _, sc := range append(segA, segB...) {
		items = append(items, sc.item)
	}
	HydrateListMeta(ctx, s.pool, items)
	s.hydrateWorkRepresentative(ctx, items, params.ExcludeGenres, params.ExcludeLangs)

	resp := ListResponse{
		Items:       items,
		Total:       resAll.TotalHits,
		Limit:       limit,
		Offset:      offset,
		Query:       params.Query,
		ProcessTime: resAll.ProcessingTimeMs + resA.ProcessingTimeMs,
		Facets:      decodeFacets(resAll.FacetDistribution),
	}
	if offset == 0 {
		resp.MatchedAuthors = authors[:min(len(authors), maxMatchedAuthors)]
	}
	return resp, true, nil
}

// scoreWorkHits — хиты works-индекса в scoredItem (базовый score Meili — при rerank).
func scoreWorkHits(hits meilisearch.Hits, rerank bool) []scoredItem {
	out := make([]scoredItem, 0, len(hits))
	for _, h := range hits {
		var wh workHit
		if err := h.DecodeInto(&wh); err != nil {
			continue
		}
		score := 0.0
		if rerank {
			if raw, ok := h["_rankingScore"]; ok && len(raw) > 0 {
				_ = json.Unmarshal(raw, &score)
			}
		}
		out = append(out, scoredItem{item: wh.toListItem(), base: score, pop: popularityBoost(wh.Popularity)})
	}
	return out
}
