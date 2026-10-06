package metadata

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/html/charset"
)

// Состав сборников из оглавления fb2 (#388, миграция 0051). Сборник — работа с
// works.kind; его состав — разделы <body> издания-якоря по порядку. Строка
// связывается с отдельной работой каталога, только если её название точно (после
// нормализации) совпало с работой одного из авторов сборника и кандидат один:
// на выборке прода точность 29/30, связывается 14–20 % строк — остальных
// рассказов в каталоге отдельно нет.

// compilationKindsSQL — типы работ-сборников (works.kind).
const compilationKindsSQL = `('collection', 'anthology', 'omnibus')`

// maxContentsEntries — больше строк не храним: у собраний стихов их тысячи.
const maxContentsEntries = 500

type tocNode struct {
	title    string
	children []tocNode
}

// scanFb2Contents — дерево разделов тел книги (до глубины 3). Тела с атрибутом
// name (примечания, комментарии) пропускаются. Битый XML не ошибка: возвращается
// то, что успели прочитать.
func scanFb2Contents(r io.Reader) []tocNode {
	dec := xml.NewDecoder(r)
	dec.CharsetReader = charset.NewReaderLabel
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity

	var roots []tocNode
	// stack[i] — открытый раздел глубины i+1; nil-указатели не используем —
	// дерево собираем из срезов при закрытии раздела.
	type open struct {
		node  tocNode
		depth int
	}
	var stack []open
	inBody, skipBody := false, false
	sectionDepth := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "body":
				inBody, skipBody = true, attrValue(t, "name") != ""
				sectionDepth = 0
			case "section":
				if !inBody || skipBody {
					continue
				}
				sectionDepth++
				if sectionDepth <= 3 {
					stack = append(stack, open{depth: sectionDepth})
				}
			case "title":
				if inBody && !skipBody && sectionDepth > 0 && sectionDepth <= 3 && len(stack) > 0 {
					top := &stack[len(stack)-1]
					if top.depth == sectionDepth && top.node.title == "" {
						top.node.title = titleText(dec)
					}
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "body":
				inBody, skipBody = false, false
			case "section":
				if !inBody || skipBody || sectionDepth == 0 {
					continue
				}
				if sectionDepth <= 3 && len(stack) > 0 {
					done := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					if len(stack) > 0 {
						parent := &stack[len(stack)-1]
						parent.node.children = append(parent.node.children, done.node)
					} else {
						roots = append(roots, done.node)
					}
				}
				sectionDepth--
			}
		}
	}
	return roots
}

