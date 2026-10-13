package timeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"

	"github.com/skriptes/skriptes/backend/internal/bio"
)

// EventOut — веха наружу: без evidence, с флагом hidden для админа.
type EventOut struct {
	bio.Event
	Hidden bool `json:"hidden,omitempty"`
}

// WorkOut — работа датасета, сопоставленная с каталогом (WorkID — наш works.id
// или 0, если не нашли; precision > recall).
type WorkOut struct {
	Ref     string       `json:"ref"`
	Title   string       `json:"title"`
	QID     string       `json:"qid,omitempty"`
	Written *bio.Written `json:"written,omitempty"`
	WorkID  int64        `json:"work_id,omitempty"`
}

// Timeline — ответ GET /api/authors/{id}/bio-timeline.
type Timeline struct {
	Available bool               `json:"available"`
	QID       string             `json:"qid,omitempty"`
	Name      string             `json:"name,omitempty"`
	Born      string             `json:"born,omitempty"`
	Died      string             `json:"died,omitempty"`
	Chapters  []bio.Chapter      `json:"chapters,omitempty"`
	Events    []EventOut         `json:"events,omitempty"`
	Works     []WorkOut          `json:"works,omitempty"`
	Context   []bio.ContextEvent `json:"context,omitempty"`
	Sources   []bio.Source       `json:"sources,omitempty"`
	Version   string             `json:"version,omitempty"`
}

// minEvents — порог «таймлайн не скучен»: меньше нескрытых вех — секции нет.
const minEvents = 3

type catalogWork struct {
	id    int64
	title string
	norm  string
	qid   string
	year  int
}

