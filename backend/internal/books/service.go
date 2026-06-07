package books

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meilisearch/meilisearch-go"
	"github.com/skriptes/skriptes/backend/internal/history"
)

// worksIndexName — индекс логических книг (works) в Meili. Зеркало
// importer.worksIndex (тот пакет не экспортирует константу). Веб-список и Cmd+K
// ищут здесь, чтобы фасеты считали РАБОТЫ; OPDS остаётся на индексе "books".
const worksIndexName = "works"

// langCacheTTL — как долго кэшировать вселенную языков коллекции (нужна для
// корректного скрытия мультиязычных работ: показать работу, если у неё есть
// издание на видимом языке).
const langCacheTTL = 5 * time.Minute

// ErrNotFound возвращается из Get когда книги с таким id нет (или она удалена).
var ErrNotFound = errors.New("book not found")

// PersonaSource — узкий контракт для персонализированного re-ranking'а.
// Реализуется *history.Service. Объявлен интерфейсом чтобы unit-тестам
// можно было подсовывать заглушки, а main-wiring пока остаётся тривиальным.
type PersonaSource interface {
	PersonaProfile(ctx context.Context, userID int64) (history.PersonaProfile, error)
}

// Service — read-side сервис каталога книг.
type Service struct {
	pool    *pgxpool.Pool
	meili   meilisearch.ServiceManager
	persona PersonaSource

	// Кэш вселенной языков коллекции (для works-фильтра скрытия языков).
	langMu      sync.Mutex
	langCache   []string
	langCacheAt time.Time
}

// New собирает Service. meili может быть nil — тогда List вернёт пустой
// результат вместо ошибки (полезно для unit-тестов handlers без Meili).
// persona может быть nil — тогда персонализация не применяется
// (List вернёт результаты в meili-порядке).
func New(pool *pgxpool.Pool, meili meilisearch.ServiceManager, persona PersonaSource) *Service {
	return &Service{pool: pool, meili: meili, persona: persona}
}

// List — поиск книг через Meilisearch с фильтрами, сортировкой и facets.
//
// Логика комбинирования фильтров: AND между разными атрибутами,
// OR внутри multi-value (genres). Год — диапазон [from, to] инклюзивно.
//
// Если params.Query пустая — возвращает первые limit/offset (Meili
// сортирует по дефолтным правилам, либо по params.Sort).
func (s *Service) List(ctx context.Context, params ListParams) (ListResponse, error) {
	if s.meili == nil {
		return ListResponse{Items: []ListItem{}, Limit: params.Limit, Offset: params.Offset}, nil
	}
	limit := clampInt(params.Limit, 1, 100, 20)
	offset := clampInt(params.Offset, 0, 10_000, 0)

	// Решаем, применяем ли re-ranking. Условия:
	//   - есть PersonaSource и UserID > 0;
	//   - есть текстовый запрос — re-rank только в search-сценарии;
	//     на "пустом" /books пользователь ожидает стабильный список,
	//     а не индивидуальный для каждого пользователя;
	//   - первая страница (offset == 0) — пагинированный re-rank путает;
	//   - пользователь не задал явную сортировку (Sort пустой);
	//   - нет фильтров по конкретному автору/серии (там и так одна группа).
	rerank := s.persona != nil && params.UserID > 0 && params.Query != "" &&
		offset == 0 && params.Sort == "" && params.AuthorID == 0 && params.SeriesID == 0

	// Расширяем окно meili-запроса при rerank: получаем ~3*limit (capped 50)
	// чтобы было что переупорядочивать; вернём всё равно limit.
	meiliLimit := int64(limit)
	if rerank {
		meiliLimit = int64(limit * 3)
		if meiliLimit > 50 {
			meiliLimit = 50
		}
	}

	req := &meilisearch.SearchRequest{
		Limit:            meiliLimit,
		Offset:           int64(offset),
		ShowRankingScore: rerank,
	}
	if f := buildFilter(params); f != "" {
		req.Filter = f
	}
	if sortRules := buildSort(params.Sort); len(sortRules) > 0 {
		req.Sort = sortRules
	}
	if len(params.Facets) > 0 {
		req.Facets = params.Facets
	}

	res, err := s.meili.Index("books").SearchWithContext(ctx, params.Query, req)
	if err != nil {
		return ListResponse{}, fmt.Errorf("meili search: %w", err)
	}

	scored := make([]scoredItem, 0, len(res.Hits))
	for _, h := range res.Hits {
		var item ListItem
		if err := h.DecodeInto(&item); err != nil {
			continue
		}
		score := 0.0
		if rerank {
			if raw, ok := h["_rankingScore"]; ok && len(raw) > 0 {
				_ = json.Unmarshal(raw, &score)
			}
		}
		scored = append(scored, scoredItem{item: item, base: score})
	}

	if rerank {
		profile, err := s.persona.PersonaProfile(ctx, params.UserID)
		if err == nil && !profile.IsEmpty() {
			applyPersonaBoost(scored, profile)
			sortByFinalScore(scored)
			if len(scored) > limit {
				scored = scored[:limit]
			}
		}
	}

	items := make([]ListItem, 0, len(scored))
	for _, sc := range scored {
		items = append(items, sc.item)
	}

	// Догидрачиваем cover_path из Postgres: в Meili-индексе обложек нет
	// (проставляются лениво после индексации enrichment'ом), а для
	// мобильного списка с thumbnail'ами нужен свежий путь. Один
	// batched-SELECT по id текущей страницы — дёшево.
	s.hydrateCovers(ctx, items)

	total := res.EstimatedTotalHits
	if total == 0 && res.TotalHits > 0 {
		total = res.TotalHits
	}
	return ListResponse{
		Items:       items,
		Total:       total,
		Limit:       limit,
		Offset:      offset,
		Query:       params.Query,
		ProcessTime: res.ProcessingTimeMs,
		Facets:      decodeFacets(res.FacetDistribution),
	}, nil
}

