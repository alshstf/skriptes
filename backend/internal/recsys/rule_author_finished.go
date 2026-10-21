package recsys

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// AuthorFinished — «Автор, которого вы дочитали» (план recsys/02, задача 2.2):
// автор, у которого пользователь прочитал хотя бы одну работу и оценки его
// работ в среднем ≥ AuthorMinAvgRating (а без оценок — прочитано ≥
// AuthorMinReadUnrated), — следующая его работа по году написания после
// последней прочитанной; без хронологии (год неизвестен или позже ничего нет) —
// самая известная непрочитанная (число изданий, затем рейтинг Фантлаба — прокси
// известности в PG; индекс works сюда не тянем). Сборники и работы с
// > MaxAuthors авторами не считаются ни прочитанным, ни кандидатом; служебные и
// скрытые на инстансе авторы — мимо. Порядок секции — по близости к автору.
type AuthorFinished struct{}

func (AuthorFinished) Name() string         { return "author_you_finished" }
func (AuthorFinished) Section() SectionKind { return SectionAuthorFinished }

// Пороги «автор понравился» — на глаз (план recsys/02): правятся по харнессу.
const (
	AuthorMinAvgRating   = 4.0
	AuthorMinReadUnrated = 2
)

// authorAnchor — что пользователь прочитал у автора и что из этого последнее.
type authorAnchor struct {
	read      int
	rated     int
	ratingSum float64
	lastWork  int64
	lastTitle string
	lastYear  int
	lastAt    time.Time
}

// liked — автор понравился: средняя оценка его работ ≥ порога, а без оценок —
// прочитано не меньше AuthorMinReadUnrated.
func (a *authorAnchor) liked() bool {
	if a.read == 0 {
		return false
	}
	if a.rated > 0 {
		return a.ratingSum/float64(a.rated) >= AuthorMinAvgRating
	}
	return a.read >= AuthorMinReadUnrated
}

func (AuthorFinished) Candidates(ctx context.Context, q Querier, p *Profile, n int) ([]Candidate, error) {
	read := p.readWorks()
	if len(read) == 0 || n <= 0 {
		return nil, nil
	}
	meta, err := loadWorkMeta(ctx, q, read)
	if err != nil {
		return nil, fmt.Errorf("author finished: %w", err)
	}
	anchors := map[int64]*authorAnchor{}
	for _, wid := range read {
		m := meta[wid]
		if m == nil || m.Kind != "" || len(m.Authors) > MaxAuthors {
			continue
		}
		s := p.Signals[wid]
		at := s.FinishedAt()
		for _, aid := range m.Authors {
			a := anchors[aid]
			if a == nil {
				a = &authorAnchor{}
				anchors[aid] = a
			}
			a.read++
			if s.Rating > 0 {
				a.ratingSum += float64(s.Rating)
				a.rated++
			}
			if a.lastWork == 0 || at.After(a.lastAt) {
				a.lastWork, a.lastTitle, a.lastYear, a.lastAt = wid, m.Title, m.Year, at
			}
		}
	}
	liked := make([]int64, 0, len(anchors))
	for aid, a := range anchors {
		if a.liked() {
			liked = append(liked, aid)
		}
	}
	if len(liked) == 0 {
		return nil, nil
	}

	// Имена; служебные авторы («Коллектив авторов») и скрытые на инстансе — мимо.
	names := map[int64]string{}
	if err := loadAuthorNames(ctx, q, liked, names); err != nil {
		return nil, fmt.Errorf("author finished: names: %w", err)
	}
	authorIDs := make([]int64, 0, len(names))
	for _, aid := range liked {
		if _, ok := names[aid]; ok {
			authorIDs = append(authorIDs, aid)
		}
	}
	if len(authorIDs) == 0 {
		return nil, nil
	}

	// Кандидаты: работы этих авторов (не сборники, ≤ MaxAuthors авторов, с живым
	// изданием) — по году написания, внутри года — по известности.
	type cand struct {
		workID   int64
		year     int
		editions int
		rating   float64
	}
	byAuthor := map[int64][]cand{}
	if err := each(ctx, q, `
		SELECT DISTINCT ba.author_id, w.id, COALESCE(w.written_year, 0)::int AS year,
		       w.edition_count, COALESCE(w.fantlab_rating, 0)::float8 AS fl
		FROM works w
		JOIN books b ON b.work_id = w.id AND NOT b.deleted AND NOT b.instance_hidden
		JOIN book_authors ba ON ba.book_id = b.id AND ba.author_id = ANY($1)
		WHERE NOT w.instance_hidden AND COALESCE(w.kind, '') = ''
		  AND (SELECT count(*) FROM book_authors x WHERE x.book_id = b.id) <= $2
		ORDER BY ba.author_id, year, w.edition_count DESC, fl DESC, w.id`,
		[]any{authorIDs, MaxAuthors}, func(rows pgx.Rows) error {
			var (
				aid int64
				c   cand
			)
			if err := rows.Scan(&aid, &c.workID, &c.year, &c.editions, &c.rating); err != nil {
				return err
			}
			byAuthor[aid] = append(byAuthor[aid], c)
			return nil
		}); err != nil {
		return nil, fmt.Errorf("author finished: candidates: %w", err)
	}

	out := make([]Candidate, 0, len(authorIDs))
	seen := map[int64]struct{}{}
	for _, aid := range authorIDs {
		a := anchors[aid]
		var (
			pick  cand
			found bool
		)
		if a.lastYear > 0 {
			for _, c := range byAuthor[aid] {
				if c.year > a.lastYear && !p.Engaged(c.workID) {
					pick, found = c, true
					break
				}
			}
		}
		if !found {
			for _, c := range byAuthor[aid] {
				if p.Engaged(c.workID) {
					continue
				}
				if !found || c.editions > pick.editions || (c.editions == pick.editions && c.rating > pick.rating) {
					pick, found = c, true
				}
			}
		}
		if !found {
			continue
		}
		if _, dup := seen[pick.workID]; dup {
			continue
		}
		seen[pick.workID] = struct{}{}
		out = append(out, Candidate{
			WorkID: pick.workID,
			Score:  p.Authors[aid],
			Reason: Reason{
				Kind: ReasonAuthorFinished, AuthorID: aid, Author: names[aid],
				WorkID: a.lastWork, Title: a.lastTitle, At: timePtr(a.lastAt),
			},
		})
	}
	sortCandidates(out)
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}
