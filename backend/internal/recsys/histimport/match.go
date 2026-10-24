package histimport

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/skriptes/skriptes/backend/internal/metadata"
	"github.com/skriptes/skriptes/backend/internal/recsys"
	"github.com/skriptes/skriptes/backend/internal/textnorm"
)

// Сопоставление книг из файла с работами каталога (recsys/04, 4.3). Точность
// важнее полноты (грабля 13): «уверенно» — только когда название совпало точно
// и автор — тем же гейтом имени, что у обогащения (нестрогое имя — только с
// этим точным названием, как MatchLoose), и такая работа одна; иначе — до трёх
// кандидатов на выбор пользователю; ISBN — лишь подсказка (в fb2 его почти нет).
// Сухой прогон на проде (cmd/importdry) показал две ловушки, они — правила
// classify: подзаголовок («451° по Фаренгейту. Повести. Рассказы» — сборник)
// и русское название, совпавшее с оригинальным названием английского издания
// (src_title «Пикник на обочине» у «Roadside Picnic»), — «на выбор».

// Confidence — насколько уверенно сопоставлено.
type Confidence string

const (
	ConfidenceHigh   Confidence = "high"   // одна работа, совпали название и автор
	ConfidenceMedium Confidence = "medium" // кандидаты на выбор
	ConfidenceNone   Confidence = "none"   // в библиотеке не нашлось
)

// Candidate — работа каталога, подходящая к книге из файла.
type Candidate struct {
	WorkID  int64    `json:"work_id"`
	Title   string   `json:"title"`
	Authors []string `json:"authors"`
	Year    *int     `json:"year,omitempty"`
}

// Match — сопоставление одной книги файла.
type Match struct {
	Confidence Confidence `json:"confidence"`
	// WorkID — работа при high.
	WorkID int64 `json:"work_id,omitempty"`
	// Candidates — при high одна (что нашли), при medium — до maxCandidates.
	Candidates []Candidate `json:"candidates,omitempty"`
}

// TitleSearcher — запасной путь: поиск работ по названию в индексе works
// (названия оригинала и латинские имена авторов — там же), для книг, чей автор
// не нашёлся по имени (латиница против кириллицы каталога).
type TitleSearcher interface {
	SearchWorkCandidates(ctx context.Context, q string, limit int) ([]int64, error)
}

// Matcher — сопоставитель. Search может быть nil (без запасного пути).
type Matcher struct {
	DB     recsys.Querier
	Search TitleSearcher
}

const (
	authorsPerName   = 30 // авторов каталога на одно имя из файла (до проверки гейтом)
	searchCandidates = 5  // работ из поиска по названию
	maxCandidates    = 3
)

type level int

const (
	levelNone    level = 0
	levelPartial level = 1 // название: подзаголовок; автор: только фамилия
	levelLoose   level = 2 // автор: нестрогое совпадение имени (транслит) — верно только с точным названием
	levelFull    level = 3
	levelUnknown level = -1 // у книги в файле нет автора
)

type catAuthor struct {
	id                 int64
	last, first, latin string
	display            string
}

type workInfo struct {
	id      int64
	title   string
	titles  []string // работа, издания, оригиналы
	isbns   []string
	authors []catAuthor
	year    *int
	eds     int
}

