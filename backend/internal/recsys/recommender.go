package recsys

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Recommender — источник кандидатов для ленты и харнесса (план recsys/01 1.5):
// по профилю пользователя — до n работ, лучшие первыми, без уже известных ему
// (p.Works). Правила фазы 2 и векторы фазы 5 реализуют этот же интерфейс, и
// харнесс сравнивает их с бейзлайнами ниже на одних данных.
type Recommender interface {
	Name() string
	Recommend(ctx context.Context, p *Profile, n int) ([]int64, error)
}

// knownIDs — работы, с которыми пользователь уже взаимодействовал: их не
// рекомендуем (и не засчитываем).
func knownIDs(p *Profile) []int64 {
	if p == nil {
		return nil
	}
	out := make([]int64, 0, len(p.Works))
	for id := range p.Works {
		out = append(out, id)
	}
	return out
}

// PopularityRecommender — бейзлайн «самое известное»: Top отдаёт видимые работы
// по убыванию popularity индекса works (books.Service.TopPopularWorkIDs).
type PopularityRecommender struct {
	Top func(ctx context.Context, offset, n int) ([]int64, error)
}

func (PopularityRecommender) Name() string { return "popularity" }

func (r PopularityRecommender) Recommend(ctx context.Context, p *Profile, n int) ([]int64, error) {
	known := map[int64]struct{}{}
	for _, id := range knownIDs(p) {
		known[id] = struct{}{}
	}
	out := make([]int64, 0, n)
	for offset := 0; len(out) < n && offset < 10*n+len(known); offset += n {
		page, err := r.Top(ctx, offset, n)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		for _, id := range page {
			if _, ok := known[id]; ok {
				continue
			}
			out = append(out, id)
			if len(out) == n {
				break
			}
		}
	}
	return out, nil
}

// SameAuthorRecommender — бейзлайн «тот же автор»: работы авторов с
// положительной близостью (Profile.Authors > 0), по убыванию числа изданий
// (прокси известности в PG); антологии не в счёт (MaxAuthors).
type SameAuthorRecommender struct {
	Q Querier
}

func (SameAuthorRecommender) Name() string { return "same_author" }

func (r SameAuthorRecommender) Recommend(ctx context.Context, p *Profile, n int) ([]int64, error) {
	if p == nil || len(p.Authors) == 0 {
		return nil, nil
	}
	authors := make([]int64, 0, len(p.Authors))
	for id, score := range p.Authors {
		if score > 0 {
			authors = append(authors, id)
		}
	}
	if len(authors) == 0 {
		return nil, nil
	}
	rows, err := r.Q.Query(ctx, `
		SELECT w.id
		FROM works w
		WHERE NOT w.instance_hidden
		  AND w.id <> ALL($2)
		  AND EXISTS (
		      SELECT 1 FROM books b
		      JOIN book_authors ba ON ba.book_id = b.id
		      WHERE b.work_id = w.id AND b.deleted = false AND NOT b.instance_hidden
		        AND ba.author_id = ANY($1)
		        AND (SELECT count(*) FROM book_authors x WHERE x.book_id = b.id) <= $4)
		ORDER BY w.edition_count DESC, w.fantlab_rating DESC NULLS LAST, w.id
		LIMIT $3`, authors, knownIDs(p), n, MaxAuthors)
	if err != nil {
		return nil, fmt.Errorf("same author: %w", err)
	}
	defer rows.Close()
	return scanIDs(rows)
}

// RandomRecommender — бейзлайн «случайное»: n видимых работ с живым изданием,
// детерминированно по Seed.
type RandomRecommender struct {
	Q    Querier
	Seed int64
}

func (RandomRecommender) Name() string { return "random" }

func (r RandomRecommender) Recommend(ctx context.Context, p *Profile, n int) ([]int64, error) {
	rows, err := r.Q.Query(ctx, `
		SELECT w.id FROM works w
		WHERE NOT w.instance_hidden AND w.id <> ALL($1)
		  AND EXISTS (SELECT 1 FROM books b WHERE b.work_id = w.id AND b.deleted = false AND NOT b.instance_hidden)
		ORDER BY md5(w.id::text || $2::text)
		LIMIT $3`, knownIDs(p), strconv.FormatInt(r.Seed, 10), n)
	if err != nil {
		return nil, fmt.Errorf("random: %w", err)
	}
	defer rows.Close()
	return scanIDs(rows)
}

func scanIDs(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}) ([]int64, error) {
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Names — имена рекомендателей через запятую (для логов и сводки).
func Names(rs []Recommender) string {
	names := make([]string, len(rs))
	for i, r := range rs {
		names[i] = r.Name()
	}
	return strings.Join(names, ", ")
}