// hydrateCovers проставляет CoverPath и EditionCount на items одним
// batched-запросом в Postgres. Meili-индекс обложек не хранит (они приезжают
// лениво), а edition_count живёт в works — берём прямо из БД по id текущей
// страницы (id = представительное издание работы). Ошибки не фатальны.
func (s *Service) hydrateCovers(ctx context.Context, items []ListItem) {
	if s.pool == nil || len(items) == 0 {
		return
	}
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT b.id,
		       COALESCE(b.cover_path, (
		           SELECT bb.cover_path FROM books bb
		           WHERE bb.work_id = b.work_id AND bb.deleted = false
		             AND bb.cover_path IS NOT NULL AND bb.cover_path <> ''
		           ORDER BY bb.id LIMIT 1
		       ), ''),
		       COALESCE(w.edition_count, 1)
		FROM books b
		LEFT JOIN works w ON w.id = b.work_id
		WHERE b.id = ANY($1)
	`, ids)
	if err != nil {
		return
	}
	defer rows.Close()
	type hyd struct {
		cover    string
		editions int
	}
	byID := make(map[int64]hyd, len(items))
	for rows.Next() {
		var id int64
		var h hyd
		if err := rows.Scan(&id, &h.cover, &h.editions); err != nil {
			return
		}
		byID[id] = h
	}
	if rows.Err() != nil {
		return
	}
	for i := range items {
		if h, ok := byID[items[i].ID]; ok {
			if h.cover != "" {
				items[i].CoverPath = h.cover
			}
			items[i].EditionCount = h.editions
		}
	}
}

// ── works-индекс: список/typeahead/карточка по работе ───────────

// workHit — декодирование документа индекса works. Отличие от ListItem —
// lang приходит МАССИВОМ (json-ключ "lang"); маппим в представительный язык
// для чипа в списке.
type workHit struct {
	ID           int64    `json:"id"`
	Title        string   `json:"title"`
	Authors      []string `json:"authors"`
	AuthorIDs    []int64  `json:"author_ids"`
	Series       string   `json:"series"`
	SeriesID     *int64   `json:"series_id"`
	Genres       []string `json:"genres"`
	Year         *int     `json:"year"`
	Langs        []string `json:"lang"`
	EditionCount int      `json:"edition_count"`
}

func (wh workHit) toListItem() ListItem {
	li := ListItem{
		ID: wh.ID, Title: wh.Title, Authors: wh.Authors, AuthorIDs: wh.AuthorIDs,
		Series: wh.Series, SeriesID: wh.SeriesID, Genres: wh.Genres, Year: wh.Year,
		EditionCount: wh.EditionCount,
	}
	if len(wh.Langs) > 0 {
		langs := append([]string(nil), wh.Langs...)
		sort.Strings(langs) // детерминированный представительный язык для чипа
		li.Lang = langs[0]
	}
	return li
}

// ListWorks — веб-список/поиск по индексу works (фасеты считают РАБОТЫ).
// Зеркало List, но: индекс works, work-mode фильтр (genres NOT IN + lang IN
// visible), id = works.id, обложка/edition_count гидрируются по work_id.
func (s *Service) ListWorks(ctx context.Context, params ListParams) (ListResponse, error) {
	if s.meili == nil {
		return ListResponse{Items: []ListItem{}, Limit: params.Limit, Offset: params.Offset}, nil
	}
	limit := clampInt(params.Limit, 1, 100, 20)
	offset := clampInt(params.Offset, 0, 10_000, 0)

	rerank := s.persona != nil && params.UserID > 0 && params.Query != "" &&
		offset == 0 && params.Sort == "" && params.AuthorID == 0 && params.SeriesID == 0
	meiliLimit := int64(limit)
	if rerank {
		meiliLimit = int64(limit * 3)
		if meiliLimit > 50 {
			meiliLimit = 50
		}
	}

	var visibleLangs []string
	if len(params.ExcludeLangs) > 0 {
		visibleLangs = s.allLangs(ctx)
	}

	req := &meilisearch.SearchRequest{
		Limit:            meiliLimit,
		Offset:           int64(offset),
		ShowRankingScore: rerank,
	}
	if f := buildWorksFilter(params, visibleLangs); f != "" {
		req.Filter = f
	}
	if sortRules := buildSort(params.Sort); len(sortRules) > 0 {
		req.Sort = sortRules
	}
	if len(params.Facets) > 0 {
		req.Facets = params.Facets
	}

	res, err := s.meili.Index(worksIndexName).SearchWithContext(ctx, params.Query, req)
	if err != nil {
		return ListResponse{}, fmt.Errorf("meili works search: %w", err)
	}

	// Fallback на books-индекс, пока works-индекс ещё НЕ построен (одноразовое
	// окно при апгрейде до Phase 6: ResyncWorksIndex наполняет works в фоне).
	// У непустой библиотеки browse без запроса/фильтров не может дать 0 работ →
	// значит индекс пуст. List отдаёт издания, но с work_id (через DecodeInto) —
	// ссылки на /works корректны; самолечится, как только works наполнится.
	if len(res.Hits) == 0 && isUnfilteredBrowse(params) {
		return s.List(ctx, params)
	}

	scored := make([]scoredItem, 0, len(res.Hits))
	for _, h := range res.Hits {
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
		scored = append(scored, scoredItem{item: wh.toListItem(), base: score})
	}

	if rerank {
		profile, err := s.persona.PersonaProfile(ctx, params.UserID)
		if err == nil && !profile.IsEmpty() {
			applyPersonaBoost(scored, profile)
			sortByFinalScore(scored)
			if len(scored) > limit {
				scored = scored[:limit]
			}
		}
	}

	items := make([]ListItem, 0, len(scored))
	for _, sc := range scored {
		items = append(items, sc.item)
	}
	s.hydrateWorkCovers(ctx, items)

	total := res.EstimatedTotalHits
	if total == 0 && res.TotalHits > 0 {
		total = res.TotalHits
	}
	return ListResponse{
		Items:       items,
		Total:       total,
		Limit:       limit,
		Offset:      offset,
		Query:       params.Query,
		ProcessTime: res.ProcessingTimeMs,
		Facets:      decodeFacets(res.FacetDistribution),
	}, nil
}

// SuggestWorks — typeahead по индексу works (Cmd+K). Зеркало Suggest.
func (s *Service) SuggestWorks(ctx context.Context, query string, limit int, userID int64, excludeGenres, excludeLangs []string) ([]ListItem, error) {
	if s.meili == nil || strings.TrimSpace(query) == "" {
		return []ListItem{}, nil
	}
	limit = clampInt(limit, 1, 20, 5)
	rerank := s.persona != nil && userID > 0
	meiliLimit := int64(limit)
	if rerank {
		meiliLimit = int64(limit * 3)
		if meiliLimit > 20 {
			meiliLimit = 20
		}
	}

	var visibleLangs []string
	if len(excludeLangs) > 0 {
		visibleLangs = s.allLangs(ctx)
	}
	req := &meilisearch.SearchRequest{Limit: meiliLimit, ShowRankingScore: rerank}
	if f := worksExclusionFilter(excludeGenres, excludeLangs, visibleLangs); f != "" {
		req.Filter = f
	}
	res, err := s.meili.Index(worksIndexName).SearchWithContext(ctx, query, req)
	if err != nil {
		return nil, fmt.Errorf("meili works search: %w", err)
	}

	scored := make([]scoredItem, 0, len(res.Hits))
	for _, h := range res.Hits {
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
		scored = append(scored, scoredItem{item: wh.toListItem(), base: score})
	}

	if rerank {
		profile, err := s.persona.PersonaProfile(ctx, userID)
		if err == nil && !profile.IsEmpty() {
			applyPersonaBoost(scored, profile)
			sortByFinalScore(scored)
			if len(scored) > limit {
				scored = scored[:limit]
			}
		}
	}

	out := make([]ListItem, 0, len(scored))
	for _, sc := range scored {
		out = append(out, sc.item)
	}
	s.hydrateWorkCovers(ctx, out)
	return out, nil
}

// hydrateWorkCovers проставляет CoverPath (обложка любого издания работы) и
// EditionCount по work_id (= ListItem.ID для works-выдачи).
func (s *Service) hydrateWorkCovers(ctx context.Context, items []ListItem) {
	if s.pool == nil || len(items) == 0 {
		return
	}
	ids := make([]int64, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT w.id, COALESCE(rep.id, 0), COALESCE(rep.cover_path, ''), COALESCE(w.edition_count, 1)
		FROM works w
		LEFT JOIN LATERAL (
		    SELECT bb.id, bb.cover_path
		    FROM books bb
		    WHERE bb.work_id = w.id AND bb.deleted = false
		    ORDER BY (bb.cover_path IS NOT NULL AND bb.cover_path <> '') DESC, bb.id
		    LIMIT 1
		) rep ON true
		WHERE w.id = ANY($1)
	`, ids)
	if err != nil {
		return
	}
	defer rows.Close()
	type hyd struct {
		coverEditionID int64
		cover          string
		editions       int
	}
	byID := make(map[int64]hyd, len(items))
	for rows.Next() {
		var id int64
		var h hyd
		if err := rows.Scan(&id, &h.coverEditionID, &h.cover, &h.editions); err != nil {
			return
		}
		byID[id] = h
	}
	if rows.Err() != nil {
		return
	}
	for i := range items {
		if h, ok := byID[items[i].ID]; ok {
			if h.cover != "" {
				items[i].CoverPath = h.cover
			}
			if h.coverEditionID > 0 {
				items[i].CoverEditionID = h.coverEditionID
			}
			items[i].EditionCount = h.editions
		}
	}
}