// Match сопоставляет книги файла (в том же порядке). ex — скрытое у
// пользователя: такие работы в кандидаты не попадают (грабля 14).
func (m *Matcher) Match(ctx context.Context, items []Item, ex recsys.Exclusions) ([]Match, error) {
	names := map[string]struct{}{}
	for _, it := range items {
		for _, a := range it.Authors {
			if n := foldName(a); n != "" {
				names[n] = struct{}{}
			}
		}
	}
	byName, err := m.authorsByName(ctx, names)
	if err != nil {
		return nil, err
	}

	// Кандидаты: работы авторов, прошедших гейт имени, и издания с тем же ISBN.
	authorIDs := map[int64]struct{}{}
	for _, it := range items {
		for _, a := range it.Authors {
			for _, ca := range byName[foldName(a)] {
				if authorLevel(ca, a) > levelNone {
					authorIDs[ca.id] = struct{}{}
				}
			}
		}
	}
	works, err := m.worksByAuthors(ctx, keys(authorIDs))
	if err != nil {
		return nil, err
	}
	var isbns []string
	for _, it := range items {
		isbns = append(isbns, it.ISBNs...)
	}
	byISBN, err := m.worksByISBN(ctx, isbns)
	if err != nil {
		return nil, err
	}
	if err := m.load(ctx, works, flatten(byISBN), ex); err != nil {
		return nil, err
	}

	byAuthor := map[int64][]*workInfo{}
	for _, w := range works {
		for _, a := range w.authors {
			byAuthor[a.id] = append(byAuthor[a.id], w)
		}
	}
	out := make([]Match, len(items))
	for i, it := range items {
		out[i] = classify(it, candidatesOf(it, works, byAuthor, byName, byISBN))
	}

	// Запасной путь — поиск по названию для книг, которым по автору и ISBN не
	// нашлось уверенного совпадения: новые кандидаты (переводное название,
	// латинское имя, автор, которого гейт не взял) — к прежним.
	if m.Search == nil {
		return out, nil
	}
	found := map[int][]int64{}
	var more []int64
	for i, it := range items {
		if out[i].Confidence == ConfidenceHigh {
			continue
		}
		q := searchQuery(it)
		if q == "" {
			continue
		}
		ids, err := m.Search.SearchWorkCandidates(ctx, q, searchCandidates)
		if err != nil {
			return nil, fmt.Errorf("histimport: поиск %q: %w", q, err)
		}
		found[i] = ids
		more = append(more, ids...)
	}
	if len(more) == 0 {
		return out, nil
	}
	if err := m.load(ctx, works, more, ex); err != nil {
		return nil, err
	}
	for i, ids := range found {
		cands := candidatesOf(items[i], works, byAuthor, byName, byISBN)
		for _, id := range ids {
			if w := works[id]; w != nil && !slices.Contains(cands, w) {
				cands = append(cands, w)
			}
		}
		out[i] = classify(items[i], cands)
	}
	return out, nil
}

// classify — уверенность по кандидатам книги.
func classify(it Item, cands []*workInfo) Match {
	type scored struct {
		w    *workInfo
		t, a level
		isbn bool
		// foreign — книга в файле названа по-русски, а у работы своё название
		// не кириллицей: совпали через название оригинала (src_title) с
		// переводом на другой язык — читали, скорее, оригинал.
		foreign bool
	}
	cyr := hasCyrillic(it.Title)
	var ss []scored
	for _, w := range cands {
		s := scored{w: w, t: titleLevel(it.Title, w.titles), a: worksAuthorLevel(it.Authors, w.authors),
			foreign: cyr && !hasCyrillic(w.title)}
		for _, isbn := range it.ISBNs {
			if slices.Contains(w.isbns, isbn) {
				s.isbn = true
			}
		}
		// Кандидат — точное название, подзаголовок у того же автора (хотя бы
		// фамилия) или ISBN. Подзаголовок у чужого автора — шум с прода:
		// «Пикник на обочине с Малышом», «„Пикник на обочине“ Братьев Стругацких».
		if s.t == levelFull || (s.t == levelPartial && s.a >= levelPartial) || s.isbn {
			ss = append(ss, s)
		}
	}
	if len(ss) == 0 {
		return Match{Confidence: ConfidenceNone}
	}
	sort.SliceStable(ss, func(i, j int) bool {
		a, b := ss[i], ss[j]
		if a.foreign != b.foreign {
			return b.foreign
		}
		if a.a != b.a {
			return a.a > b.a
		}
		if a.t != b.t {
			return a.t > b.t
		}
		if a.isbn != b.isbn {
			return a.isbn
		}
		if a.w.eds != b.w.eds {
			return a.w.eds > b.w.eds
		}
		return a.w.id < b.w.id
	})
	// Уверенно: название совпало точно, автор — полностью или нестрого (с
	// точным названием это и есть подтверждение книгой), работа не «чужая» по
	// языку, и такая работа одна.
	var sure []scored
	for _, s := range ss {
		if s.t == levelFull && s.a >= levelLoose && !s.foreign {
			sure = append(sure, s)
		}
	}
	if len(sure) == 1 {
		return Match{Confidence: ConfidenceHigh, WorkID: sure[0].w.id, Candidates: []Candidate{sure[0].w.candidate()}}
	}
	out := Match{Confidence: ConfidenceMedium}
	for _, s := range ss {
		if len(out.Candidates) == maxCandidates {
			break
		}
		out.Candidates = append(out.Candidates, s.w.candidate())
	}
	return out
}

