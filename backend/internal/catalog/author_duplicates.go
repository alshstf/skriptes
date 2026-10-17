package catalog

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Возможные дубли авторов (#308): одна и та же запись librusec под двумя
// именами. Кандидаты — «Фамилия Имя» ↔ «Фамилия Имя Отчество» (у обоих живые
// книги) и одинаковое латинское имя из fb2 переводов («Конан Дойл Артур» ↔
// «Дойль Артур Конан», authors.latin_name).
//
// «ФИ ↔ ФИО» — только однозначные: у записи без отчества один кандидат с
// отчеством. «Иванов Николай» при десятке «Ивановых Николаев …» — кандидат,
// только если на него указывает доказательство (midEvidence: издания одной
// работы или книги одного авторского цикла на обеих записях). Разбор прода
// 2026-10-16 (#527): из 4,5 тыс. пар 1,6 тыс. были такими неразрешимыми
// группами, ещё 1,3 тыс. — соавторы с чужим латинским именем (чинится в
// RecomputeAuthorStats: латинское имя — только по сольным книгам). Разные люди
// среди них тоже есть (Матесон Ричард и Ричард Кристиан) — решает
// администратор: слияние только вручную (metadata.MergeAuthors), «не дубликат»
// запоминается (author_duplicate_rejections, RejectDuplicate) — такая пара
// больше не предлагается. Тёзки с уточнением (name_note) разделены намеренно и
// в кандидаты не попадают.

// DuplicateAuthor — один из возможных дублей.
type DuplicateAuthor struct {
	ID          int64       `json:"id"`
	FullName    string      `json:"full_name"`
	BookCount   int         `json:"book_count"`
	Renown      int64       `json:"renown"`
	YearsActive *YearsRange `json:"years_active,omitempty"`
	// Reason — почему кандидат: "middle_name" (ФИ ↔ ФИО) | "latin_name".
	Reason string `json:"reason"`
	// Evidence — что ещё указывает на одного человека (для "middle_name"):
	// "work" — издания одной работы, "series" — книги одного авторского цикла.
	Evidence string `json:"evidence,omitempty"`
}

// DuplicatePair — пара возможных дублей (страница админки).
type DuplicatePair struct {
	A DuplicateAuthor `json:"a"`
	B DuplicateAuthor `json:"b"`
}

// dupBase — авторы, которые могут быть дублями: не служебные, без уточнения, с
// живыми книгами; ключ ФИ свёрнут («ё» = «е»).
const dupBase = `
	SELECT a.id, lower(replace(a.last_name, 'ё', 'е')) AS l,
	       lower(replace(COALESCE(a.first_name, ''), 'ё', 'е')) AS f,
	       COALESCE(a.middle_name, '') = '' AS no_middle, a.latin_name
	FROM authors a
	WHERE NOT a.is_service AND COALESCE(a.name_note, '') = ''
	  AND EXISTS (SELECT 1 FROM book_authors ba JOIN books b ON b.id = ba.book_id AND b.deleted = false
	              WHERE ba.author_id = a.id)`

// midEvidence — доказательство «ФИ ↔ ФИО» для пары (sid — запись без отчества,
// fid — с отчеством): 'work' — издания одной работы записаны на обе записи,
// 'series' — книги одного авторского цикла (межавторские и издательские серии
// не в счёт: там под одним названием сотни авторов), ” — ничего.
func midEvidence(sid, fid string) string {
	return `CASE WHEN EXISTS (
	        SELECT 1 FROM book_authors e1
	        JOIN books eb1 ON eb1.id = e1.book_id AND eb1.deleted = false AND eb1.work_id IS NOT NULL
	        JOIN books eb2 ON eb2.work_id = eb1.work_id AND eb2.deleted = false
	        JOIN book_authors e2 ON e2.book_id = eb2.id AND e2.author_id = ` + fid + `
	        WHERE e1.author_id = ` + sid + `) THEN 'work'
	    WHEN EXISTS (
	        SELECT 1 FROM book_authors e1
	        JOIN books eb1 ON eb1.id = e1.book_id AND eb1.deleted = false
	        JOIN series es ON es.id = eb1.series_id AND es.kind IS NULL
	        JOIN books eb2 ON eb2.series_id = eb1.series_id AND eb2.deleted = false
	        JOIN book_authors e2 ON e2.book_id = eb2.id AND e2.author_id = ` + fid + `
	        WHERE e1.author_id = ` + sid + `) THEN 'series'
	    ELSE '' END`
}

// dupPairsSQL — пары (a, b, reason, evidence) среди авторов CTE base: «ФИ ↔ ФИО»
// — однозначные или с доказательством; одинаковое латинское имя. midFilter —
// дополнительное условие на пару mid (a, b) (карточка автора — только его пары).
func dupPairsSQL(midFilter, latinFilter string) string {
	return `
	mid AS (
	    SELECT x.id AS a, y.id AS b, count(*) OVER (PARTITION BY x.id) AS fulls
	    FROM base x JOIN base y
	      ON y.l = x.l AND y.f = x.f AND x.f <> '' AND x.no_middle AND NOT y.no_middle
	),
	pairs AS (
	    SELECT m.a, m.b, 'middle_name' AS reason, m.evidence
	    FROM (SELECT mid.a, mid.b, mid.fulls, ` + midEvidence("mid.a", "mid.b") + ` AS evidence
	          FROM mid WHERE ` + midFilter + `) m
	    WHERE m.fulls = 1 OR m.evidence <> ''
	    UNION ALL
	    SELECT x.id, y.id, 'latin_name', '' FROM base x JOIN base y
	      ON x.latin_name IS NOT NULL AND y.latin_name = x.latin_name AND x.id < y.id
	         AND NOT (y.l = x.l AND y.f = x.f)
	    WHERE ` + latinFilter + `
	)`
}

// notRejected — пара (x, y) не отклонена админом («не дубликат», #527).
func notRejected(x, y string) string {
	return `NOT EXISTS (SELECT 1 FROM author_duplicate_rejections r
	                    WHERE r.author_a = least(` + x + `, ` + y + `) AND r.author_b = greatest(` + x + `, ` + y + `))`
}

// dupAuthorCols — поля DuplicateAuthor по автору под алиасом a.
const dupAuthorCols = `a.id, a.last_name, COALESCE(a.first_name, ''), COALESCE(a.middle_name, ''), a.book_count, a.renown,
	(SELECT min(COALESCE(wy.written_year, b.written_year)) FROM book_authors ba JOIN books b ON b.id = ba.book_id LEFT JOIN works wy ON wy.id = b.work_id
	  WHERE ba.author_id = a.id AND b.deleted = false AND COALESCE(wy.written_year, b.written_year) IS NOT NULL),
	(SELECT max(COALESCE(wy.written_year, b.written_year)) FROM book_authors ba JOIN books b ON b.id = ba.book_id LEFT JOIN works wy ON wy.id = b.work_id
	  WHERE ba.author_id = a.id AND b.deleted = false AND COALESCE(wy.written_year, b.written_year) IS NOT NULL)`

func scanDupAuthor(row pgx.Row, d *DuplicateAuthor, extra ...any) error {
	var last, first, middle string
	var yrFrom, yrTo pgtype.Int2
	dest := append([]any{&d.ID, &last, &first, &middle, &d.BookCount, &d.Renown, &yrFrom, &yrTo}, extra...)
	if err := row.Scan(dest...); err != nil {
		return err
	}
	d.FullName = fullName(last, first, middle)
	if yrFrom.Valid && yrTo.Valid {
		d.YearsActive = &YearsRange{From: int(yrFrom.Int16), To: int(yrTo.Int16)}
	}
	return nil
}

// AuthorDuplicates — возможные дубли автора authorID (подсказка на его карточке).
// Те же правила, что у страницы пар: base — только авторы с тем же ФИ или тем же
// латинским именем (неоднозначность «ФИ ↔ ФИО» считается по ним всем), пары —
// только с участием authorID.
func (s *Service) AuthorDuplicates(ctx context.Context, authorID int64) ([]DuplicateAuthor, error) {
	rows, err := s.pool.Query(ctx, `
		WITH me AS (
		    SELECT a.id, lower(replace(a.last_name, 'ё', 'е')) AS l,
		           lower(replace(COALESCE(a.first_name, ''), 'ё', 'е')) AS f, a.latin_name
		    FROM authors a WHERE a.id = $1 AND COALESCE(a.name_note, '') = ''
		),
		base AS (
		    SELECT d.* FROM (`+dupBase+`) d, me
		    WHERE (d.l = me.l AND d.f = me.f) OR (me.latin_name IS NOT NULL AND d.latin_name = me.latin_name)
		    UNION
		    SELECT a.id, me.l, me.f, COALESCE(a.middle_name, '') = '', a.latin_name
		    FROM authors a JOIN me ON me.id = a.id
		),`+dupPairsSQL("$1 IN (mid.a, mid.b)", "$1 IN (x.id, y.id)")+`,
		cand AS (
		    SELECT CASE WHEN p.a = $1 THEN p.b ELSE p.a END AS id, p.reason, p.evidence
		    FROM pairs p WHERE `+notRejected("p.a", "p.b")+`
		)
		SELECT `+dupAuthorCols+`, cand.reason, cand.evidence
		FROM cand JOIN authors a ON a.id = cand.id
		ORDER BY a.renown DESC, a.book_count DESC, a.id
		LIMIT 10`, authorID)
	if err != nil {
		return nil, fmt.Errorf("author duplicates: %w", err)
	}
	defer rows.Close()
	out := []DuplicateAuthor{}
	for rows.Next() {
		var d DuplicateAuthor
		if err := scanDupAuthor(rows, &d, &d.Reason, &d.Evidence); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DuplicatePairs — пары возможных дублей от самых известных (страница админки) и
// их общее число.
func (s *Service) DuplicatePairs(ctx context.Context, limit, offset int) ([]DuplicatePair, int, error) {
	limit, offset = sanitizePaging(limit, offset)
	rows, err := s.pool.Query(ctx, `
		WITH base AS (`+dupBase+`),`+dupPairsSQL("true", "true")+`
		SELECT p.a, p.b, p.reason, p.evidence, count(*) OVER () FROM pairs p
		JOIN authors x ON x.id = p.a JOIN authors y ON y.id = p.b
		WHERE `+notRejected("p.a", "p.b")+`
		ORDER BY greatest(x.renown, y.renown) DESC, x.book_count + y.book_count DESC, p.a, p.b
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("duplicate pairs: %w", err)
	}
	type raw struct {
		a, b             int64
		reason, evidence string
	}
	total := 0
	raws, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (raw, error) {
		var p raw
		return p, r.Scan(&p.a, &p.b, &p.reason, &p.evidence, &total)
	})
	if err != nil {
		return nil, 0, err
	}
	out := make([]DuplicatePair, 0, len(raws))
	for _, p := range raws {
		var pair DuplicatePair
		for _, side := range []struct {
			id  int64
			dst *DuplicateAuthor
		}{{p.a, &pair.A}, {p.b, &pair.B}} {
			if err := scanDupAuthor(s.pool.QueryRow(ctx, `SELECT `+dupAuthorCols+` FROM authors a WHERE a.id = $1`, side.id), side.dst); err != nil {
				return nil, 0, err
			}
			side.dst.Reason = p.reason
			side.dst.Evidence = p.evidence
		}
		out = append(out, pair)
	}
	return out, total, nil
}

// RejectDuplicate — «не дубликат» (#527): пару a, b больше не предлагать ни на
// карточке автора, ни на странице «Дубли авторов». Повтор — без ошибки.
func (s *Service) RejectDuplicate(ctx context.Context, a, b, by int64) error {
	if a <= 0 || b <= 0 || a == b {
		return fmt.Errorf("reject duplicate: need two different authors")
	}
	if a > b {
		a, b = b, a
	}
	tag, err := s.pool.Exec(ctx, `
		INSERT INTO author_duplicate_rejections (author_a, author_b, rejected_by)
		SELECT $1, $2, NULLIF($3::bigint, 0)
		WHERE EXISTS (SELECT 1 FROM authors WHERE id = $1) AND EXISTS (SELECT 1 FROM authors WHERE id = $2)
		ON CONFLICT DO NOTHING`, a, b, by)
	if err != nil {
		return fmt.Errorf("reject duplicate: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var n int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM authors WHERE id = ANY($1)`, []int64{a, b}).Scan(&n); err != nil {
			return fmt.Errorf("reject duplicate: %w", err)
		}
		if n < 2 {
			return ErrNotFound
		}
	}
	return nil
}
