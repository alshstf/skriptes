package recsys

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// ErrUnknownSection — секции с таким kind у правил нет.
var ErrUnknownSection = errors.New("recsys: unknown section")

// Feed — секции ленты по правилам (план recsys/02): профиль пользователя →
// кандидаты правила → карточки с учётом скрытого. Профиль строится на каждый
// запрос секции (семь коротких запросов по user_id — на семейном инстансе
// миллисекунды); кэш появится, когда понадобится.
type Feed struct {
	Q       Querier
	Weights Weights
	Now     func() time.Time
}

// NewFeed — лента с весами по умолчанию.
func NewFeed(q Querier) *Feed {
	return &Feed{Q: q, Weights: DefaultWeights, Now: time.Now}
}

// Section — карточки секции kind для пользователя; неизвестная секция —
// ErrUnknownSection. ex — скрытое у пользователя (жанры/языки/сборники).
func (f *Feed) Section(ctx context.Context, kind SectionKind, userID int64, ex Exclusions, limit int) ([]Item, error) {
	rule, ok := RuleFor(kind)
	if !ok {
		return nil, ErrUnknownSection
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	p, err := BuildProfile(ctx, f.Q, userID, f.Now(), f.Weights)
	if err != nil {
		return nil, err
	}
	if p.IsEmpty() {
		return []Item{}, nil
	}
	// Кандидатов — с запасом: часть отсеет скрытое у пользователя.
	cands, err := rule.Candidates(ctx, f.Q, p, 3*limit)
	if err != nil {
		return nil, fmt.Errorf("recsys: %s: %w", rule.Name(), err)
	}
	return Hydrate(ctx, f.Q, cands, ex, limit)
}