func (w *workInfo) candidate() Candidate {
	c := Candidate{WorkID: w.id, Title: w.title, Year: w.year, Authors: []string{}}
	for _, a := range w.authors {
		if !slices.Contains(c.Authors, a.display) {
			c.Authors = append(c.Authors, a.display)
		}
	}
	return c
}

// candidatesOf — работы-кандидаты книги: её авторов (прошедших гейт) и по ISBN.
func candidatesOf(it Item, works map[int64]*workInfo, byAuthor map[int64][]*workInfo, byName map[string][]catAuthor, byISBN map[string][]int64) []*workInfo {
	seen := map[int64]bool{}
	var out []*workInfo
	add := func(id int64) {
		if w := works[id]; w != nil && !seen[id] {
			seen[id] = true
			out = append(out, w)
		}
	}
	for _, a := range it.Authors {
		for _, ca := range byName[foldName(a)] {
			if authorLevel(ca, a) > levelNone {
				for _, w := range byAuthor[ca.id] {
					add(w.id)
				}
			}
		}
	}
	for _, isbn := range it.ISBNs {
		for _, id := range byISBN[isbn] {
			add(id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].id < out[j].id })
	return out
}

// authorLevel — автор каталога и имя из файла: полное совпадение (фамилия и
// имя — гейт обогащения, или латинское имя из переводов словами), нестрогое
// (транслит: Dostoevsky/Достоевский), только фамилия или ничего.
func authorLevel(ca catAuthor, name string) level {
	switch {
	case metadata.AuthorNameMatches(ca.last, ca.first, name) || sameWords(ca.latin, name):
		return levelFull
	case metadata.AuthorLooseMatches(ca.last, ca.first, name):
		return levelLoose
	case metadata.AuthorSurnameMatches(ca.last, name):
		return levelPartial
	}
	return levelNone
}

func hasCyrillic(s string) bool {
	for _, r := range s {
		if unicode.Is(unicode.Cyrillic, r) {
			return true
		}
	}
	return false
}

// worksAuthorLevel — лучшее совпадение авторов работы с авторами книги файла.
func worksAuthorLevel(names []string, authors []catAuthor) level {
	if len(names) == 0 {
		return levelUnknown
	}
	best := levelNone
	for _, n := range names {
		for _, a := range authors {
			if l := authorLevel(a, n); l > best {
				best = l
			}
		}
	}
	return best
}

func sameWords(a, b string) bool {
	wa, wb := strings.Fields(foldKey(a)), strings.Fields(foldKey(b))
	if len(wa) < 2 || len(wa) != len(wb) {
		return false
	}
	slices.Sort(wa)
	slices.Sort(wb)
	return slices.Equal(wa, wb)
}

var (
	trailingParensRe = regexp.MustCompile(`\s*[(\[][^()\[\]]*[)\]]\s*$`)
	subtitleSepRe    = regexp.MustCompile(`\s*[:.]\s+|\s+[-–—]\s+`)
)

