package awards

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
)

// Сопоставление лауреатов с каталогом. Фантлаб даёт русское название
// произведения, название в оригинале и автора «Имя Фамилия»; каталог — в
// основном русские издания. Произведение: нормализованное название работы или
// её живого издания (регистр, ё/е, пунктуация) либо оригинальное название
// издания (src-title fb2) или, для ручного списка, где названия выверены, — издание
// с основным названием без подзаголовка премии («Кто бы мог подумать! Как мозг…»;
// у Фантлаба «Землянин. На службе Великого дома» — вторая книга цикла, а не
// «Землянин» с подзаголовком) + основа фамилии среди
// авторов работы. Из нескольких работ — найденная по полному русскому названию, с
// русскими изданиями, с наибольшим числом изданий. Автор (премии автору): фамилия
// целиком, имя — первым словом, отчеством или инициалом; отчество или его инициал
// («Василий Иванович Аксёнов», «Василий И. Аксёнов» — не Василий Павлович) — обязательны; тёзки — самый
// известный. Кинопремии — по QID фильма среди экранизаций (book_adaptations).
// Точность важнее полноты: без совпадения фамилии связи нет.

// normSQL — нормализация строки в SQL (зеркало normTitle).
func normSQL(expr string) string {
	return fmt.Sprintf(`btrim(regexp_replace(replace(lower(%s), 'ё', 'е'), '[^[:alnum:]]+', ' ', 'g'))`, expr)
}

// softSQL — нормализация имени в SQL (зеркало softName).
func softSQL(expr string) string {
	return fmt.Sprintf(`regexp_replace(translate(%s, 'эйхьъ', 'еиг'), '(.)\1+', '\1', 'g')`, normSQL(expr))
}

// mainTitle — основное название: до первой точки, двоеточия, ! ? ; — без подзаголовка.
func mainTitle(s string) string {
	if i := strings.IndexAny(s, ".:!?;"); i >= 0 {
		s = s[:i]
	}
	return normTitle(s)
}

// normTitle — нормализация для сравнения: нижний регистр, ё → е, всё, кроме
// букв и цифр, — одним пробелом.
func normTitle(s string) string {
	s = strings.ReplaceAll(strings.ToLower(s), "ё", "е")
	var b strings.Builder
	space := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		} else {
			space = true
		}
	}
	return b.String()
}

// softName — нормализация имени поверх normTitle: варианты транскрипции
// (Сэмюэл/Сэмюэль, Херберт/Герберт, Мьевиль/Мьевилль) сводятся к одному
// написанию: э → е, й → и, х → г, без ь и ъ, удвоенные буквы — одной.
func softName(s string) string {
	var b strings.Builder
	var prev rune = -1
	for _, r := range normTitle(s) {
		switch r {
		case 'э':
			r = 'е'
		case 'й':
			r = 'и'
		case 'х':
			r = 'г'
		case 'ь', 'ъ':
			continue
		}
		if r == prev {
			continue
		}
		prev = r
		b.WriteRune(r)
	}
	return b.String()
}

// surnameStem — основа фамилии из «Имя Фамилия» (последнее слово): первые
// max(4, n−2) букв — «Стругацкие» и «Стругацкий» совпадут.
func surnameStem(author string) string {
	toks := strings.Fields(softName(author))
	if len(toks) == 0 {
		return ""
	}
	r := []rune(toks[len(toks)-1])
	if k := max(4, len(r)-2); k < len(r) {
		r = r[:k]
	}
	return string(r)
}

// workWant — строка aw_want: id лауреата, название, оригинальное название,
// основное название, основа фамилии.
type workWant struct {
	id         int64
	t, o, m, s string
}

// minMainTitle — основное название короче этого (в буквах) не сравниваем: «Ген» из
// «Ген. Очень личная история» найдёт и чужой «Ген».
const minMainTitle = 4

// newWorkWant — строка сопоставления; основное название — только если byMain.
func newWorkWant(id int64, title, orig, author string, byMain bool) (workWant, bool) {
	w := workWant{id: id, t: normTitle(title), o: normTitle(orig), s: surnameStem(author)}
	if byMain {
		w.m = mainTitle(title)
	}
	if len([]rune(strings.ReplaceAll(w.m, " ", ""))) < minMainTitle {
		w.m = ""
	}
	return w, w.t != "" && w.s != ""
}

