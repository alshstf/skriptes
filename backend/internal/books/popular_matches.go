package books

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/meilisearch/meilisearch-go"
	"github.com/skriptes/skriptes/backend/internal/textnorm"
)

// «Известные совпадения» (#401). Meili ставит выше названия, равные запросу
// целиком (правило exactness): на «мастер» окно подсказок и первая страница
// /books — это десятки книг «Мастер», а «Мастер и Маргарита» (известность 1725)
// туда не попадает и пересортировке поднимать нечего. Второй запрос — тот же
// текст по известности: правило sort стоит раньше exactness, длинные известные
// названия выходят наверх.

// popularitySort — порядок второго запроса.
var popularitySort = []string{"popularity:desc"}

const (
	// popularSuggestHits — сколько известных совпадений добавить в окно подсказок.
	popularSuggestHits = 8
	// pinCandidates — кандидатов на закрепление в /books (до проверки слов).
	pinCandidates = 10
	// maxPinned — сколько закреплять наверху первой страницы /books (решение
	// владельца 2026-10-06).
	maxPinned = 3
	// pinProbeHits — первые хиты по релевантности, с которыми сравнивается
	// известность кандидата; заодно этот запрос даёт точное число и фасеты.
	pinProbeHits = 5
	// pinMinPopularity — не закреплять малоизвестное.
	pinMinPopularity = 400
)

// pickPinned — какие известные совпадения закрепить наверху /books (до
// maxPinned, по известности): каждое слово запроса — целое слово названия
// (по началу слова Meili находит и «Морелла» на «море», и «Дары волхвов» на
// «дар»), известность от pinMinPopularity, не меньше двух лучших из первых
// результатов по релевантности и трети самого известного кандидата. Подобрано
// на запросах прода 2026-10-06: «мастер» — «Мастер и Маргарита», «остров» —
// «Остров сокровищ», «Остров погибших кораблей», «Остров доктора Моро»;
// «идиот», «гарри поттер», «дюна» — ничего (известное и так наверху).
func pickPinned(popular, relevance []workHit, query string) []workHit {
	words := letterWords(query)
	if len(words) == 0 {
		return nil
	}
	var topRel int64
	for i, h := range relevance {
		if i >= pinProbeHits {
			break
		}
		topRel = max(topRel, h.Popularity)
	}
	var out []workHit
	for _, h := range popular {
		if len(out) >= maxPinned || h.Popularity < pinMinPopularity || h.Popularity < 2*topRel {
			break // отсортированы по известности — дальше только меньше
		}
		if len(out) > 0 && h.Popularity*3 < out[0].Popularity {
			break
		}
		if !titleHasWords(h.Title, words) {
			continue
		}
		out = append(out, h)
	}
	return out
}