// titleText — текст <title> раздела: абзацы через пробел (elemText склеил бы
// «Часть первая» и название без пробела).
func titleText(dec *xml.Decoder) string {
	var b strings.Builder
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.StartElement:
			depth++
			if t.Name.Local == "p" {
				b.WriteByte(' ')
			}
		case xml.EndElement:
			depth--
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// contentAuthor — автор сборника для сопоставления строк оглавления.
type contentAuthor struct {
	id    int64
	forms []string // ключи имени: «фамилия», «имя фамилия», «фамилия имя»
}

func newContentAuthor(id int64, last, first string) contentAuthor {
	l, f := contentKey(last), ""
	if w := strings.Fields(first); len(w) > 0 {
		f = contentKey(w[0]) // имя — первое слово поля (второе имя, инициал — нет)
	}
	a := contentAuthor{id: id}
	if l != "" {
		a.forms = append(a.forms, l)
		if f != "" {
			a.forms = append(a.forms, f+" "+l, l+" "+f)
		}
	}
	return a
}

// contentEntry — строка оглавления; authorID — автор из заголовка-группы
// (антология по авторам), 0 — любой автор сборника.
type contentEntry struct {
	title    string
	authorID int64
}

// contentsEntries — строки «Состава» из дерева разделов: одна обёртка —
// уровнем ниже; заголовок-группа («Повести», «Часть 2») или имя автора сборника —
// его подразделы; служебные заголовки и пустые — мимо.
func contentsEntries(roots []tocNode, compilationTitle string, authors []contentAuthor) []contentEntry {
	top := roots
	for len(top) == 1 && len(top[0].children) > 0 {
		top = top[0].children
	}
	selfKey := contentKey(compilationTitle)
	var out []contentEntry
	add := func(title string, authorID int64) {
		k := contentKey(title)
		if k == "" || k == selfKey || isServiceContentTitle(k) || len(out) >= maxContentsEntries {
			return
		}
		out = append(out, contentEntry{title: displayContentTitle(title), authorID: authorID})
	}
	for _, n := range top {
		k := contentKey(n.title)
		switch {
		case k == "" && len(n.children) > 0:
			for _, c := range n.children {
				add(c.title, 0)
			}
		case len(n.children) > 0 && isGroupContentTitle(k):
			for _, c := range n.children {
				add(c.title, 0)
			}
		case len(n.children) > 0 && authorByForm(authors, k) != 0:
			aid := authorByForm(authors, k)
			for _, c := range n.children {
				add(c.title, aid)
			}
		default:
			add(n.title, 0)
		}
	}
	if len(out) < 2 {
		return nil // одна строка — не оглавление сборника
	}
	return out
}

func authorByForm(authors []contentAuthor, key string) int64 {
	for _, a := range authors {
		for _, f := range a.forms {
			if f == key {
				return a.id
			}
		}
	}
	return 0
}

var (
	reFootnote   = regexp.MustCompile(`\[\d+\]|\{\d+\}`)
	reTranslator = regexp.MustCompile(`(?i)[\s(\[]+(перевод|пер\.)\s.*$`)
	reNumbering  = regexp.MustCompile(`(?i)^\s*((глава|часть|книга|том|рассказ)\s+)?([0-9]+|[ivxlcdm]+)\s*[.):\-—]\s*`)
	reChapter    = regexp.MustCompile(`(?i)^(глава|часть|книга|том)\s+([0-9]+|[ivxlcdm]+|перв\S*|втор\S*|трет\S*|четв\S*|пят\S*|шест\S*|седьм\S*|восьм\S*|девят\S*|десят\S*)$`)
)

// displayContentTitle — название строки для показа: без сносок («[58]») и строки
// переводчика в конце («Сестры Перевод М. П. Богословской» → «Сестры»).
func displayContentTitle(s string) string {
	s = reFootnote.ReplaceAllString(s, "")
	if t := strings.TrimSpace(reTranslator.ReplaceAllString(s, "")); t != "" {
		s = t
	}
	return strings.Join(strings.Fields(s), " ")
}

// reQuoted — название в «ёлочках» внутри строки («Весны извечные надежды «Рита
// Хейворт…»» в «Четырёх сезонах» Кинга).
var reQuoted = regexp.MustCompile(`«([^«»]+)»`)

// contentKey — ключ названия для сравнения: нижний регистр, ё → е, без сносок,
// «Перевод …» в конце, нумерации «1.», «IV.», «Глава 2.» в начале и пунктуации.
func contentKey(s string) string {
	s = strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(s, "ё", "е"), "Ё", "Е"))
	s = reFootnote.ReplaceAllString(s, " ")
	s = reTranslator.ReplaceAllString(s, "")
	s = reNumbering.ReplaceAllString(s, "")
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// serviceContentPrefixes — служебные разделы: не произведения.
var serviceContentPrefixes = []string{
	"предисловие", "послесловие", "вступление", "вступительная статья", "вместо предисловия",
	"вместо послесловия", "от автора", "от авторов", "от составителя", "от составителей", "от редактора",
	"от редакции", "от издательства", "от издателя", "от переводчика", "об авторе", "об авторах",
	"сведения об авторах", "коротко об авторах", "примечания", "примечание", "комментарии",
	"комментарий", "содержание", "оглавление", "библиография", "аннотация", "annotation", "contents",
	"notes", "notes and references", "приложение", "приложения", "иллюстрации", "информация",
	"благодарности", "словарь", "глоссарий", "хронология", "указатель", "список литературы",
}

func isServiceContentTitle(key string) bool {
	if reChapter.MatchString(key) {
		return true // «Глава 3», «Часть первая» без названия — глава, не произведение
	}
	for _, p := range serviceContentPrefixes {
		if key == p || strings.HasPrefix(key, p+" ") {
			return true
		}
	}
	return false
}

// groupContentWords — заголовок только из этих слов — группа разделов
// («Повести», «Повести и рассказы», «Ранние рассказы»).
var groupContentWords = map[string]bool{
	"повести": true, "повесть": true, "рассказы": true, "романы": true, "пьесы": true, "стихотворения": true,
	"стихи": true, "поэмы": true, "очерки": true, "статьи": true, "эссе": true, "сказки": true, "новеллы": true,
	"миниатюры": true, "фельетоны": true, "пьеса": true, "и": true, "ранние": true, "поздние": true,
	"другие": true, "избранные": true, "юмористические": true, "фантастические": true, "из": true,
}