// titleVariants — название из файла и его формы без хвоста: «(The Kingkiller
// Chronicle, #1)» у Goodreads, «(Russian Edition)» у Kindle, подзаголовок
// после двоеточия.
func titleVariants(title string) []string {
	out := []string{title}
	t := title
	for trailingParensRe.MatchString(t) {
		t = trailingParensRe.ReplaceAllString(t, "")
		if t != "" && !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	if loc := subtitleSepRe.FindStringIndex(t); loc != nil && loc[0] > 0 {
		if head := t[:loc[0]]; !slices.Contains(out, head) {
			out = append(out, head)
		}
	}
	return out
}

// titleLevel — название из файла против названий работы: равны по ключу (в
// любом варианте) — полностью, одно — начало другого с подзаголовком — частично.
func titleLevel(title string, titles []string) level {
	best := levelNone
	for _, v := range titleVariants(title) {
		kv := metadata.WorkTitleKey(v)
		if kv == "" {
			continue
		}
		for _, t := range titles {
			if kv == metadata.WorkTitleKey(t) {
				return levelFull
			}
			if metadata.TitlesMatch(v, t) {
				best = levelPartial
			}
		}
	}
	return best
}

// searchQuery — запрос запасного поиска: название без хвостов и первый автор
// (ранжирует нужную работу выше одноимённых; Meili отбрасывает несовпавшие
// слова с конца).
func searchQuery(it Item) string {
	vs := titleVariants(it.Title)
	q := strings.TrimSpace(vs[len(vs)-1])
	if len([]rune(q)) < 2 {
		return ""
	}
	if len(it.Authors) > 0 {
		q += " " + it.Authors[0]
	}
	return q
}

// foldName — имя для поиска по триграммам: нижний регистр и та же свёртка
// «ё/э/й», что у индекса authors_name_fold_trgm.
func foldName(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(textnorm.FoldName(strings.ToLower(s)))), " ")
}

var authorFold = textnorm.NameFoldSQL("a.normalized_name")

// authorsByName — авторы каталога, похожие на имена из файла (триграммы по
// свёрнутому имени и по латинскому имени из переводов); проверка — гейтом.
func (m *Matcher) authorsByName(ctx context.Context, names map[string]struct{}) (map[string][]catAuthor, error) {
	out := map[string][]catAuthor{}
	if len(names) == 0 {
		return out, nil
	}
	rows, err := m.DB.Query(ctx, `
		SELECT q.name, a.id, a.last_name, a.first_name, COALESCE(a.latin_name, ''),
		       TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name))
		FROM unnest($1::text[]) AS q(name)
		CROSS JOIN LATERAL (
			SELECT a.* FROM authors a
			WHERE (`+authorFold+` % q.name OR a.latin_name % q.name) AND NOT a.instance_hidden
			ORDER BY GREATEST(similarity(`+authorFold+`, q.name), COALESCE(similarity(a.latin_name, q.name), 0)) DESC, a.id
			LIMIT `+fmt.Sprint(authorsPerName)+`
		) a`, keysS(names))
	if err != nil {
		return nil, fmt.Errorf("histimport: авторы по имени: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			name string
			a    catAuthor
		)
		if err := rows.Scan(&name, &a.id, &a.last, &a.first, &a.latin, &a.display); err != nil {
			return nil, err
		}
		out[name] = append(out[name], a)
	}
	return out, rows.Err()
}

