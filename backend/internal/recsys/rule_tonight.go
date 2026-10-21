package recsys

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/jackc/pgx/v5"
)

// FitsTonight — «На вечер» (план recsys/02, задача 2.6): «у меня есть N минут».
// Работы, которые при скорости чтения пользователя укладываются в Minutes
// (размер самого маленького живого издания ≤ Minutes × BytesPerMin — длина
// текста пока = размер fb2, как в скорости чтения), в жанрах с положительной
// близостью (Profile.Genres), ещё не тронутые. Без личной скорости —
// DefaultBytesPerMin. Порядок — известность (число изданий, рейтинг Фантлаба)
// с бонусом за силу жанра. В Rules не входит (нужен параметр минут; в харнессе
// не участвует) — зовёт Feed.Tonight.
type FitsTonight struct {
	Minutes     int
	BytesPerMin float64
}

const (
	// DefaultBytesPerMin — скорость, когда личной нет (новый пользователь,
	// Kindle): ≈ 200 слов/мин кириллицей в fb2 (UTF-8 — два байта на букву,
	// плюс разметка). Единица та же, что у recsys.Speed.
	DefaultBytesPerMin = 2500.0
	// TonightDefaultMinutes / TonightMaxMinutes — чип по умолчанию и потолок запроса.
	TonightDefaultMinutes = 40
	TonightMaxMinutes     = 600
	// tonightGenrePerCap / tonightGenreBonusCap — бонус за каждый совпавший
	// любимый жанр (близость, не больше tonightGenrePerCap, делённая на него —
	// до 1 за жанр) и предел суммы (в «изданиях»): два любимых жанра — выше одного.
	tonightGenrePerCap   = 3.0
	tonightGenreBonusCap = 3.0
)

func (FitsTonight) Name() string         { return "fits_tonight" }
func (FitsTonight) Section() SectionKind { return SectionFitsTonight }

// MaxBytes — размер издания, который укладывается в Minutes.
func (r FitsTonight) MaxBytes() int64 {
	bpm := r.BytesPerMin
	if bpm <= 0 {
		bpm = DefaultBytesPerMin
	}
	return int64(float64(r.Minutes) * bpm)
}

// MinutesFor — минут на bytes при скорости правила.
func (r FitsTonight) MinutesFor(bytes int64) int {
	bpm := r.BytesPerMin
	if bpm <= 0 {
		bpm = DefaultBytesPerMin
	}
	return int(math.Round(float64(bytes) / bpm))
}

func (r FitsTonight) Candidates(ctx context.Context, q Querier, p *Profile, n int) ([]Candidate, error) {
	if p == nil || n <= 0 || r.Minutes <= 0 {
		return nil, nil
	}
	genres := make([]string, 0, len(p.Genres))
	for g, score := range p.Genres {
		if score > 0 {
			genres = append(genres, g)
		}
	}
	if len(genres) == 0 {
		return nil, nil
	}
	sort.Strings(genres)
	engaged := make([]int64, 0, len(p.Signals))
	for id := range p.Signals {
		if p.Engaged(id) {
			engaged = append(engaged, id)
		}
	}
	type row struct {
		workID   int64
		size     int64
		editions int
		fl       float64
		genres   []string
	}
	var rows []row
	if err := each(ctx, q, `
		SELECT w.id, min(b.size_bytes), w.edition_count, COALESCE(w.fantlab_rating, 0)::float8,
		       array_agg(DISTINCT g.fb2_code)
		FROM works w
		JOIN books b ON b.work_id = w.id AND NOT b.deleted AND NOT b.instance_hidden
		     AND b.size_bytes > 0 AND b.size_bytes <= $1
		JOIN book_genres bg ON bg.book_id = b.id
		JOIN genres g ON g.id = bg.genre_id AND g.fb2_code = ANY($2)
		WHERE NOT w.instance_hidden AND COALESCE(w.kind, '') = '' AND w.id <> ALL($3)
		GROUP BY w.id
		ORDER BY w.edition_count DESC, 4 DESC, w.id
		LIMIT $4`,
		[]any{r.MaxBytes(), genres, engaged, 3 * n}, func(rs pgx.Rows) error {
			var x row
			if err := rs.Scan(&x.workID, &x.size, &x.editions, &x.fl, &x.genres); err != nil {
				return err
			}
			rows = append(rows, x)
			return nil
		}); err != nil {
		return nil, fmt.Errorf("fits tonight: %w", err)
	}
	out := make([]Candidate, 0, len(rows))
	for _, x := range rows {
		// Сильнейший из совпавших жанров — в причину; сумма — бонус к известности.
		best, bestScore, bonus := "", 0.0, 0.0
		for _, g := range x.genres {
			s := p.Genres[g]
			bonus += math.Min(s, tonightGenrePerCap) / tonightGenrePerCap
			if s > bestScore {
				best, bestScore = g, s
			}
		}
		out = append(out, Candidate{
			WorkID: x.workID,
			Score:  float64(x.editions) + x.fl/10 + math.Min(bonus, tonightGenreBonusCap),
			Reason: Reason{Kind: ReasonFitsTonight, Minutes: r.MinutesFor(x.size), Genre: best},
		})
	}
	sortCandidates(out)
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}