func isGroupContentTitle(key string) bool {
	if reChapter.MatchString(key) {
		return true // «Книга вторая» с подразделами — группа
	}
	words := strings.Fields(key)
	if len(words) == 0 {
		return false
	}
	for _, w := range words {
		if !groupContentWords[w] {
			return false
		}
	}
	return true
}

// contentCandidate — отдельная работа одного из авторов сборника.
type contentCandidate struct {
	workID  int64
	authors []int64
}

// matchContentEntry — работа каталога для строки оглавления: точное совпадение
// ключа названия (или «Автор Название» с именем одного из авторов сборника), ровно
// один кандидат. Не нашлось или несколько — 0.
func matchContentEntry(e contentEntry, byKey map[string][]contentCandidate, authors []contentAuthor) int64 {
	key := contentKey(e.title)
	if id := uniqueCandidate(byKey[key], e.authorID); id != 0 {
		return id
	}
	// Название в «ёлочках» внутри строки — тоже название произведения.
	for _, m := range reQuoted.FindAllStringSubmatch(e.title, -1) {
		if id := uniqueCandidate(byKey[contentKey(m[1])], e.authorID); id != 0 {
			return id
		}
	}
	if e.authorID != 0 {
		return 0
	}
	// «Кинг Стивен Туман», «Стивен Кинг. Туман» — имя автора и название.
	for _, a := range authors {
		for _, f := range a.forms {
			if rest, ok := strings.CutPrefix(key, f+" "); ok {
				if id := uniqueCandidate(byKey[rest], a.id); id != 0 {
					return id
				}
			}
		}
	}
	return 0
}

func uniqueCandidate(cands []contentCandidate, authorID int64) int64 {
	var found int64
	for _, c := range cands {
		if authorID != 0 && !containsID(c.authors, authorID) {
			continue
		}
		if found != 0 && found != c.workID {
			return 0 // несколько работ с таким названием — не угадываем
		}
		found = c.workID
	}
	return found
}

