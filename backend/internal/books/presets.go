package books

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/meilisearch/meilisearch-go"
)

// Готовые подборки (#389, A1) — системные полки на /shelves. Не хранятся:
// состав вычисляется на лету из чтения пользователя, подписок и экранизаций.
// Видимость и порядок по известности — через индекс works (фильтр `id IN`).

const (
	PresetUnfinishedSeries = "unfinished-series"
	PresetAuthorsUnread    = "authors-unread"
	PresetReadThisYear     = "read-this-year"
	PresetAdaptations      = "adaptations"
)

// presetOrder — порядок подборок на странице.
var presetOrder = []string{PresetUnfinishedSeries, PresetAuthorsUnread, PresetReadThisYear, PresetAdaptations}

const (
	// presetMaxItems — потолок одной подборки (она не листается).
	presetMaxItems = 100
	// presetPerAuthor — не больше стольких работ одного автора в «Непрочитанном
	// у любимых авторов»: иначе плодовитый автор займёт всю подборку.
	presetPerAuthor = 5
	// presetAuthorReads — автор «любимый», если прочитано столько его работ
	// (кроме явной подписки).
	presetAuthorReads = 3
	// presetAuthorCandidates — сколько работ авторов брать из индекса до
	// ограничения по автору.
	presetAuthorCandidates = 400
)

// ErrUnknownPreset — нет подборки с таким ключом.
var ErrUnknownPreset = errors.New("unknown preset")

// Preset — подборка в списке полок.
type Preset struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	Hint  string `json:"hint"`
	Count int    `json:"count"`
}

// PresetResult — книги подборки; Notes — пояснение к работе (id работы →
// «прочитано 2 из 7», «экранизация 2027: …»).
type PresetResult struct {
	Items []ListItem       `json:"items"`
	Notes map[int64]string `json:"notes,omitempty"`
}

// PresetParams — кто смотрит и что ему скрыто.
type PresetParams struct {
	UserID           int64
	ExcludeGenres    []string
	ExcludeLangs     []string
	HideCompilations bool
	Now              time.Time
}

// presetEntry — работа подборки до проверки видимости.
type presetEntry struct {
	workID int64
	note   string
}

func presetMeta(key string, now time.Time) (title, hint string) {
	switch key {
	case PresetUnfinishedSeries:
		return "Недочитанные серии", "Следующая книга в сериях, которые вы начали"
	case PresetAuthorsUnread:
		return "Непрочитанное у любимых авторов", "Самые известные непрочитанные книги авторов из подписок и тех, кого вы прочли трижды"
	case PresetReadThisYear:
		return fmt.Sprintf("Прочитано в %d году", now.Year()), "Книги, отмеченные прочитанными в этом году"
	case PresetAdaptations:
		return fmt.Sprintf("Экранизации %d–%d", now.Year(), now.Year()+1), "Книги, по которым выходят фильмы и сериалы в этом и следующем году"
	}
	return "", ""
}

// Presets — подборки пользователя со счётчиками.
func (s *Service) Presets(ctx context.Context, p PresetParams) ([]Preset, error) {
	out := make([]Preset, 0, len(presetOrder))
	for _, key := range presetOrder {
		items, _, err := s.resolvePreset(ctx, key, p)
		if err != nil {
			return nil, err
		}
		title, hint := presetMeta(key, p.Now)
		out = append(out, Preset{Key: key, Title: title, Hint: hint, Count: len(items)})
	}
	return out, nil
}

// PresetWorks — книги одной подборки с плашкой сигналов, как в /books.
func (s *Service) PresetWorks(ctx context.Context, key string, p PresetParams) (PresetResult, error) {
	items, notes, err := s.resolvePreset(ctx, key, p)
	if err != nil {
		return PresetResult{}, err
	}
	HydrateListMeta(ctx, s.pool, items)
	ex := presetExclusions(key, p)
	s.hydrateWorkRepresentative(ctx, items, ex.ExcludeGenres, ex.ExcludeLangs)
	return PresetResult{Items: items, Notes: notes}, nil
}

// presetExclusions — собственное чтение («Прочитано в году») показываем как
// есть, как книги на своей полке; остальные подборки предлагают книги и
// уважают скрытое.
func presetExclusions(key string, p PresetParams) PresetParams {
	if key == PresetReadThisYear {
		return PresetParams{UserID: p.UserID, Now: p.Now}
	}
	return p
}

