package history

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// Закладки и выделения с заметками в веб-ридере (#389, B6).

// Annotation — закладка или выделение в издании.
type Annotation struct {
	ID        int64     `json:"id"`
	BookID    int64     `json:"book_id"`
	Kind      string    `json:"kind"` // bookmark | highlight
	CFI       string    `json:"cfi"`
	Excerpt   string    `json:"excerpt,omitempty"`
	Note      string    `json:"note,omitempty"`
	Label     string    `json:"label,omitempty"`
	Fraction  *float64  `json:"fraction,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// Edition — название и язык издания (в «Моих заметках» работы).
	EditionTitle string `json:"edition_title,omitempty"`
	EditionLang  string `json:"edition_lang,omitempty"`
}

// ErrAnnotationNotFound — нет такой заметки у пользователя.
var ErrAnnotationNotFound = errors.New("annotation not found")

// ErrInvalidAnnotation — неверный вид или пустая позиция.
var ErrInvalidAnnotation = errors.New("invalid annotation")

const (
	maxExcerpt = 2000
	maxNote    = 10000
	maxLabel   = 300
	maxCFI     = 2000
)

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

const annotationCols = `a.id, a.book_id, a.kind, a.cfi, a.excerpt, a.note, a.label, a.fraction::float8, a.created_at, a.updated_at`

func scanAnnotation(row pgx.Row, extra ...any) (Annotation, error) {
	var a Annotation
	dest := append([]any{&a.ID, &a.BookID, &a.Kind, &a.CFI, &a.Excerpt, &a.Note, &a.Label, &a.Fraction, &a.CreatedAt, &a.UpdatedAt}, extra...)
	err := row.Scan(dest...)
	return a, err
}

// SaveAnnotation создаёт закладку/выделение (то же место — обновляет текст и заметку).
func (s *Service) SaveAnnotation(ctx context.Context, userID, bookID int64, a Annotation) (Annotation, error) {
	if (a.Kind != "bookmark" && a.Kind != "highlight") || strings.TrimSpace(a.CFI) == "" || len(a.CFI) > maxCFI {
		return Annotation{}, ErrInvalidAnnotation
	}
	if a.Fraction != nil && (*a.Fraction < 0 || *a.Fraction > 1) {
		a.Fraction = nil
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO annotations AS a (user_id, book_id, kind, cfi, excerpt, note, label, fraction)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (user_id, book_id, kind, cfi) DO UPDATE SET
			excerpt = EXCLUDED.excerpt, note = EXCLUDED.note, label = EXCLUDED.label,
			fraction = EXCLUDED.fraction, updated_at = now()
		RETURNING `+annotationCols,
		userID, bookID, a.Kind, strings.TrimSpace(a.CFI), clip(a.Excerpt, maxExcerpt), clip(a.Note, maxNote),
		clip(a.Label, maxLabel), a.Fraction)
	out, err := scanAnnotation(row)
	if err != nil {
		return Annotation{}, fmt.Errorf("save annotation: %w", err)
	}
	return out, nil
}

// UpdateAnnotationNote меняет заметку.
func (s *Service) UpdateAnnotationNote(ctx context.Context, userID, id int64, note string) (Annotation, error) {
	row := s.pool.QueryRow(ctx, `
		UPDATE annotations a SET note = $3, updated_at = now()
		WHERE a.id = $1 AND a.user_id = $2
		RETURNING `+annotationCols, id, userID, clip(note, maxNote))
	out, err := scanAnnotation(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return Annotation{}, ErrAnnotationNotFound
	}
	if err != nil {
		return Annotation{}, fmt.Errorf("update annotation: %w", err)
	}
	return out, nil
}

// DeleteAnnotation удаляет закладку или выделение.
func (s *Service) DeleteAnnotation(ctx context.Context, userID, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM annotations WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete annotation: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrAnnotationNotFound
	}
	return nil
}

// BookAnnotations — заметки пользователя в издании по ходу книги.
func (s *Service) BookAnnotations(ctx context.Context, userID, bookID int64) ([]Annotation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+annotationCols+` FROM annotations a
		WHERE a.user_id = $1 AND a.book_id = $2
		ORDER BY a.fraction NULLS LAST, a.created_at`, userID, bookID)
	if err != nil {
		return nil, fmt.Errorf("book annotations: %w", err)
	}
	defer rows.Close()
	out := []Annotation{}
	for rows.Next() {
		a, err := scanAnnotation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// WorkAnnotations — заметки пользователя во всех изданиях работы («Мои заметки»
// на карточке): по изданию, затем по ходу книги.
func (s *Service) WorkAnnotations(ctx context.Context, userID, workID int64) ([]Annotation, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+annotationCols+`, b.title, COALESCE(b.lang, '')
		FROM annotations a JOIN books b ON b.id = a.book_id
		WHERE a.user_id = $1 AND b.work_id = $2 AND NOT b.deleted
		ORDER BY a.book_id, a.fraction NULLS LAST, a.created_at`, userID, workID)
	if err != nil {
		return nil, fmt.Errorf("work annotations: %w", err)
	}
	defer rows.Close()
	out := []Annotation{}
	for rows.Next() {
		var title, lang string
		a, err := scanAnnotation(rows, &title, &lang)
		if err != nil {
			return nil, err
		}
		a.EditionTitle, a.EditionLang = title, lang
		out = append(out, a)
	}
	return out, rows.Err()
}