// worksByAuthors — пустые записи работ этих авторов (детали — load).
func (m *Matcher) worksByAuthors(ctx context.Context, authorIDs []int64) (map[int64]*workInfo, error) {
	works := map[int64]*workInfo{}
	if len(authorIDs) == 0 {
		return works, nil
	}
	rows, err := m.DB.Query(ctx, `
		SELECT DISTINCT b.work_id FROM book_authors ba JOIN books b ON b.id = ba.book_id
		WHERE ba.author_id = ANY($1) AND NOT b.deleted AND b.work_id IS NOT NULL`, authorIDs)
	if err != nil {
		return nil, fmt.Errorf("histimport: работы авторов: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		works[id] = nil
	}
	return works, rows.Err()
}

func (m *Matcher) worksByISBN(ctx context.Context, isbns []string) (map[string][]int64, error) {
	out := map[string][]int64{}
	if len(isbns) == 0 {
		return out, nil
	}
	rows, err := m.DB.Query(ctx, `
		SELECT DISTINCT isbn, work_id FROM books
		WHERE isbn = ANY($1) AND NOT deleted AND work_id IS NOT NULL`, isbns)
	if err != nil {
		return nil, fmt.Errorf("histimport: ISBN: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			isbn string
			id   int64
		)
		if err := rows.Scan(&isbn, &id); err != nil {
			return nil, err
		}
		out[isbn] = append(out[isbn], id)
	}
	return out, rows.Err()
}

// load заполняет works деталями — работы id из works (ещё пустые) и extra;
// скрытые у пользователя и на инстансе — удаляет.
func (m *Matcher) load(ctx context.Context, works map[int64]*workInfo, extra []int64, ex recsys.Exclusions) error {
	var ids []int64
	for id, w := range works {
		if w == nil {
			ids = append(ids, id)
		}
	}
	for _, id := range extra {
		if w, ok := works[id]; !ok || w == nil {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var langs, genres []string
	if len(ex.Langs) > 0 {
		langs = ex.Langs
	}
	if len(ex.Genres) > 0 {
		genres = ex.Genres
	}
	// Издание видно, если живое, не скрыто на инстансе и не скрыто у
	// пользователя по языку или жанру (зеркало recsys.Hydrate); работа — если
	// у неё есть видимое издание.
	rows, err := m.DB.Query(ctx, `
		SELECT w.id, w.title, w.written_year::int, count(DISTINCT b.id)::int,
		       array_agg(DISTINCT b.title), array_remove(array_agg(DISTINCT b.src_title), NULL),
		       array_remove(array_agg(DISTINCT b.isbn), NULL)
		FROM works w JOIN books b ON b.work_id = w.id
		WHERE w.id = ANY($1) AND NOT w.instance_hidden
		  AND (NOT $4::bool OR COALESCE(w.kind, '') = '')
		  AND NOT b.deleted AND NOT b.instance_hidden
		  AND ($2::text[] IS NULL OR b.lang IS NULL OR NOT (b.lang = ANY($2::text[])))
		  AND ($3::text[] IS NULL OR NOT EXISTS (
		      SELECT 1 FROM book_genres bgx JOIN genres gx ON gx.id = bgx.genre_id
		      WHERE bgx.book_id = b.id AND gx.fb2_code = ANY($3::text[])))
		GROUP BY w.id`, ids, langs, genres, ex.HideCompilations)
	if err != nil {
		return fmt.Errorf("histimport: работы: %w", err)
	}
	loaded := map[int64]*workInfo{}
	err = forEach(rows, func(r pgx.Rows) error {
		w := &workInfo{}
		var year *int
		var eds, src, isbns []string
		if err := r.Scan(&w.id, &w.title, &year, &w.eds, &eds, &src, &isbns); err != nil {
			return err
		}
		w.year = year
		w.titles = append(append([]string{w.title}, eds...), src...)
		w.isbns = isbns
		loaded[w.id] = w
		return nil
	})
	if err != nil {
		return err
	}
	for _, id := range ids {
		delete(works, id)
	}
	if len(loaded) == 0 {
		return nil
	}
	rows, err = m.DB.Query(ctx, `
		SELECT DISTINCT b.work_id, a.id, a.last_name, a.first_name, COALESCE(a.latin_name, ''),
		       TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name))
		FROM books b JOIN book_authors ba ON ba.book_id = b.id JOIN authors a ON a.id = ba.author_id
		WHERE b.work_id = ANY($1) AND NOT b.deleted
		ORDER BY 1, 2`, keys(loaded))
	if err != nil {
		return fmt.Errorf("histimport: авторы работ: %w", err)
	}
	err = forEach(rows, func(r pgx.Rows) error {
		var (
			wid int64
			a   catAuthor
		)
		if err := r.Scan(&wid, &a.id, &a.last, &a.first, &a.latin, &a.display); err != nil {
			return err
		}
		loaded[wid].authors = append(loaded[wid].authors, a)
		return nil
	})
	if err != nil {
		return err
	}
	for id, w := range loaded {
		works[id] = w
	}
	return nil
}

func forEach(rows pgx.Rows, fn func(pgx.Rows) error) error {
	defer rows.Close()
	for rows.Next() {
		if err := fn(rows); err != nil {
			return err
		}
	}
	return rows.Err()
}

func keys[V any](m map[int64]V) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func keysS(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func flatten(m map[string][]int64) []int64 {
	var out []int64
	for _, ids := range m {
		out = append(out, ids...)
	}
	return out
}
