package catalog

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Возможные дубли авторов (#308): одна и та же запись librusec под двумя
// именами. Кандидаты — «Фамилия Имя» ↔ «Фамилия Имя Отчество» (у обоих живые
// книги; на проде 4,5 тыс. пар) и одинаковое латинское имя из fb2 переводов
// («Конан Дойл Артур» ↔ «Дойль Артур Конан», authors.latin_name). Разные люди
// среди них тоже есть (Матесон Ричард и Ричард Кристиан) — решает
// администратор: слияние только вручную (metadata.MergeAuthors). Тёзки с
// уточнением (name_note) разделены намеренно и в кандидаты не попадают.

// DuplicateAuthor — один из возможных дублей.
type DuplicateAuthor struct {
	ID          int64       `json:"id"`
	FullName    string      `json:"full_name"`
	BookCount   int         `json:"book_count"`
	Renown      int64       `json:"renown"`
	YearsActive *YearsRange `json:"years_active,omitempty"`
	// Reason — почему кандидат: "middle_name" (ФИ ↔ ФИО) | "latin_name".
	Reason string `json:"reason"`
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

// dupAuthorCols — поля DuplicateAuthor по автору под алиасом a.
const dupAuthorCols = `a.id, a.last_name, COALESCE(a.first_name, ''), COALESCE(a.middle_name, ''), a.book_count, a.renown,
	(SELECT min(b.written_year) FROM book_authors ba JOIN books b ON b.id = ba.book_id
	  WHERE ba.author_id = a.id AND b.deleted = false AND b.written_year IS NOT NULL),
	(SELECT max(b.written_year) FROM book_authors ba JOIN books b ON b.id = ba.book_id
	  WHERE ba.author_id = a.id AND b.deleted = false AND b.written_year IS NOT NULL)`

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
// Сначала дешёвый отбор по ключу ФИ и латинскому имени, живые книги — только у
// отобранных.
func (s *Service) AuthorDuplicates(ctx context.Context, authorID int64) ([]DuplicateAuthor, error) {
	rows, err := s.pool.Query(ctx, `
		WITH me AS (
		    SELECT a.id, lower(replace(a.last_name, 'ё', 'е')) AS l,
		           lower(replace(COALESCE(a.first_name, ''), 'ё', 'е')) AS f,
		           COALESCE(a.middle_name, '') = '' AS no_middle, a.latin_name
		    FROM authors a WHERE a.id = $1 AND COALESCE(a.name_note, '') = ''
		),
		cand AS (
		    SELECT a.id, CASE
		               WHEN lower(replace(a.last_name, 'ё', 'е')) = me.l
		                AND lower(replace(COALESCE(a.first_name, ''), 'ё', 'е')) = me.f
		                AND me.f <> '' AND (COALESCE(a.middle_name, '') = '') <> me.no_middle THEN 'middle_name'
		               ELSE 'latin_name' END AS reason
		    FROM authors a, me
		    WHERE a.id <> me.id AND NOT a.is_service AND COALESCE(a.name_note, '') = ''
		      AND ((lower(replace(a.last_name, 'ё', 'е')) = me.l
		            AND lower(replace(COALESCE(a.first_name, ''), 'ё', 'е')) = me.f
		            AND me.f <> '' AND (COALESCE(a.middle_name, '') = '') <> me.no_middle)
		           OR (me.latin_name IS NOT NULL AND a.latin_name = me.latin_name))
		      AND EXISTS (SELECT 1 FROM book_authors ba JOIN books b ON b.id = ba.book_id AND b.deleted = false
		                  WHERE ba.author_id = a.id)
		)
		SELECT `+dupAuthorCols+`, cand.reason
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
		if err := scanDupAuthor(rows, &d, &d.Reason); err != nil {
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
	const pairsSQL = `
		WITH base AS (` + dupBase + `),
		     pairs AS (
		         SELECT x.id AS a, y.id AS b, 'middle_name' AS reason FROM base x JOIN base y
		           ON y.l = x.l AND y.f = x.f AND x.f <> '' AND x.no_middle AND NOT y.no_middle
		         UNION
		         SELECT x.id, y.id, 'latin_name' FROM base x JOIN base y
		           ON x.latin_name IS NOT NULL AND y.latin_name = x.latin_name AND x.id < y.id
		              AND NOT (y.l = x.l AND y.f = x.f)
		     )`
	rows, err := s.pool.Query(ctx, pairsSQL+`
		SELECT p.a, p.b, p.reason, count(*) OVER () FROM pairs p
		JOIN authors x ON x.id = p.a JOIN authors y ON y.id = p.b
		ORDER BY greatest(x.renown, y.renown) DESC, x.book_count + y.book_count DESC, p.a, p.b
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("duplicate pairs: %w", err)
	}
	type raw struct {
		a, b   int64
		reason string
	}
	total := 0
	raws, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (raw, error) {
		var p raw
		return p, r.Scan(&p.a, &p.b, &p.reason, &total)
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
		}
		out = append(out, pair)
	}
	return out, total, nil
}