// resolvePreset — видимые работы подборки по порядку и пояснения к ним.
func (s *Service) resolvePreset(ctx context.Context, key string, p PresetParams) ([]ListItem, map[int64]string, error) {
	if p.Now.IsZero() {
		p.Now = time.Now()
	}
	var (
		entries []presetEntry
		err     error
		byPop   bool // порядок — по известности (иначе — как отдал SQL)
	)
	switch key {
	case PresetUnfinishedSeries:
		entries, err = s.unfinishedSeries(ctx, p.UserID)
	case PresetAuthorsUnread:
		return s.authorsUnread(ctx, p)
	case PresetReadThisYear:
		entries, err = s.readThisYear(ctx, p.UserID, p.Now)
	case PresetAdaptations:
		entries, err = s.upcomingAdaptations(ctx, p.Now)
		byPop = true
	default:
		return nil, nil, ErrUnknownPreset
	}
	if err != nil {
		return nil, nil, err
	}
	items, err := s.visibleWorks(ctx, entries, presetExclusions(key, p), byPop)
	if err != nil {
		return nil, nil, err
	}
	notes := make(map[int64]string, len(items))
	byID := make(map[int64]string, len(entries))
	for _, e := range entries {
		byID[e.workID] = e.note
	}
	for _, it := range items {
		if n := byID[it.ID]; n != "" {
			notes[it.ID] = n
		}
	}
	return items, notes, nil
}

// visibleWorks — документы индекса works для работ подборки с учётом скрытого.
// byPop — по известности, иначе в порядке entries.
func (s *Service) visibleWorks(ctx context.Context, entries []presetEntry, p PresetParams, byPop bool) ([]ListItem, error) {
	if len(entries) == 0 || s.meili == nil {
		return []ListItem{}, nil
	}
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, strconv.FormatInt(e.workID, 10))
	}
	var visibleLangs []string
	if len(p.ExcludeLangs) > 0 {
		visibleLangs = s.allLangs(ctx)
	}
	req := &meilisearch.SearchRequest{
		Limit:  int64(len(entries)),
		Filter: andFilter("id IN ["+strings.Join(ids, ", ")+"]", worksExclusionFilter(p.ExcludeGenres, p.ExcludeLangs, visibleLangs, p.HideCompilations)),
	}
	if byPop {
		req.Sort = popularitySort
	}
	res, err := s.meili.Index(worksIndexName).SearchWithContext(ctx, "", req)
	if err != nil {
		return nil, fmt.Errorf("meili works (preset): %w", err)
	}
	hits := decodeWorkHits(res.Hits)
	out := make([]ListItem, 0, len(hits))
	if byPop {
		for _, h := range hits {
			out = append(out, h.toListItem())
		}
		return out, nil
	}
	found := make(map[int64]workHit, len(hits))
	for _, h := range hits {
		found[h.ID] = h
	}
	for _, e := range entries {
		if h, ok := found[e.workID]; ok {
			out = append(out, h.toListItem())
		}
	}
	return out, nil
}

// readWorksCTE — работы, отмеченные пользователем ($1) прочитанными.
const readWorksCTE = `rw AS (
	SELECT b.work_id, max(r.completed_at) AS read_at
	FROM reads r JOIN books b ON b.id = r.book_id
	WHERE r.user_id = $1 AND r.completed_at IS NOT NULL AND b.work_id IS NOT NULL
	GROUP BY b.work_id
)`

