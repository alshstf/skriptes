package recsys

import (
	"context"
	"fmt"
)

// DroppedThenLoved — «Брошенное, к чему дорос» (план recsys/02, задача 2.3):
// работа, брошенная в начале (доля < DroppedMaxFraction, последнее движение
// старше DroppedMinAge — то же определение, что у слагаемого KindDropped модели
// близости), автора которой пользователь с тех пор полюбил: после даты
// бросания — не меньше DroppedLovedMin его работ с оценкой ≥ 4 или дочитанных.
// Предлагаем вернуться к брошенной. Единственное правило, которое нарочно
// отдаёт работу «в руках» (она открыта в ридере); скрытая из ленты — нет.
// Порядок — больше полюбленных работ, затем недавнее бросание.
type DroppedThenLoved struct{}

func (DroppedThenLoved) Name() string         { return "dropped_then_loved_author" }
func (DroppedThenLoved) Section() SectionKind { return SectionDropped }

// DroppedLovedMin — сколько работ автора нужно полюбить после бросания.
// Порог на глаз (план recsys/02); правится по харнессу.
const DroppedLovedMin = 2

func (DroppedThenLoved) Candidates(ctx context.Context, q Querier, p *Profile, n int) ([]Candidate, error) {
	if p == nil || n <= 0 {
		return nil, nil
	}
	var dropped, all []int64
	for id, a := range p.Works {
		all = append(all, id)
		s := p.Signals[id]
		if a.Parts[KindDropped] != 0 && !s.Dismissed && !s.PromptNever {
			dropped = append(dropped, id)
		}
	}
	if len(dropped) == 0 {
		return nil, nil
	}
	meta, err := loadWorkMeta(ctx, q, all)
	if err != nil {
		return nil, fmt.Errorf("dropped then loved: %w", err)
	}
	// Полюбленные работы по автору: (оценка ≥ 4 или дочитано) с датой — чтобы
	// сравнить с датой бросания. Антологии и сборники — не в счёт.
	type loved struct {
		workID int64
		at     int64 // unix
	}
	byAuthor := map[int64][]loved{}
	for id, s := range p.Signals {
		m := meta[id]
		if m == nil || m.Kind != "" || len(m.Authors) > MaxAuthors {
			continue
		}
		var at int64
		switch {
		case s.Rating >= 4:
			at = s.RatedAt.Unix()
		case s.Completed:
			at = s.CompletedAt.Unix()
		default:
			continue
		}
		for _, aid := range m.Authors {
			byAuthor[aid] = append(byAuthor[aid], loved{workID: id, at: at})
		}
	}
	names := map[int64]string{}
	out := make([]Candidate, 0, len(dropped))
	for _, id := range dropped {
		m := meta[id]
		s := p.Signals[id]
		if m == nil || m.Kind != "" || len(m.Authors) > MaxAuthors {
			continue
		}
		droppedAt := s.ReadAt.Unix()
		bestAuthor, bestCount := int64(0), 0
		for _, aid := range m.Authors {
			count := 0
			for _, l := range byAuthor[aid] {
				if l.workID != id && l.at > droppedAt {
					count++
				}
			}
			if count > bestCount || (count == bestCount && count > 0 && aid < bestAuthor) {
				bestAuthor, bestCount = aid, count
			}
		}
		if bestCount < DroppedLovedMin {
			continue
		}
		names[bestAuthor] = ""
		at := s.ReadAt
		out = append(out, Candidate{
			WorkID: id,
			Score:  float64(bestCount)*1e13 + float64(at.UnixMilli()),
			Reason: Reason{Kind: ReasonDropped, At: timePtr(at), AuthorID: bestAuthor, Count: bestCount},
		})
	}
	if len(out) == 0 {
		return nil, nil
	}
	ids := make([]int64, 0, len(names))
	for aid := range names {
		ids = append(ids, aid)
	}
	if err := loadAuthorNames(ctx, q, ids, names); err != nil {
		return nil, fmt.Errorf("dropped then loved: %w", err)
	}
	kept := out[:0]
	for _, c := range out {
		name, ok := names[c.Reason.AuthorID]
		if !ok || name == "" {
			continue // служебный или скрытый автор
		}
		c.Reason.Author = name
		kept = append(kept, c)
	}
	out = kept
	sortCandidates(out)
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}