// allLangs — вселенная нормализованных языков коллекции (кэш с TTL). На ошибке
// возвращает прошлый кэш (возможно nil) — фильтр скрытия тогда делает fallback.
func (s *Service) allLangs(ctx context.Context) []string {
	s.langMu.Lock()
	defer s.langMu.Unlock()
	if s.langCache != nil && time.Since(s.langCacheAt) < langCacheTTL {
		return s.langCache
	}
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT lower(btrim(lang)) FROM books
		WHERE deleted = false AND lang IS NOT NULL AND btrim(lang) <> ''
	`)
	if err != nil {
		return s.langCache
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var l string
		if rows.Scan(&l) == nil && l != "" {
			out = append(out, l)
		}
	}
	if rows.Err() == nil {
		s.langCache = out
		s.langCacheAt = time.Now()
	}
	return s.langCache
}

// GetWork возвращает карточку логической книги по works.id. Представительное
// издание выбирается среди ВИДИМЫХ (не скрытых жанром/языком); если видимых нет
// — ErrNotFound (работа целиком скрыта). Дальше переиспользуется Get(repID):
// он уже work-центричен (title/авторы/жанры — уровня работы).
func (s *Service) GetWork(ctx context.Context, workID int64, excludeGenres, excludeLangs []string) (Book, error) {
	repID, err := s.visibleWorkEditionID(ctx, workID, excludeGenres, excludeLangs)
	if err != nil {
		return Book{}, err
	}
	return s.Get(ctx, repID)
}

// visibleWorkEditionID — id представительного ВИДИМОГО издания работы (обложка в
// приоритете, затем язык/год издания/id). Применяет те же исключения, что и
// список: издание видимо, если его язык не скрыт И ни один жанр не скрыт.
func (s *Service) visibleWorkEditionID(ctx context.Context, workID int64, excludeGenres, excludeLangs []string) (int64, error) {
	var id int64
	// COALESCE(..,'{}') — nil-слайс pgx кодирует как NULL, а `<> ALL(NULL)` даёт
	// NULL (не TRUE) и отсёк бы все издания. Пустой массив → корректно: <> ALL(чисто)
	// = TRUE, = ANY(пусто) = FALSE.
	err := s.pool.QueryRow(ctx, `
		SELECT b.id FROM books b
		WHERE b.work_id = $1 AND b.deleted = false
		  AND (b.lang IS NULL OR lower(btrim(b.lang)) <> ALL(COALESCE($3::text[], '{}')))
		  AND NOT EXISTS (
		      SELECT 1 FROM book_genres bg JOIN genres g ON g.id = bg.genre_id
		      WHERE bg.book_id = b.id AND g.fb2_code = ANY(COALESCE($2::text[], '{}'))
		  )
		ORDER BY (b.cover_path IS NOT NULL AND b.cover_path <> '') DESC,
		         b.lang NULLS LAST, b.edition_year DESC NULLS LAST, b.id
		LIMIT 1
	`, workID, excludeGenres, excludeLangs).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("pick visible edition: %w", err)
	}
	return id, nil
}

// isUnfilteredBrowse — «голый» browse первой страницы (без запроса/фильтров).
// Только для него ListWorks делает fallback на books-индекс при пустом works.
func isUnfilteredBrowse(p ListParams) bool {
	return p.Query == "" && len(p.Genres) == 0 && p.Lang == "" &&
		p.YearFrom == 0 && p.YearTo == 0 && p.SeriesID == 0 && p.AuthorID == 0 &&
		p.Offset <= 0
}

// buildWorksFilter — meili-фильтр для индекса works. genres-исключение — NOT IN
// (жанры уровня работы); lang-исключение — через worksLangExclusion (показать
// работу, если у неё есть издание на видимом языке).
func buildWorksFilter(p ListParams, visibleLangs []string) string {
	var parts []string
	if clause := inClause("genres", p.Genres); clause != "" {
		parts = append(parts, clause)
	}
	if p.Lang != "" {
		parts = append(parts, fmt.Sprintf("lang = %s", strconv.Quote(p.Lang)))
	}
	if p.YearFrom > 0 {
		parts = append(parts, fmt.Sprintf("year >= %d", p.YearFrom))
	}
	if p.YearTo > 0 {
		parts = append(parts, fmt.Sprintf("year <= %d", p.YearTo))
	}
	if p.SeriesID > 0 {
		parts = append(parts, fmt.Sprintf("series_id = %d", p.SeriesID))
	}
	if p.AuthorID > 0 {
		parts = append(parts, fmt.Sprintf("author_ids = %d", p.AuthorID))
	}
	if clause := notInClause("genres", p.ExcludeGenres); clause != "" {
		parts = append(parts, clause)
	}
	if clause := worksLangExclusion(p.ExcludeLangs, visibleLangs); clause != "" {
		parts = append(parts, clause)
	}
	return strings.Join(parts, " AND ")
}

// worksExclusionFilter — только исключения (для SuggestWorks).
func worksExclusionFilter(excludeGenres, excludeLangs, visibleLangs []string) string {
	var parts []string
	if c := notInClause("genres", excludeGenres); c != "" {
		parts = append(parts, c)
	}
	if c := worksLangExclusion(excludeLangs, visibleLangs); c != "" {
		parts = append(parts, c)
	}
	return strings.Join(parts, " AND ")
}

// worksLangExclusion — скрытие по языку на индексе works (lang — массив языков
// изданий). Семантика: показать работу, если у неё есть издание на видимом
// языке. visible = вселенная − скрытые. Работы вовсе без языка не прячем
// (паритет с books-индексом, где NOT IN их пропускал). Если вселенная неизвестна
// (ошибка кэша) — консервативный fallback на NOT IN.
func worksLangExclusion(excludeLangs, visibleLangs []string) string {
	if len(excludeLangs) == 0 {
		return ""
	}
	if len(visibleLangs) == 0 {
		return notInClause("lang", excludeLangs)
	}
	vis := subtractLangs(visibleLangs, excludeLangs)
	if len(vis) == 0 {
		// Все языки скрыты — оставляем только работы без языка.
		return "(lang IS EMPTY OR lang IS NULL)"
	}
	return fmt.Sprintf("(%s OR lang IS EMPTY OR lang IS NULL)", inClause("lang", vis))
}

func subtractLangs(universe, remove []string) []string {
	rm := make(map[string]struct{}, len(remove))
	for _, r := range remove {
		rm[strings.ToLower(strings.TrimSpace(r))] = struct{}{}
	}
	var out []string
	for _, x := range universe {
		if _, ok := rm[strings.ToLower(strings.TrimSpace(x))]; !ok {
			out = append(out, x)
		}
	}
	return out
}

// ── Re-ranking ──────────────────────────────────────────────────

// scoredItem — Item + базовый score из Meili (если включали ShowRankingScore)
// + persona-бонус. final = base + personal.
type scoredItem struct {
	item     ListItem
	base     float64
	personal float64
}

// Коэффициенты бонусов. Подобраны под Meili-_rankingScore в [0,1]:
// мы хотим, чтобы прямые персональные сигналы (избранная книга,
// автор, серия) могли уверенно перевесить разницу в релевантности.
//
// Иерархия по силе:
//
//	favorite_book   (0.6)   — самый сильный: пользователь явно сказал «хочу»
//	favorite_author (0.5)   — почти такой же, но даёт буст ВСЕМ книгам автора
//	favorite_series (0.4)   — аналогично для серии
//	per-book activity       — view = 0.1, read = 0.3, cap 0.5 (просмотрел → ещё раз нужна)
//	author/series activity  — сильно ниже: лишь намёк, что "похоже на интересы"
//	genre activity          — самый слабый: жанры пересекаются у многого
const (
	bonusFavoriteBook   = 0.6
	bonusFavoriteAuthor = 0.5
	bonusFavoriteSeries = 0.4

	// bookActivityScale — на каждую единицу веса в BookActivity. View=1
	// → +0.1, read=3 → +0.3. Этот буст применяется К САМОЙ книге, поэтому
	// он должен быть заметным: после открытия карточки книга должна
	// уверенно подниматься на повторном поиске.
	bookActivityScale = 0.1
	bookActivityCap   = 0.5

	// Activity по авторам/сериям/жанрам — заметно слабее: это "похожее",
	// а не "то же самое".
	authorActivityScale = 0.05
	authorActivityCap   = 0.4
	seriesActivityScale = 0.05
	seriesActivityCap   = 0.4
	genreActivityScale  = 0.02
	genreActivityCap    = 0.2
)

func applyPersonaBoost(scored []scoredItem, p history.PersonaProfile) {
	for i := range scored {
		it := scored[i].item
		bonus := 0.0

		// Прямой book-level сигнал — самый сильный.
		if _, ok := p.FavoriteBooks[it.ID]; ok {
			bonus += bonusFavoriteBook
		}
		if w, ok := p.BookActivity[it.ID]; ok {
			bonus += capFloat(w*bookActivityScale, bookActivityCap)
		}

		for _, aid := range it.AuthorIDs {
			if _, ok := p.FavoriteAuthors[aid]; ok {
				bonus += bonusFavoriteAuthor
			}
			if w, ok := p.AuthorActivity[aid]; ok {
				bonus += capFloat(w*authorActivityScale, authorActivityCap)
			}
		}
		if it.SeriesID != nil {
			sid := *it.SeriesID
			if _, ok := p.FavoriteSeries[sid]; ok {
				bonus += bonusFavoriteSeries
			}
			if w, ok := p.SeriesActivity[sid]; ok {
				bonus += capFloat(w*seriesActivityScale, seriesActivityCap)
			}
		}
		for _, g := range it.Genres {
			if w, ok := p.GenreActivity[g]; ok {
				bonus += capFloat(w*genreActivityScale, genreActivityCap)
			}
		}
		scored[i].personal = bonus
	}
}

// sortByFinalScore — стабильная сортировка по убыванию (base+personal),
// сохраняет порядок Meili при равных score. Стабильность важна: если
// у двух хитов нет бонусов, они должны остаться в meili-порядке.
func sortByFinalScore(scored []scoredItem) {
	sort.SliceStable(scored, func(i, j int) bool {
		return (scored[i].base + scored[i].personal) > (scored[j].base + scored[j].personal)
	})
}

func capFloat(v, max float64) float64 {
	if v > max {
		return max
	}
	return v
}

// buildFilter — собирает meili filter-expression из ListParams.
// Возвращает пустую строку если фильтров нет.
//
// Эскейпинг строк: используем strconv.Quote — он даст корректный JSON-
// совместимый литерал ("Сергей \"Лютый\" Иванов" → "Сергей \"Лютый\" Иванов"),
// и meili такие литералы понимает.
func buildFilter(p ListParams) string {
	var parts []string
	if clause := inClause("genres", p.Genres); clause != "" {
		parts = append(parts, clause)
	}
	if p.Lang != "" {
		parts = append(parts, fmt.Sprintf("lang = %s", strconv.Quote(p.Lang)))
	}
	if p.YearFrom > 0 {
		parts = append(parts, fmt.Sprintf("year >= %d", p.YearFrom))
	}
	if p.YearTo > 0 {
		parts = append(parts, fmt.Sprintf("year <= %d", p.YearTo))
	}
	if p.SeriesID > 0 {
		parts = append(parts, fmt.Sprintf("series_id = %d", p.SeriesID))
	}
	if p.AuthorID > 0 {
		parts = append(parts, fmt.Sprintf("author_ids = %d", p.AuthorID))
	}
	// Скрытые жанры/языки (admin ∪ персональные) — исключающие фильтры.
	if clause := notInClause("genres", p.ExcludeGenres); clause != "" {
		parts = append(parts, clause)
	}
	if clause := notInClause("lang", p.ExcludeLangs); clause != "" {
		parts = append(parts, clause)
	}
	return strings.Join(parts, " AND ")
}

// inClause / notInClause собирают meili-выражение `<attr> IN [...]` /
// `<attr> NOT IN [...]` с корректным эскейпингом значений. Пустые
// значения отбрасываются; если значимых нет — возвращается "".
func inClause(attr string, values []string) string {
	if q := quoteValues(values); q != "" {
		return fmt.Sprintf("%s IN [%s]", attr, q)
	}
	return ""
}

func notInClause(attr string, values []string) string {
	if q := quoteValues(values); q != "" {
		return fmt.Sprintf("%s NOT IN [%s]", attr, q)
	}
	return ""
}

func quoteValues(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" {
			continue
		}
		quoted = append(quoted, strconv.Quote(v))
	}
	return strings.Join(quoted, ",")
}

// exclusionFilter — meili-выражение для скрытых жанров/языков
// (`genres NOT IN [...] AND lang NOT IN [...]`). Пусто, если скрывать
// нечего. Используется в Suggest; в List те же клаузы добавляет buildFilter.
func exclusionFilter(excludeGenres, excludeLangs []string) string {
	var parts []string
	if c := notInClause("genres", excludeGenres); c != "" {
		parts = append(parts, c)
	}
	if c := notInClause("lang", excludeLangs); c != "" {
		parts = append(parts, c)
	}
	return strings.Join(parts, " AND ")
}

// buildSort — преобразует "user-friendly" значение sort в массив для Meili.
// Если sort пустой — возвращаем nil, Meili применит свои ranking rules
// (включая popularity:desc как customRanking).
func buildSort(sort string) []string {
	switch sort {
	case "year_desc":
		return []string{"year:desc"}
	case "year_asc":
		return []string{"year:asc"}
	case "popularity":
		return []string{"popularity:desc"}
	default:
		return nil
	}
}

// decodeFacets — превращает FacetDistribution (json.RawMessage в SDK)
// в map[string]map[string]int64. Если raw пустой или не парсится —
// возвращаем nil: facets опциональны, лучше отдать список без них,
// чем 500.
func decodeFacets(raw json.RawMessage) map[string]map[string]int64 {
	if len(raw) == 0 {
		return nil
	}
	var out map[string]map[string]int64
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Suggest — компактный typeahead по индексу books.
// Возвращает срезанный набор ListItem (без total/pagination).
//
// Если userID > 0 и есть PersonaSource — применяется тот же
// персонализированный re-ranking, что и в List: книги любимых
// авторов/серий поднимаются наверх. В палитре поиска (Cmd+K) это
// особенно ценно, потому что показывается только top-5.
//
// excludeGenres / excludeLangs — скрытые из выдачи жанры/языки (admin ∪
// персональные); применяются тем же `NOT IN`, что и в List, чтобы палитра
// не подсказывала книги, которых нет в основном списке.
//
// Если meili не сконфигурирован — пустой срез без ошибки (для unit-тестов).
func (s *Service) Suggest(ctx context.Context, query string, limit int, userID int64, excludeGenres, excludeLangs []string) ([]ListItem, error) {
	if s.meili == nil || strings.TrimSpace(query) == "" {
		return []ListItem{}, nil
	}
	limit = clampInt(limit, 1, 20, 5)
	rerank := s.persona != nil && userID > 0

	meiliLimit := int64(limit)
	if rerank {
		// То же расширение окна, что и в List, но более скромное —
		// палитра редко отдаёт больше 5-10 строк.
		meiliLimit = int64(limit * 3)
		if meiliLimit > 20 {
			meiliLimit = 20
		}
	}

	req := &meilisearch.SearchRequest{
		Limit:            meiliLimit,
		ShowRankingScore: rerank,
	}
	if f := exclusionFilter(excludeGenres, excludeLangs); f != "" {
		req.Filter = f
	}
	res, err := s.meili.Index("books").SearchWithContext(ctx, query, req)
	if err != nil {
		return nil, fmt.Errorf("meili search: %w", err)
	}

	scored := make([]scoredItem, 0, len(res.Hits))
	for _, h := range res.Hits {
		var item ListItem
		if err := h.DecodeInto(&item); err != nil {
			continue
		}
		score := 0.0
		if rerank {
			if raw, ok := h["_rankingScore"]; ok && len(raw) > 0 {
				_ = json.Unmarshal(raw, &score)
			}
		}
		scored = append(scored, scoredItem{item: item, base: score})
	}

	if rerank {
		profile, err := s.persona.PersonaProfile(ctx, userID)
		if err == nil && !profile.IsEmpty() {
			applyPersonaBoost(scored, profile)
			sortByFinalScore(scored)
			if len(scored) > limit {
				scored = scored[:limit]
			}
		}
	}

	out := make([]ListItem, 0, len(scored))
	for _, sc := range scored {
		out = append(out, sc.item)
	}
	return out, nil
}

// Get возвращает детальную карточку книги по id.
// Удалённые (deleted=true) тоже возвращаются — frontend сам решит как их
// показать. Это симметрично с импортёром, который их сохраняет в PG.
func (s *Service) Get(ctx context.Context, id int64) (Book, error) {
	var b Book
	var (
		dateAdded   pgtype.Date
		writtenYear pgtype.Int2
		editionYear pgtype.Int2
		rating      pgtype.Int2
		annotation  pgtype.Text
		coverPath   pgtype.Text
		serNo       pgtype.Int4
		seriesID    pgtype.Int8
		seriesTitle pgtype.Text
		archive     string
		lang        pgtype.Text
	)
	// Work-центрично: Title/WrittenYear/Series/SerNo — уровня работы (works);
	// lang/edition_year/cover/file/size/annotation — ОТКРЫТОГО издания (id в URL),
	// для кнопок скачать/читать и обратной совместимости.
	err := s.pool.QueryRow(ctx, `
		SELECT
			b.id, b.lib_id, COALESCE(w.title, b.title), b.work_id,
			b.lang, b.date_added, b.rating, b.annotation, b.cover_path,
			COALESCE(w.written_year, b.written_year), b.edition_year,
			COALESCE(w.ser_no, b.ser_no), COALESCE(w.series_id, b.series_id), s.title,
			b.file_name, b.ext, b.size_bytes, b.deleted,
			a.filename
		FROM books b
		LEFT JOIN works w    ON w.id = b.work_id
		LEFT JOIN series s   ON s.id = COALESCE(w.series_id, b.series_id)
		JOIN archives a      ON a.id = b.archive_id
		WHERE b.id = $1
	`, id).Scan(
		&b.ID, &b.LibID, &b.Title, &b.WorkID,
		&lang, &dateAdded, &rating, &annotation, &coverPath,
		&writtenYear, &editionYear,
		&serNo, &seriesID, &seriesTitle,
		&b.FileName, &b.Ext, &b.SizeBytes, &b.Deleted,
		&archive,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Book{}, ErrNotFound
		}
		return Book{}, fmt.Errorf("query book: %w", err)
	}

	if lang.Valid {
		b.Lang = lang.String
	}
	if dateAdded.Valid {
		t := dateAdded.Time
		b.DateAdded = &t
	}
	if rating.Valid {
		v := int(rating.Int16)
		b.Rating = &v
	}
	if annotation.Valid {
		b.Annotation = annotation.String
	}
	if coverPath.Valid {
		b.CoverPath = coverPath.String
	}
	if writtenYear.Valid {
		v := int(writtenYear.Int16)
		b.WrittenYear = &v
	}
	if editionYear.Valid {
		v := int(editionYear.Int16)
		b.EditionYear = &v
	}
	if serNo.Valid {
		v := int(serNo.Int32)
		b.SerNo = &v
	}
	if seriesID.Valid && seriesTitle.Valid {
		b.Series = &SeriesRef{ID: seriesID.Int64, Title: seriesTitle.String}
	}
	b.Archive = archive

	// Авторы/жанры — уровня РАБОTЫ (union по всем изданиям). Для singleton-работы
	// совпадает с изданием. WorkID гарантирован инвариантом (миграция 0017).
	workID := b.WorkID
	if workID == 0 {
		workID = -1 // не сматчит ничего → пустые union (defensive)
	}
	authors, err := s.queryWorkAuthors(ctx, workID, b.ID)
	if err != nil {
		return Book{}, err
	}
	b.Authors = authors

	genres, err := s.queryWorkGenres(ctx, workID, b.ID)
	if err != nil {
		return Book{}, err
	}
	b.Genres = genres

	editions, err := s.queryEditions(ctx, workID, b.ID)
	if err != nil {
		return Book{}, err
	}
	b.Editions = editions

	// Обложка карточки: если у открытого издания её нет — берём обложку любого
	// другого издания работы (для героя/мини-обложки), чтобы не показывать
	// плейсхолдер, когда у книги обложка есть хоть в одном fb2.
	if b.CoverPath == "" {
		for _, e := range b.Editions {
			if e.CoverPath != "" {
				b.CoverPath = e.CoverPath
				break
			}
		}
	}

	// Аннотация карточки — work-level: если у открытого издания её нет, берём
	// аннотацию любого другого издания работы (детерминированно, по id).
	if b.Annotation == "" && workID > 0 {
		var ann pgtype.Text
		if err := s.pool.QueryRow(ctx, `
			SELECT annotation FROM books
			WHERE work_id = $1 AND deleted = false AND annotation IS NOT NULL AND annotation <> ''
			ORDER BY id LIMIT 1
		`, workID).Scan(&ann); err == nil && ann.Valid {
			b.Annotation = ann.String
		}
	}

	return b, nil
}

// GenresAndLang — лёгкий запрос: коды жанров (fb2_code) и язык книги по id.
// Нужен для глобального hard-block скрытого контента на маршрутах, которые
// не грузят полную карточку (обложка по id). Возвращает ErrNotFound, если
// книги нет. Удалённые книги тоже учитываются (gate важнее, чем deleted-флаг).
func (s *Service) GenresAndLang(ctx context.Context, id int64) ([]string, string, error) {
	var (
		lang  pgtype.Text
		codes []string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT b.lang,
		       COALESCE(array_agg(g.fb2_code) FILTER (WHERE g.fb2_code IS NOT NULL), '{}')
		FROM books b
		LEFT JOIN book_genres bg ON bg.book_id = b.id
		LEFT JOIN genres g       ON g.id = bg.genre_id
		WHERE b.id = $1
		GROUP BY b.id, b.lang
	`, id).Scan(&lang, &codes)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", ErrNotFound
		}
		return nil, "", fmt.Errorf("query book genres/lang: %w", err)
	}
	return codes, lang.String, nil
}