// letterWords — слова строки: нижний регистр, ё → е, разделитель — всё, кроме
// букв и цифр («Мир-Кольцо» → мир, кольцо).
func letterWords(s string) []string {
	return strings.FieldsFunc(strings.ToLower(textnorm.FoldYo(s)), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// titleHasWords — каждое слово запроса есть в названии целым словом.
func titleHasWords(title string, words []string) bool {
	have := map[string]bool{}
	for _, w := range letterWords(title) {
		have[w] = true
	}
	for _, w := range words {
		if !have[w] {
			return false
		}
	}
	return true
}

func decodeWorkHits(hits meilisearch.Hits) []workHit {
	out := make([]workHit, 0, len(hits))
	for _, h := range hits {
		var wh workHit
		if err := h.DecodeInto(&wh); err == nil {
			out = append(out, wh)
		}
	}
	return out
}

// idsExclusion — фильтр «кроме этих работ».
func idsExclusion(hits []workHit) string {
	ids := make([]string, 0, len(hits))
	for _, h := range hits {
		ids = append(ids, strconv.FormatInt(h.ID, 10))
	}
	return "NOT id IN [" + strings.Join(ids, ", ") + "]"
}

// mergeHits — хиты основного запроса и известных совпадений без повторов.
func mergeHits(main, extra meilisearch.Hits) meilisearch.Hits {
	seen := map[int64]bool{}
	for _, wh := range decodeWorkHits(main) {
		seen[wh.ID] = true
	}
	out := append(meilisearch.Hits{}, main...)
	for _, h := range extra {
		var wh workHit
		if err := h.DecodeInto(&wh); err != nil || seen[wh.ID] {
			continue
		}
		seen[wh.ID] = true
		out = append(out, h)
	}
	return out
}

// wholeWordHits — хиты, в названии которых каждое слово запроса — целое слово.
// Второй запрос находит и по началу слова: без этого на «дар» в подсказки
// попадали «Дары волхвов» выше «Дара» Набокова, на «море» — «Морелла».
func wholeWordHits(hits meilisearch.Hits, query string) meilisearch.Hits {
	words := letterWords(query)
	out := make(meilisearch.Hits, 0, len(hits))
	for _, h := range hits {
		var wh workHit
		if err := h.DecodeInto(&wh); err == nil && titleHasWords(wh.Title, words) {
			out = append(out, h)
		}
	}
	return out
}

// popularHits — тот же запрос по известности (все слова обязательны: иначе
// Meili ронял бы хвостовые слова).
func (s *Service) popularHits(ctx context.Context, q, filter string, limit int64, withScore bool) (meilisearch.Hits, error) {
	req := &meilisearch.SearchRequest{
		Limit: limit, Sort: popularitySort, MatchingStrategy: meilisearch.All, ShowRankingScore: withScore,
	}
	if filter != "" {
		req.Filter = filter
	}
	res, err := s.meili.Index(worksIndexName).SearchWithContext(ctx, q, req)
	if err != nil {
		return nil, fmt.Errorf("meili works search (popular): %w", err)
	}
	return res.Hits, nil
}

// listWorksWithPinned — /books с закреплёнными известными совпадениями: первая
// страница — они сверху, дальше обычная выдача без них (смещение следующих
// страниц сдвигается на их число — страницы стыкуются без потерь и повторов).
// Закреплённые считаются одинаково на каждой странице. ok=false — закреплять
// нечего (или Meili ещё не умеет фильтр по id) — обычный путь.
func (s *Service) listWorksWithPinned(ctx context.Context, params ListParams, offset, limit int, rerank bool,
	visibleLangs []string) (ListResponse, bool, error) {
	index := s.meili.Index(worksIndexName)
	q := textnorm.FoldYo(params.Query)
	base := buildWorksFilter(params, visibleLangs)

	// Весь запрос (точное число, фасеты, первые по релевантности) и известные —
	// параллельно.
	type popRes struct {
		hits meilisearch.Hits
		err  error
	}
	popCh := make(chan popRes, 1)
	go func() {
		h, err := s.popularHits(ctx, q, base, pinCandidates, false)
		popCh <- popRes{h, err}
	}()
	probe := &meilisearch.SearchRequest{MatchingStrategy: meilisearch.All, HitsPerPage: pinProbeHits, Page: 1}
	if base != "" {
		probe.Filter = base
	}
	if len(params.Facets) > 0 {
		probe.Facets = params.Facets
	}
	resAll, err := index.SearchWithContext(ctx, q, probe)
	pop := <-popCh
	if err != nil {
		return ListResponse{}, false, fmt.Errorf("meili works search: %w", err)
	}
	if pop.err != nil {
		return ListResponse{}, false, nil // без известных — обычный поиск
	}
	pinned := pickPinned(decodeWorkHits(pop.hits), decodeWorkHits(resAll.Hits), params.Query)
	if len(pinned) == 0 {
		return ListResponse{}, false, nil
	}

	// Итоговый список = закреплённые + остальные (без них). Страница
	// [offset, offset+limit) берёт свой отрезок из обеих частей.
	var segA []scoredItem
	for i := offset; i < len(pinned) && i < offset+limit; i++ {
		segA = append(segA, scoredItem{item: pinned[i].toListItem()})
	}
	restLimit := limit - len(segA)
	restOffset := max(0, offset-len(pinned))
	var resB *meilisearch.SearchResponse
	if restLimit > 0 {
		reqB := &meilisearch.SearchRequest{
			MatchingStrategy: meilisearch.All, ShowRankingScore: rerank,
			Limit: int64(restLimit), Offset: int64(restOffset),
			Filter: andFilter(base, idsExclusion(pinned)),
		}
		resB, err = index.SearchWithContext(ctx, q, reqB)
		if err != nil {
			// Фильтр по id ещё не применён (настройки индекса догоняют после
			// выкатки) — обычный поиск, не ошибка.
			return ListResponse{}, false, nil
		}
	} else {
		resB = &meilisearch.SearchResponse{}
	}
	segB := scoreWorkHits(resB.Hits, rerank)
	if rerank {
		s.rerankScored(ctx, params.UserID, segB)
	}
	items := make([]ListItem, 0, len(segA)+len(segB))
	for _, sc := range append(segA, segB...) {
		items = append(items, sc.item)
	}
	HydrateListMeta(ctx, s.pool, items)
	s.hydrateWorkRepresentative(ctx, items, params.ExcludeGenres, params.ExcludeLangs)
	return ListResponse{
		Items:       items,
		Total:       resAll.TotalHits,
		Limit:       limit,
		Offset:      offset,
		Query:       params.Query,
		ProcessTime: resAll.ProcessingTimeMs + resB.ProcessingTimeMs,
		Facets:      decodeFacets(resAll.FacetDistribution),
	}, true, nil
}