func containsID(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// ContentsScanner — фоновый разбор оглавлений сборников (works.contents_scanned_at
// IS NULL) и повторная связка строк без работы после импорта.
type ContentsScanner struct {
	pool      *pgxpool.Pool
	booksRoot string
	logger    *slog.Logger
}

func NewContentsScanner(pool *pgxpool.Pool, booksRoot string, logger *slog.Logger) *ContentsScanner {
	return &ContentsScanner{pool: pool, booksRoot: booksRoot, logger: logger}
}

// Run — через delay (старт успевает отработать разовые шаги) и дальше раз в
// interval разбирает новые сборники, пока ctx жив.
func (s *ContentsScanner) Run(ctx context.Context, delay, interval time.Duration) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(delay):
	}
	for {
		if n, linked, err := s.ScanPending(ctx); err != nil && ctx.Err() == nil {
			s.logger.Warn("compilation contents: scan failed", "err", err)
		} else if n > 0 {
			s.logger.Info("compilation contents: scanned", "compilations", n, "linked", linked)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

type pendingCompilation struct {
	workID   int64
	title    string
	archive  string
	fileName string
}

// ScanPending разбирает все сборники без разобранного оглавления. Возвращает
// число разобранных и число связанных строк.
func (s *ContentsScanner) ScanPending(ctx context.Context) (scanned, linked int, err error) {
	for ctx.Err() == nil {
		batch, err := s.pending(ctx, 200)
		if err != nil {
			return scanned, linked, err
		}
		if len(batch) == 0 {
			return scanned, linked, nil
		}
		for _, c := range batch {
			if ctx.Err() != nil {
				return scanned, linked, ctx.Err()
			}
			n, err := s.scanOne(ctx, c)
			if err != nil {
				return scanned, linked, err
			}
			scanned++
			linked += n
		}
	}
	return scanned, linked, ctx.Err()
}

// pending — сборники без разобранного оглавления и их издание-якорь (название
// совпадает с работой, иначе самое раннее живое издание fb2).
func (s *ContentsScanner) pending(ctx context.Context, limit int) ([]pendingCompilation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT w.id, COALESCE(w.title, ''), a.filename, b.file_name || '.' || b.ext
		FROM works w
		CROSS JOIN LATERAL (
		    SELECT b.archive_id, b.file_name, b.ext FROM books b
		    WHERE b.work_id = w.id AND NOT b.deleted AND lower(b.ext) = 'fb2'
		    ORDER BY (b.normalized_title = w.normalized_title) DESC, b.id
		    LIMIT 1) b
		JOIN archives a ON a.id = b.archive_id
		WHERE w.kind IN `+compilationKindsSQL+` AND w.contents_scanned_at IS NULL
		ORDER BY w.id
		LIMIT $1`, limit)
	if err != nil {
		return nil, fmt.Errorf("contents pending: %w", err)
	}
	defer rows.Close()
	var out []pendingCompilation
	for rows.Next() {
		var c pendingCompilation
		if err := rows.Scan(&c.workID, &c.title, &c.archive, &c.fileName); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		// Сборники без живого fb2-издания — отметить, чтобы не выбирать снова.
		_, err = s.pool.Exec(ctx, `UPDATE works w SET contents_scanned_at = now()
			WHERE w.kind IN `+compilationKindsSQL+` AND w.contents_scanned_at IS NULL
			  AND NOT EXISTS (SELECT 1 FROM books b WHERE b.work_id = w.id AND NOT b.deleted AND lower(b.ext) = 'fb2')`)
	}
	return out, err
}

// ContentLine — строка оглавления с найденной работой (0 — без связи).
type ContentLine struct {
	Title  string `json:"title"`
	WorkID int64  `json:"work_id,omitempty"`
}

// lines — разбор оглавления и связка одного сборника, без записи. Нечитаемый
// файл — пустой состав.
func (s *ContentsScanner) lines(ctx context.Context, c pendingCompilation) ([]ContentLine, error) {
	var roots []tocNode
	rc, err := openFB2(filepath.Join(s.booksRoot, c.archive), c.fileName)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			s.logger.Info("compilation contents: fb2 open failed", "work_id", c.workID, "err", err)
		}
	} else {
		roots = scanFb2Contents(rc)
		_ = rc.Close()
	}
	authors, byKey, err := s.candidates(ctx, c.workID)
	if err != nil {
		return nil, err
	}
	entries := contentsEntries(roots, c.title, authors)
	out := make([]ContentLine, 0, len(entries))
	for _, e := range entries {
		l := ContentLine{Title: e.title}
		if id := matchContentEntry(e, byKey, authors); id != c.workID {
			l.WorkID = id
		}
		out = append(out, l)
	}
	return out, nil
}

// Preview — состав сборника, каким его записал бы сканер (сухой прогон, без
// записи и без отметки).
func (s *ContentsScanner) Preview(ctx context.Context, workID int64) ([]ContentLine, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT w.id, COALESCE(w.title, ''), a.filename, b.file_name || '.' || b.ext
		FROM works w
		CROSS JOIN LATERAL (
		    SELECT b.archive_id, b.file_name, b.ext FROM books b
		    WHERE b.work_id = w.id AND NOT b.deleted AND lower(b.ext) = 'fb2'
		    ORDER BY (b.normalized_title = w.normalized_title) DESC, b.id
		    LIMIT 1) b
		JOIN archives a ON a.id = b.archive_id
		WHERE w.id = $1`, workID)
	if err != nil {
		return nil, err
	}
	var c pendingCompilation
	found := false
	for rows.Next() {
		if err := rows.Scan(&c.workID, &c.title, &c.archive, &c.fileName); err != nil {
			rows.Close()
			return nil, err
		}
		found = true
	}
	rows.Close()
	if err := rows.Err(); err != nil || !found {
		return nil, err
	}
	return s.lines(ctx, c)
}

// scanOne — оглавление одного сборника: разбор, связка, запись (отметка
// ставится и при пустом составе — повторов нет).
func (s *ContentsScanner) scanOne(ctx context.Context, c pendingCompilation) (int, error) {
	lines, err := s.lines(ctx, c)
	if err != nil {
		return 0, err
	}
	linked := 0
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `DELETE FROM work_contents WHERE compilation_work_id = $1`, c.workID); err != nil {
		return 0, err
	}
	if len(lines) > 0 {
		rows := make([][]any, 0, len(lines))
		for i, l := range lines {
			var wid any
			if l.WorkID != 0 {
				wid = l.WorkID
				linked++
			}
			rows = append(rows, []any{c.workID, i + 1, l.Title, wid})
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"work_contents"},
			[]string{"compilation_work_id", "position", "title", "work_id"}, pgx.CopyFromRows(rows)); err != nil {
			return 0, fmt.Errorf("contents write: %w", err)
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE works SET contents_scanned_at = now() WHERE id = $1`, c.workID); err != nil {
		return 0, err
	}
	return linked, tx.Commit(ctx)
}

// candidates — авторы сборника и их отдельные работы (не сборники) по ключу
// названия: название работы и названия её изданий.
func (s *ContentsScanner) candidates(ctx context.Context, compilationID int64) ([]contentAuthor, map[string][]contentCandidate, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT a.id, a.last_name, COALESCE(a.first_name, '')
		FROM books b JOIN book_authors ba ON ba.book_id = b.id JOIN authors a ON a.id = ba.author_id
		WHERE b.work_id = $1 AND NOT b.deleted AND NOT a.is_service`, compilationID)
	if err != nil {
		return nil, nil, fmt.Errorf("contents authors: %w", err)
	}
	var authors []contentAuthor
	var ids []int64
	for rows.Next() {
		var id int64
		var last, first string
		if err := rows.Scan(&id, &last, &first); err != nil {
			rows.Close()
			return nil, nil, err
		}
		authors = append(authors, newContentAuthor(id, last, first))
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	byKey := map[string][]contentCandidate{}
	if len(ids) == 0 {
		return authors, byKey, nil
	}
	rows, err = s.pool.Query(ctx, `
		SELECT w.id, array_agg(DISTINCT ba.author_id), array_agg(DISTINCT b.title), w.title
		FROM books b
		JOIN works w ON w.id = b.work_id AND COALESCE(w.kind, '') = '' AND w.id <> $2
		JOIN book_authors ba ON ba.book_id = b.id
		WHERE NOT b.deleted AND b.work_id IN (
		    SELECT b2.work_id FROM book_authors ba2 JOIN books b2 ON b2.id = ba2.book_id AND NOT b2.deleted
		    WHERE ba2.author_id = ANY($1::bigint[]))
		GROUP BY w.id, w.title`, ids, compilationID)
	if err != nil {
		return nil, nil, fmt.Errorf("contents candidates: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var wid int64
		var wAuthors []int64
		var titles []string
		var wTitle *string
		if err := rows.Scan(&wid, &wAuthors, &titles, &wTitle); err != nil {
			return nil, nil, err
		}
		if wTitle != nil {
			titles = append(titles, *wTitle)
		}
		seen := map[string]bool{}
		for _, t := range titles {
			k := contentKey(t)
			if k == "" || seen[k] {
				continue
			}
			seen[k] = true
			byKey[k] = append(byKey[k], contentCandidate{workID: wid, authors: wAuthors})
		}
	}
	return authors, byKey, rows.Err()
}

// RelinkContents связывает строки оглавлений без работы с работами, появившимися
// в каталоге (после импорта; ссылки на удалённые работы FK обнулил). Возвращает
// число новых связей.
func (s *ContentsScanner) RelinkContents(ctx context.Context) (int, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT c.compilation_work_id FROM work_contents c
		JOIN works w ON w.id = c.compilation_work_id AND w.kind IN `+compilationKindsSQL+`
		WHERE c.work_id IS NULL`)
	if err != nil {
		return 0, fmt.Errorf("relink compilations: %w", err)
	}
	var comps []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		comps = append(comps, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	linked := 0
	for _, cid := range comps {
		if ctx.Err() != nil {
			return linked, ctx.Err()
		}
		n, err := s.relinkOne(ctx, cid)
		if err != nil {
			return linked, err
		}
		linked += n
	}
	return linked, nil
}

func (s *ContentsScanner) relinkOne(ctx context.Context, compilationID int64) (int, error) {
	rows, err := s.pool.Query(ctx, `SELECT position, title FROM work_contents
		WHERE compilation_work_id = $1 AND work_id IS NULL`, compilationID)
	if err != nil {
		return 0, err
	}
	type row struct {
		pos   int
		title string
	}
	var open []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.pos, &r.title); err != nil {
			rows.Close()
			return 0, err
		}
		open = append(open, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	authors, byKey, err := s.candidates(ctx, compilationID)
	if err != nil {
		return 0, err
	}
	linked := 0
	for _, r := range open {
		// Заголовок-группа автора при повторной связке неизвестен — ищем среди
		// всех авторов сборника (то же правило единственного кандидата).
		id := matchContentEntry(contentEntry{title: r.title}, byKey, authors)
		if id == 0 || id == compilationID {
			continue
		}
		if _, err := s.pool.Exec(ctx, `UPDATE work_contents SET work_id = $3
			WHERE compilation_work_id = $1 AND position = $2 AND work_id IS NULL`, compilationID, r.pos, id); err != nil {
			return linked, err
		}
		linked++
	}
	return linked, nil
}