// queryWorkAuthors — авторы уровня РАБОТЫ: union по всем изданиям работы
// (workID), либо по одному изданию (bookID), если работа не определена.
func (s *Service) queryWorkAuthors(ctx context.Context, workID, bookID int64) ([]AuthorRef, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.last_name, a.first_name, a.middle_name
		FROM authors a
		JOIN book_authors ba ON ba.author_id = a.id
		JOIN books b         ON b.id = ba.book_id
		WHERE (b.work_id = $1 OR b.id = $2) AND b.deleted = false
		GROUP BY a.id, a.last_name, a.first_name, a.middle_name
		ORDER BY min(ba.position), a.last_name
	`, workID, bookID)
	if err != nil {
		return nil, fmt.Errorf("query authors: %w", err)
	}
	defer rows.Close()
	var out []AuthorRef
	for rows.Next() {
		var a AuthorRef
		if err := rows.Scan(&a.ID, &a.LastName, &a.FirstName, &a.MiddleName); err != nil {
			return nil, err
		}
		a.FullName = fullName(a)
		out = append(out, a)
	}
	return out, rows.Err()
}

// queryWorkGenres — жанры уровня РАБОТЫ: union по всем изданиям работы.
func (s *Service) queryWorkGenres(ctx context.Context, workID, bookID int64) ([]GenreRef, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT g.id, g.fb2_code, COALESCE(g.name_ru,''), COALESCE(g.name_en,'')
		FROM genres g
		JOIN book_genres bg ON bg.genre_id = g.id
		JOIN books b        ON b.id = bg.book_id
		WHERE (b.work_id = $1 OR b.id = $2) AND b.deleted = false
		GROUP BY g.id, g.fb2_code, g.name_ru, g.name_en
		ORDER BY g.fb2_code
	`, workID, bookID)
	if err != nil {
		return nil, fmt.Errorf("query genres: %w", err)
	}
	defer rows.Close()
	var out []GenreRef
	for rows.Next() {
		var g GenreRef
		if err := rows.Scan(&g.ID, &g.Code, &g.NameRu, &g.NameEn); err != nil {
			return nil, err
		}
		g.Display = pickGenreDisplay(g)
		out = append(out, g)
	}
	return out, rows.Err()
}

