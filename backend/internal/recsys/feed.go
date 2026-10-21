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

// Related — «раз понравилось — вот следующая» (план recsys/02, задача 2.8):
// до limit карточек правил «дальше в цикле» и «автор, которого дочитали»,
// основание которых — работа workID (её только что оценили ≥ 4). Пусто, если
// по этой работе правилам нечего предложить.
func (f *Feed) Related(ctx context.Context, userID, workID int64, ex Exclusions, limit int) ([]Item, error) {
	if limit <= 0 || limit > 20 {
		limit = 3
	}
	p, err := BuildProfile(ctx, f.Q, userID, f.Now(), f.Weights)
	if err != nil {
		return nil, err
	}
	if p.IsEmpty() {
		return []Item{}, nil
	}
	var cands []Candidate
	for _, kind := range []SectionKind{SectionNextInSeries, SectionAuthorFinished} {
		rule, ok := RuleFor(kind)
		if !ok {
			continue
		}
		cs, err := rule.Candidates(ctx, f.Q, p, 50)
		if err != nil {
			return nil, fmt.Errorf("recsys: %s: %w", rule.Name(), err)
		}
		for _, c := range cs {
			if c.Reason.WorkID == workID {
				cands = append(cands, c)
			}
		}
	}
	return Hydrate(ctx, f.Q, cands, ex, limit)
}

// Tonight — секция «На вечер» (recsys/02, 2.6): до limit работ, которые
// укладываются в minutes при скорости speed (неизвестна — DefaultBytesPerMin),
// в любимых жанрах пользователя. minutes вне 1…TonightMaxMinutes →
// TonightDefaultMinutes.
func (f *Feed) Tonight(ctx context.Context, userID int64, minutes int, speed Speed, ex Exclusions, limit int) ([]Item, error) {
	if minutes <= 0 || minutes > TonightMaxMinutes {
		minutes = TonightDefaultMinutes
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
	rule := FitsTonight{Minutes: minutes}
	if speed.Known() {
		rule.BytesPerMin = speed.BytesPerMin
	}
	cands, err := rule.Candidates(ctx, f.Q, p, 3*limit)
	if err != nil {
		return nil, fmt.Errorf("recsys: %s: %w", rule.Name(), err)
	}
	return Hydrate(ctx, f.Q, cands, ex, limit)
}