// unfinishedSeries — по одной книге из каждой начатой авторской серии: первая
// непрочитанная после самой старшей прочитанной (по номеру, затем году), иначе —
// первая непрочитанная (пропущенные тома). Межавторские серии — не циклы.
// Свежие по чтению серии — сверху.
func (s *Service) unfinishedSeries(ctx context.Context, userID int64) ([]presetEntry, error) {
	rows, err := s.pool.Query(ctx, `
		WITH `+readWorksCTE+`,
		ser AS (
			SELECT w.series_id, max(w.ser_no) AS max_no, max(rw.read_at) AS last_read, count(*) AS read_n
			FROM works w
			JOIN rw ON rw.work_id = w.id
			JOIN series se ON se.id = w.series_id AND se.kind IS DISTINCT FROM 'multi'
			WHERE COALESCE(w.kind, '') = ''
			GROUP BY w.series_id
		),
		cand AS (
			SELECT w.id, w.series_id,
			       row_number() OVER (PARTITION BY w.series_id ORDER BY
			           (w.ser_no IS NOT NULL AND ser.max_no IS NOT NULL AND w.ser_no > ser.max_no) DESC,
			           w.ser_no NULLS LAST, w.written_year NULLS LAST, w.id) AS rn
			FROM works w
			JOIN ser ON ser.series_id = w.series_id
			WHERE COALESCE(w.kind, '') = ''
			  AND NOT EXISTS (SELECT 1 FROM rw WHERE rw.work_id = w.id)
			  AND EXISTS (SELECT 1 FROM books b WHERE b.work_id = w.id AND NOT b.deleted)
		),
		total AS (
			SELECT w.series_id, count(*) AS n
			FROM works w JOIN ser ON ser.series_id = w.series_id
			WHERE COALESCE(w.kind, '') = ''
			  AND EXISTS (SELECT 1 FROM books b WHERE b.work_id = w.id AND NOT b.deleted)
			GROUP BY w.series_id
		)
		SELECT cand.id, ser.read_n, total.n
		FROM cand
		JOIN ser ON ser.series_id = cand.series_id
		JOIN total ON total.series_id = cand.series_id
		WHERE cand.rn = 1
		ORDER BY ser.last_read DESC, cand.id
		LIMIT $2`, userID, presetMaxItems)
	if err != nil {
		return nil, fmt.Errorf("unfinished series: %w", err)
	}
	defer rows.Close()
	var out []presetEntry
	for rows.Next() {
		var (
			e           presetEntry
			read, total int
		)
		if err := rows.Scan(&e.workID, &read, &total); err != nil {
			return nil, err
		}
		e.note = fmt.Sprintf("в серии прочитано %d из %d", read, total)
		out = append(out, e)
	}
	return out, rows.Err()
}

// readThisYear — работы, отмеченные прочитанными с начала года, свежие сверху.
func (s *Service) readThisYear(ctx context.Context, userID int64, now time.Time) ([]presetEntry, error) {
	from := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location())
	rows, err := s.pool.Query(ctx, `
		SELECT b.work_id, max(r.completed_at) AS read_at
		FROM reads r JOIN books b ON b.id = r.book_id
		WHERE r.user_id = $1 AND r.completed_at >= $2 AND b.work_id IS NOT NULL
		GROUP BY b.work_id
		ORDER BY read_at DESC, b.work_id
		LIMIT $3`, userID, from, presetMaxItems)
	if err != nil {
		return nil, fmt.Errorf("read this year: %w", err)
	}
	defer rows.Close()
	var out []presetEntry
	for rows.Next() {
		var (
			e  presetEntry
			at time.Time
		)
		if err := rows.Scan(&e.workID, &at); err != nil {
			return nil, err
		}
		e.note = "прочитано " + ruDayMonth(at.In(now.Location()))
		out = append(out, e)
	}
	return out, rows.Err()
}