// queryEditions — все физические издания работы (открытое — первым). Удалённые
// исключаем (их нельзя скачать). Для singleton-работы вернёт одно издание.
func (s *Service) queryEditions(ctx context.Context, workID, bookID int64) ([]EditionRef, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT b.id, b.title, b.series_id, COALESCE(es.title,''),
		       COALESCE(b.lang,''), COALESCE(b.translator,''), b.edition_year,
		       COALESCE(b.publisher,''), COALESCE(b.isbn,''), COALESCE(b.edition_title,''),
		       b.page_count, COALESCE(b.cover_path,''), b.size_bytes, b.ext, ar.filename, b.file_name
		FROM books b
		JOIN archives ar      ON ar.id = b.archive_id
		LEFT JOIN series es    ON es.id = b.series_id
		WHERE (b.work_id = $1 OR b.id = $2) AND b.deleted = false
		ORDER BY (b.id = $2) DESC, b.lang NULLS LAST, b.edition_year DESC NULLS LAST, b.id
	`, workID, bookID)
	if err != nil {
		return nil, fmt.Errorf("query editions: %w", err)
	}
	defer rows.Close()
	var out []EditionRef
	for rows.Next() {
		var (
			e           EditionRef
			editionYear pgtype.Int2
			pageCount   pgtype.Int4
			seriesID    pgtype.Int8
			seriesTitle string
		)
		if err := rows.Scan(&e.ID, &e.Title, &seriesID, &seriesTitle,
			&e.Lang, &e.Translator, &editionYear,
			&e.Publisher, &e.ISBN, &e.EditionTitle, &pageCount,
			&e.CoverPath, &e.SizeBytes, &e.Ext, &e.Archive, &e.FileName); err != nil {
			return nil, err
		}
		if editionYear.Valid {
			v := int(editionYear.Int16)
			e.EditionYear = &v
		}
		if pageCount.Valid {
			v := int(pageCount.Int32)
			e.PageCount = &v
		}
		if seriesID.Valid && seriesTitle != "" {
			e.Series = &SeriesRef{ID: seriesID.Int64, Title: seriesTitle}
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// fullName собирает "Lastname Firstname Middlename" пропуская пустые куски.
func fullName(a AuthorRef) string {
	parts := make([]string, 0, 3)
	if a.LastName != "" {
		parts = append(parts, a.LastName)
	}
	if a.FirstName != "" {
		parts = append(parts, a.FirstName)
	}
	if a.MiddleName != "" {
		parts = append(parts, a.MiddleName)
	}
	return strings.Join(parts, " ")
}

func pickGenreDisplay(g GenreRef) string {
	switch {
	case g.NameRu != "" && g.NameRu != g.Code:
		return g.NameRu
	case g.NameEn != "" && g.NameEn != g.Code:
		return g.NameEn
	default:
		return g.Code
	}
}

func clampInt(v, lo, hi, def int) int {
	if v == 0 {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