// matchWorksQuery — пары (win_id, work_id) для лауреатов из aw_want(id, t, o, m, s).
var matchWorksQuery = `
	WITH bk AS MATERIALIZED (
		SELECT work_id, ` + normSQL("title") + ` AS t, ` + normSQL("src_title") + ` AS o
		FROM books WHERE NOT deleted AND work_id IS NOT NULL
	),
	titled AS (
		SELECT w.id AS win_id, wk.id AS work_id, w.s, 0 AS p FROM aw_want w JOIN works wk ON ` + normSQL("wk.title") + ` = w.t
		UNION SELECT w.id, b.work_id, w.s, 0 FROM aw_want w JOIN bk b ON b.t = w.t
		UNION SELECT w.id, b.work_id, w.s, 1 FROM aw_want w JOIN bk b ON b.o = w.o WHERE w.o <> ''
		-- Только в эту сторону: «Дюна» премии не должна найти «Дюну: Дом Атрейдесов».
		UNION SELECT w.id, b.work_id, w.s, 1 FROM aw_want w JOIN bk b ON b.t = w.m WHERE w.m <> '' AND w.m <> w.t
	)
	SELECT DISTINCT ON (t.win_id) t.win_id, t.work_id
	FROM titled t JOIN works wk ON wk.id = t.work_id
	WHERE EXISTS (
		SELECT 1 FROM books b
		JOIN book_authors ba ON ba.book_id = b.id
		JOIN authors a ON a.id = ba.author_id
		CROSS JOIN LATERAL regexp_split_to_table(` + softSQL("a.last_name") + `, ' ') AS tok
		WHERE b.work_id = t.work_id AND NOT b.deleted
		  AND left(tok, greatest(4, length(t.s))) = t.s)
	ORDER BY t.win_id, t.p,
	         EXISTS (SELECT 1 FROM books b WHERE b.work_id = t.work_id AND NOT b.deleted AND b.lang = 'ru') DESC,
	         wk.edition_count DESC, t.work_id`

// authorWant — строка aw_want_a: имя (первое слово), инициал отчества (одна
// буква вторым словом), фамилия (остальное или последнее слово), всё имя целиком
// (для однословных «Сюлли-Прюдом»).
type authorWant struct {
	id                            int64
	first, mid, rest, last, whole string
}

func newAuthorWant(id int64, name string) (authorWant, bool) {
	n := softName(name)
	toks := strings.Fields(n)
	if len(toks) == 0 {
		return authorWant{}, false
	}
	w := authorWant{id: id, whole: n}
	if len(toks) > 1 {
		w.first, w.rest, w.last = toks[0], strings.Join(toks[1:], " "), toks[len(toks)-1]
	}
	if len(toks) > 2 && (initialAt(name, 1) || isPatronymic(toks[1])) {
		w.mid, w.rest = string([]rune(toks[1])[:1]), strings.Join(toks[2:], " ")
	}
	return w, true
}

// initialAt — i-е слово имени — инициал с точкой («И.»; «О'Нил» — не инициал).
func initialAt(name string, i int) bool {
	f := strings.Fields(name)
	if i >= len(f) {
		return false
	}
	r := []rune(f[i])
	return len(r) == 2 && r[1] == '.' && unicode.IsLetter(r[0])
}

// isPatronymic — отчество по окончанию (слово после softName): -вич, -вна, -ична.
func isPatronymic(t string) bool {
	if len([]rune(t)) <= 4 {
		return false
	}
	for _, suf := range []string{"вич", "вна", "ична"} {
		if strings.HasSuffix(t, suf) {
			return true
		}
	}
	return false
}

// matchAuthorsQuery — пары (win_id, author_id) для aw_want_a(id, first, mid, rest, last, whole).
var matchAuthorsQuery = `
	WITH au AS MATERIALIZED (
		SELECT id, renown, ` + softSQL("last_name") + ` AS l,
		       split_part(` + softSQL("first_name") + `, ' ', 1) AS f,
		       split_part(` + softSQL("middle_name") + `, ' ', 1) AS m,
		       first_name = '' AS nofirst
		FROM authors WHERE NOT is_service
	),
	c AS (
		SELECT w.id AS win_id, a.id AS author_id, a.renown
		FROM aw_want_a w JOIN au a ON a.l IN (w.rest, w.last)
		WHERE w.first <> '' AND (a.f = w.first OR a.m = w.first
		      OR (length(w.first) <= 2 AND left(a.f, length(w.first)) = w.first))
		  AND (w.mid = '' OR left(a.m, 1) = w.mid)
		UNION ALL
		SELECT w.id, a.id, a.renown FROM aw_want_a w JOIN au a ON a.l = w.whole AND a.nofirst
	)
	SELECT DISTINCT ON (win_id) win_id, author_id FROM c ORDER BY win_id, renown DESC, author_id`

// matchFilmsQuery — кинопремии ($1 — их ключи): фильм лауреата (QID в source_link)
// среди экранизаций книг каталога → работа книги (из нескольких — с большим числом изданий).
const matchFilmsQuery = `
	SELECT DISTINCT ON (aw.id) aw.id AS win_id, b.work_id
	FROM award_wins aw
	JOIN book_adaptations ba ON ba.provider = 'wikidata' AND ba.ext_id = aw.source_link
	JOIN books b ON b.id = ba.book_id AND NOT b.deleted AND b.work_id IS NOT NULL
	JOIN works wk ON wk.id = b.work_id
	WHERE aw.award = ANY($1) AND aw.source = 'wikidata'
	ORDER BY aw.id, wk.edition_count DESC, b.work_id`