// upcomingAdaptations — работы с экранизацией этого или следующего года.
func (s *Service) upcomingAdaptations(ctx context.Context, now time.Time) ([]presetEntry, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT b.work_id, min(ba.year)::int,
		       (array_agg(ba.title ORDER BY ba.year, ba.title))[1]
		FROM book_adaptations ba
		JOIN books b ON b.id = ba.book_id AND NOT b.deleted
		WHERE ba.year BETWEEN $1 AND $1 + 1 AND b.work_id IS NOT NULL
		GROUP BY b.work_id`, now.Year())
	if err != nil {
		return nil, fmt.Errorf("upcoming adaptations: %w", err)
	}
	defer rows.Close()
	var out []presetEntry
	for rows.Next() {
		var (
			e     presetEntry
			year  int
			title string
		)
		if err := rows.Scan(&e.workID, &year, &title); err != nil {
			return nil, err
		}
		e.note = fmt.Sprintf("%d · «%s»", year, title)
		out = append(out, e)
	}
	return out, rows.Err()
}

// authorsUnread — самые известные непрочитанные работы «любимых» авторов:
// подписка или прочитано ≥ presetAuthorReads работ (служебные авторы — нет).
// Не больше presetPerAuthor работ на автора, сборники не предлагаем.
func (s *Service) authorsUnread(ctx context.Context, p PresetParams) ([]ListItem, map[int64]string, error) {
	rows, err := s.pool.Query(ctx, `
		WITH `+readWorksCTE+`,
		fav AS (
			SELECT fa.author_id FROM favorite_authors fa WHERE fa.user_id = $1
			UNION
			SELECT w.primary_author_id FROM works w JOIN rw ON rw.work_id = w.id
			WHERE w.primary_author_id IS NOT NULL AND COALESCE(w.kind, '') = ''
			GROUP BY w.primary_author_id HAVING count(*) >= $2
		)
		SELECT a.id FROM fav JOIN authors a ON a.id = fav.author_id AND NOT a.is_service`, p.UserID, presetAuthorReads)
	if err != nil {
		return nil, nil, fmt.Errorf("favorite authors: %w", err)
	}
	authorIDs := make([]string, 0)
	authorSet := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, nil, err
		}
		authorIDs = append(authorIDs, strconv.FormatInt(id, 10))
		authorSet[id] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	if len(authorIDs) == 0 || s.meili == nil {
		return []ListItem{}, nil, nil
	}
	// Прочитанное — только среди работ этих авторов (фильтр не разрастается).
	readIDs, err := s.readWorksOfAuthors(ctx, p.UserID, authorSet)
	if err != nil {
		return nil, nil, err
	}
	var visibleLangs []string
	if len(p.ExcludeLangs) > 0 {
		visibleLangs = s.allLangs(ctx)
	}
	notRead := ""
	if len(readIDs) > 0 {
		notRead = "NOT id IN [" + strings.Join(readIDs, ", ") + "]"
	}
	req := &meilisearch.SearchRequest{
		Limit: presetAuthorCandidates,
		Sort:  popularitySort,
		Filter: andFilter("author_ids IN ["+strings.Join(authorIDs, ", ")+"]", notRead, compilationsExclusion,
			worksExclusionFilter(p.ExcludeGenres, p.ExcludeLangs, visibleLangs, false)),
	}
	res, err := s.meili.Index(worksIndexName).SearchWithContext(ctx, "", req)
	if err != nil {
		return nil, nil, fmt.Errorf("meili works (authors unread): %w", err)
	}
	perAuthor := make(map[int64]int)
	items := make([]ListItem, 0, presetMaxItems)
	for _, h := range decodeWorkHits(res.Hits) {
		a := firstListedAuthor(h.AuthorIDs, authorSet)
		if a == 0 || perAuthor[a] >= presetPerAuthor {
			continue
		}
		perAuthor[a]++
		items = append(items, h.toListItem())
		if len(items) >= presetMaxItems {
			break
		}
	}
	return items, nil, nil
}

// firstListedAuthor — первый из авторов работы, входящий в набор (0 — нет).
func firstListedAuthor(ids []int64, set map[int64]bool) int64 {
	for _, id := range ids {
		if set[id] {
			return id
		}
	}
	return 0
}

// readWorksOfAuthors — прочитанные пользователем работы данных авторов (id строкой).
func (s *Service) readWorksOfAuthors(ctx context.Context, userID int64, authors map[int64]bool) ([]string, error) {
	ids := make([]int64, 0, len(authors))
	for id := range authors {
		ids = append(ids, id)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT b.work_id
		FROM reads r
		JOIN books b ON b.id = r.book_id AND b.work_id IS NOT NULL
		JOIN book_authors ba ON ba.book_id = b.id
		WHERE r.user_id = $1 AND r.completed_at IS NOT NULL AND ba.author_id = ANY($2)`, userID, ids)
	if err != nil {
		return nil, fmt.Errorf("read works of authors: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, strconv.FormatInt(id, 10))
	}
	return out, rows.Err()
}

var ruMonthsGenitive = [...]string{"января", "февраля", "марта", "апреля", "мая", "июня",
	"июля", "августа", "сентября", "октября", "ноября", "декабря"}

// ruDayMonth — «12 марта».
func ruDayMonth(t time.Time) string {
	return fmt.Sprintf("%d %s", t.Day(), ruMonthsGenitive[t.Month()-1])
}
