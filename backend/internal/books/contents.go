package books

import (
	"context"
	"fmt"
	"strings"
)

// Состав сборников и «входит в сборники» (#388, таблица work_contents — её
// заполняет metadata.ContentsScanner из оглавления fb2).

// compilationKindsSQL — типы работ-сборников (works.kind).
const compilationKindsSQL = `('collection', 'anthology', 'omnibus')`

// ContentEntry — строка «Состава» сборника; WorkID — отдельная работа каталога,
// если строка с ней совпала и она видна пользователю.
type ContentEntry struct {
	Title  string `json:"title"`
	WorkID *int64 `json:"work_id,omitempty"`
}

// CompilationRef — сборник, в который входит произведение.
type CompilationRef struct {
	WorkID int64  `json:"work_id"`
	Title  string `json:"title"`
	Kind   string `json:"kind"`
	Author string `json:"author,omitempty"` // основной автор сборника
}

// visibleWorkClause — у работы <alias> есть живое издание не скрытого языка и
// без скрытых жанров (зеркало видимости карточки: работа видна, если видно хоть
// одно её издание).
func visibleWorkClause(alias string, n int, excludeGenres, excludeLangs []string) (string, []any) {
	var b strings.Builder
	var args []any
	fmt.Fprintf(&b, "EXISTS (SELECT 1 FROM books vb WHERE vb.work_id = %s.id AND NOT vb.deleted", alias)
	if len(excludeLangs) > 0 {
		fmt.Fprintf(&b, " AND (vb.lang IS NULL OR NOT (lower(btrim(vb.lang)) = ANY($%d::text[])))", n)
		args = append(args, lowerAll(excludeLangs))
		n++
	}
	if len(excludeGenres) > 0 {
		fmt.Fprintf(&b, " AND NOT EXISTS (SELECT 1 FROM book_genres vbg JOIN genres vg ON vg.id = vbg.genre_id"+
			" WHERE vbg.book_id = vb.id AND vg.fb2_code = ANY($%d::text[]))", n)
		args = append(args, excludeGenres)
	}
	b.WriteString(")")
	return b.String(), args
}

func lowerAll(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = strings.ToLower(strings.TrimSpace(x))
	}
	return out
}

// WorkContents — «Состав» сборника по порядку; пусто, если работа не сборник или
// оглавление не разобрано. Ссылка на работу — только на видимую.
func (s *Service) WorkContents(ctx context.Context, workID int64, excludeGenres, excludeLangs []string) ([]ContentEntry, error) {
	vis, args := visibleWorkClause("lw", 2, excludeGenres, excludeLangs)
	rows, err := s.pool.Query(ctx, `
		SELECT c.title, CASE WHEN `+vis+` THEN lw.id END
		FROM work_contents c
		JOIN works cw ON cw.id = c.compilation_work_id AND cw.kind IN `+compilationKindsSQL+`
		LEFT JOIN works lw ON lw.id = c.work_id
		WHERE c.compilation_work_id = $1
		ORDER BY c.position`, append([]any{workID}, args...)...)
	if err != nil {
		return nil, fmt.Errorf("work contents: %w", err)
	}
	defer rows.Close()
	var out []ContentEntry
	for rows.Next() {
		var e ContentEntry
		if err := rows.Scan(&e.Title, &e.WorkID); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// maxCompilationRefs — сколько сборников показываем у произведения.
const maxCompilationRefs = 20

// WorkCompilations — видимые сборники, в которые входит работа (по названию).
func (s *Service) WorkCompilations(ctx context.Context, workID int64, excludeGenres, excludeLangs []string) ([]CompilationRef, error) {
	vis, args := visibleWorkClause("w", 2, excludeGenres, excludeLangs)
	rows, err := s.pool.Query(ctx, `
		SELECT w.id, COALESCE(w.title, ''), w.kind,
		       COALESCE((SELECT TRIM(CONCAT_WS(' ', a.last_name, a.first_name)) FROM authors a
		                 WHERE a.id = w.primary_author_id AND NOT a.is_service), '')
		FROM works w
		WHERE w.kind IN `+compilationKindsSQL+`
		  AND w.id IN (SELECT c.compilation_work_id FROM work_contents c WHERE c.work_id = $1)
		  AND `+vis+`
		ORDER BY w.title, w.id
		LIMIT `+fmt.Sprint(maxCompilationRefs), append([]any{workID}, args...)...)
	if err != nil {
		return nil, fmt.Errorf("work compilations: %w", err)
	}
	defer rows.Close()
	var out []CompilationRef
	for rows.Next() {
		var c CompilationRef
		if err := rows.Scan(&c.WorkID, &c.Title, &c.Kind, &c.Author); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