// FilmAwardKeys — ключи кинопремий (награды экранизаций, а не автора и книги):
// фильтр «С премиями» у авторов их не учитывает.
func FilmAwardKeys() []string { return filmAwardKeys() }

// filmAwardKeys — ключи кинопремий белого списка.
func filmAwardKeys() []string {
	var out []string
	for _, a := range Catalog {
		if a.Film {
			out = append(out, a.Key)
		}
	}
	return out
}

// Match заново сопоставляет всех лауреатов с каталогом (каталог меняется с
// импортом): находит пары и переписывает только изменившиеся связи.
func (s *Syncer) Match(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, q := range []string{
		`CREATE TEMP TABLE aw_want (id BIGINT PRIMARY KEY, t TEXT NOT NULL, o TEXT NOT NULL, m TEXT NOT NULL,
			s TEXT NOT NULL) ON COMMIT DROP`,
		`CREATE TEMP TABLE aw_want_a (id BIGINT PRIMARY KEY, first TEXT NOT NULL, mid TEXT NOT NULL, rest TEXT NOT NULL,
			last TEXT NOT NULL, whole TEXT NOT NULL) ON COMMIT DROP`,
		`CREATE TEMP TABLE aw_match (win_id BIGINT PRIMARY KEY, work_id BIGINT, author_id BIGINT) ON COMMIT DROP`,
	} {
		if _, err := tx.Exec(ctx, q); err != nil {
			return err
		}
	}
	works, authors, err := loadWants(ctx, tx)
	if err != nil {
		return err
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"aw_want"}, []string{"id", "t", "o", "m", "s"},
		pgx.CopyFromSlice(len(works), func(i int) ([]any, error) {
			w := works[i]
			return []any{w.id, w.t, w.o, w.m, w.s}, nil
		})); err != nil {
		return fmt.Errorf("award wants: %w", err)
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"aw_want_a"}, []string{"id", "first", "mid", "rest", "last", "whole"},
		pgx.CopyFromSlice(len(authors), func(i int) ([]any, error) {
			w := authors[i]
			return []any{w.id, w.first, w.mid, w.rest, w.last, w.whole}, nil
		})); err != nil {
		return fmt.Errorf("award author wants: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO aw_match (win_id, work_id) SELECT win_id, work_id FROM (`+matchWorksQuery+`) m`); err != nil {
		return fmt.Errorf("match award works: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO aw_match (win_id, author_id) SELECT win_id, author_id FROM (`+matchAuthorsQuery+`) m`); err != nil {
		return fmt.Errorf("match award authors: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO aw_match (win_id, work_id) SELECT win_id, work_id FROM (`+matchFilmsQuery+`) m
		ON CONFLICT (win_id) DO NOTHING`, filmAwardKeys()); err != nil {
		return fmt.Errorf("match award films: %w", err)
	}
	// RETURNING: x — строка до обновления, aw — после; обе работы переиндексировать.
	rows, err := tx.Query(ctx, `
		UPDATE award_wins aw SET work_id = m.work_id, author_id = m.author_id
		FROM award_wins x LEFT JOIN aw_match m ON m.win_id = x.id
		WHERE x.id = aw.id AND (aw.work_id, aw.author_id) IS DISTINCT FROM (m.work_id, m.author_id)
		RETURNING x.work_id, aw.work_id`)
	if err != nil {
		return fmt.Errorf("store award matches: %w", err)
	}
	var touched []*int64
	for rows.Next() {
		var before, after *int64
		if err := rows.Scan(&before, &after); err != nil {
			rows.Close()
			return err
		}
		touched = append(touched, before, after)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store award matches: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	for _, id := range touched {
		s.noteWork(id)
	}
	s.flushWorks(ctx)
	return nil
}

func loadWants(ctx context.Context, tx pgx.Tx) ([]workWant, []authorWant, error) {
	rows, err := tx.Query(ctx, `SELECT id, kind, title, orig_title, author, source FROM award_wins`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var works []workWant
	var authors []authorWant
	for rows.Next() {
		var id int64
		var kind, title, orig, author, source string
		if err := rows.Scan(&id, &kind, &title, &orig, &author, &source); err != nil {
			return nil, nil, err
		}
		if kind == "author" {
			if w, ok := newAuthorWant(id, author); ok {
				authors = append(authors, w)
			}
		} else if w, ok := newWorkWant(id, title, orig, author, source == sourceManual); ok {
			works = append(works, w)
		}
	}
	return works, authors, rows.Err()
}