// Timeline — таймлайн автора каталога. admin=true — скрытые вехи отдаются с
// флагом, иначе выкидываются. Нет QID или записи в датасете → Available=false.
func (s *Service) Timeline(ctx context.Context, authorID int64, admin bool) (*Timeline, error) {
	var qid *string
	err := s.pool.QueryRow(ctx, `SELECT ext_ids->>'wd_qid' FROM authors WHERE id = $1`, authorID).Scan(&qid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("bio: author %d: %w", authorID, err)
	}
	if qid == nil || *qid == "" {
		return &Timeline{}, nil
	}
	a, hidden, err := s.loadAuthor(ctx, *qid)
	if errors.Is(err, ErrNotFound) {
		return &Timeline{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := &Timeline{QID: a.QID, Name: a.Name, Born: a.Born, Died: a.Died, Chapters: a.Chapters, Sources: a.Sources, Version: s.ds.Version}
	visible := 0
	for _, e := range a.Events {
		h := hidden[e.Key]
		if h && !admin {
			continue
		}
		if !h {
			visible++
		}
		out.Events = append(out.Events, EventOut{Event: e, Hidden: h})
	}
	if visible < minEvents {
		return &Timeline{}, nil
	}
	out.Available = true
	for _, r := range a.Context {
		if c := s.ds.Context[r]; c != nil {
			out.Context = append(out.Context, c.Events...)
		}
	}
	works, err := s.authorWorks(ctx, authorID)
	if err != nil {
		return nil, err
	}
	for _, w := range a.Works {
		wo := WorkOut{Ref: w.Ref, Title: w.Title, QID: w.QID, Written: w.Written}
		if m := matchWork(w, works); m != nil {
			wo.WorkID = m.id
		}
		out.Works = append(out.Works, wo)
	}
	return out, nil
}

// LifeContext — ответ GET /api/works/{id}/life-context.
type LifeContext struct {
	Available bool           `json:"available"`
	Author    *ContextAuthor `json:"author,omitempty"`
	Period    *bio.Written   `json:"period,omitempty"`
	Items     []ContextItem  `json:"items,omitempty"`
}

// ContextAuthor — чей таймлайн.
type ContextAuthor struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// LifeContextForWork — окно вех вокруг периода написания работы каталога.
// Период — из датасета (сопоставленная работа), иначе [written_year−2,
// written_year]; нет ни того, ни другого → Available=false.
func (s *Service) LifeContextForWork(ctx context.Context, workID int64) (*LifeContext, error) {
	var (
		authorID                   *int64
		qid, wqid                  *string
		title, last, first, middle string
		year                       *int16
	)
	err := s.pool.QueryRow(ctx, `
		SELECT w.primary_author_id, w.title, w.ext_ids->>'wd_qid', w.written_year,
		       a.ext_ids->>'wd_qid', COALESCE(a.last_name,''), COALESCE(a.first_name,''), COALESCE(a.middle_name,'')
		FROM works w LEFT JOIN authors a ON a.id = w.primary_author_id
		WHERE w.id = $1`, workID).Scan(&authorID, &title, &wqid, &year, &qid, &last, &first, &middle)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("bio: work %d: %w", workID, err)
	}
	if authorID == nil || qid == nil || *qid == "" {
		return &LifeContext{}, nil
	}
	a, hidden, err := s.loadAuthor(ctx, *qid)
	if errors.Is(err, ErrNotFound) {
		return nil, nil //nolint:nilnil // ниже: «нет записи» = Available=false
	}
	if err != nil {
		return nil, err
	}
	cw := catalogWork{id: workID, title: title, norm: normTitle(title)}
	if wqid != nil {
		cw.qid = *wqid
	}
	var period bio.Written
	ref := ""
	for _, w := range a.Works {
		if m := matchWork(w, []catalogWork{cw}); m != nil {
			ref = w.Ref
			if w.Written != nil {
				period = *w.Written
			}
			break
		}
	}
	if period.From == 0 {
		if year == nil || *year == 0 {
			return &LifeContext{}, nil
		}
		period = bio.Written{From: int(*year) - 2, To: int(*year)}
	}
	items := Window(a, ref, period, hidden)
	if len(items) == 0 {
		return &LifeContext{}, nil
	}
	name := strings.TrimSpace(strings.Join([]string{last, first, middle}, " "))
	name = strings.Join(strings.Fields(name), " ")
	return &LifeContext{Available: true, Author: &ContextAuthor{ID: *authorID, Name: name}, Period: &period, Items: items}, nil
}

func (s *Service) authorWorks(ctx context.Context, authorID int64) ([]catalogWork, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, title, COALESCE(ext_ids->>'wd_qid', ''), COALESCE(written_year, 0)
		FROM works WHERE primary_author_id = $1 ORDER BY edition_count DESC, id`, authorID)
	if err != nil {
		return nil, fmt.Errorf("bio: works of %d: %w", authorID, err)
	}
	defer rows.Close()
	var out []catalogWork
	for rows.Next() {
		var w catalogWork
		var y int16
		if err := rows.Scan(&w.id, &w.title, &w.qid, &y); err != nil {
			return nil, err
		}
		w.year = int(y)
		w.norm = normTitle(w.title)
		out = append(out, w)
	}
	return out, rows.Err()
}

// matchWork — работа датасета → работа каталога: сначала QID, потом точное
// нормализованное название (с алиасами). Первое совпадение (каталожные
// работы отсортированы по числу изданий). Нет совпадения — nil.
func matchWork(w bio.Work, works []catalogWork) *catalogWork {
	if w.QID != "" {
		for i := range works {
			if works[i].qid == w.QID {
				return &works[i]
			}
		}
	}
	titles := append([]string{w.Title}, w.Aliases...)
	for _, t := range titles {
		n := normTitle(t)
		if n == "" {
			continue
		}
		for i := range works {
			if works[i].norm == n {
				return &works[i]
			}
		}
	}
	return nil
}

// normTitle — нижний регистр, ё→е, только буквы/цифры, одиночные пробелы:
// «Бойня номер пять, или Крестовый поход детей» = «бойня номер пять или
// крестовый поход детей». Своя нормализация, чтобы не зависеть от
// normalized_title импорта.
func normTitle(s string) string {
	var b strings.Builder
	space := true
	for _, r := range strings.ToLower(s) {
		switch {
		case r == 'ё':
			b.WriteRune('е')
			space = false
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			space = false
		default:
			if !space {
				b.WriteRune(' ')
				space = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}
