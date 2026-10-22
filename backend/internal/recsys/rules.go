package recsys

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

// Правила фазы 2 (план recsys/02): детерминированные рекомендации на
// имеющихся данных — сигналы профиля + SQL по каталогу. Каждое правило — одна
// секция ленты с одной причиной. Сторона пользователя берётся ТОЛЬКО из
// Profile.Signals, а не запросами по user_id: харнесс cmd/recseval прячет
// часть сигналов (leave-last-out), и правило не должно их подсмотреть.
// Видимость каталога (скрытое на инстансе, живое издание) правило проверяет
// само; скрытые пользователем жанры/языки/сборники отсекает Hydrate.

// Candidate — работа-кандидат правила: причина и вес для порядка внутри
// секции (больше — выше).
type Candidate struct {
	WorkID int64
	Score  float64
	Reason Reason
}

// Rule — правило-рекомендатель: до n кандидатов по профилю и каталогу.
type Rule interface {
	Name() string
	Section() SectionKind
	Candidates(ctx context.Context, q Querier, p *Profile, n int) ([]Candidate, error)
}

// Rules — правила в порядке секций ленты (pages/HomePage.tsx держит тот же).
// «Забытая классика» читает пул DefaultClassics — его наполняет фоновый
// RefreshLoop (cmd/skriptes/main.go). «Вы хотели прочитать» — фаза 3 (3.3).
var Rules = []Rule{
	NextInSeries{}, AuthorFinished{}, ToReadReminder{}, DroppedThenLoved{},
	ForgottenClassic{Pool: DefaultClassics}, Reread{},
}

// RuleFor — правило секции kind.
func RuleFor(kind SectionKind) (Rule, bool) {
	for _, r := range Rules {
		if r.Section() == kind {
			return r, true
		}
	}
	return nil, false
}

// Read — пользователь прочитал работу: дочитал или поставил оценку (оценка
// без отметки о прочтении — обычное дело при чтении на Kindle).
func (s *WorkSignals) Read() bool { return s != nil && (s.Completed || s.Rating > 0) }

// FinishedAt — когда прочитал: позднее из «дочитано» и «оценено» (нулевое —
// не читал или время неизвестно). Не путать с полем ReadAt — последним
// движением по изданию.
func (s *WorkSignals) FinishedAt() time.Time {
	if s == nil {
		return time.Time{}
	}
	at := s.CompletedAt
	if s.Rating > 0 && s.RatedAt.After(at) {
		at = s.RatedAt
	}
	return at
}

// Engaged — работа уже «в руках» пользователя, предлагать её нечего: прочитана,
// открыта в ридере, приобретена (OPDS/Kindle), скрыта из ленты или «никогда не
// спрашивать оценку». Просмотр карточки, ★ и полка — не в счёт: отложенную на
// полку книгу предложить как раз уместно.
func (p *Profile) Engaged(workID int64) bool {
	if p == nil {
		return false
	}
	s := p.Signals[workID]
	return s != nil && (s.Read() || s.Opened || s.Acquired || s.Dismissed || s.PromptNever)
}

// readWorks — прочитанные работы профиля (порядок — по id, детерминированно).
func (p *Profile) readWorks() []int64 {
	if p == nil {
		return nil
	}
	out := make([]int64, 0, len(p.Signals))
	for id, s := range p.Signals {
		if s.Read() {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// loadAuthorNames — имена авторов ids в names; служебные («Коллектив
// авторов») и скрытые на инстансе — не попадают (их не предлагаем).
func loadAuthorNames(ctx context.Context, q Querier, ids []int64, names map[int64]string) error {
	if len(ids) == 0 {
		return nil
	}
	return each(ctx, q, `
		SELECT id, TRIM(CONCAT_WS(' ', last_name, first_name, middle_name))
		FROM authors WHERE id = ANY($1) AND NOT is_service AND NOT instance_hidden`,
		[]any{ids}, func(rows pgx.Rows) error {
			var (
				id   int64
				name string
			)
			if err := rows.Scan(&id, &name); err != nil {
				return err
			}
			names[id] = name
			return nil
		})
}

// sortCandidates — по убыванию Score, при равенстве — по id работы.
func sortCandidates(c []Candidate) {
	sort.SliceStable(c, func(i, j int) bool {
		if c[i].Score != c[j].Score {
			return c[i].Score > c[j].Score
		}
		return c[i].WorkID < c[j].WorkID
	})
}

// timePtr — указатель на ненулевое время (нулевое → nil, в JSON не уходит).
func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// RuleRecommender — правило как Recommender харнесса (cmd/recseval).
type RuleRecommender struct {
	Rule Rule
	Q    Querier
}

func (r RuleRecommender) Name() string { return r.Rule.Name() }

func (r RuleRecommender) Recommend(ctx context.Context, p *Profile, n int) ([]int64, error) {
	cands, err := r.Rule.Candidates(ctx, r.Q, p, n)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", r.Rule.Name(), err)
	}
	out := make([]int64, 0, len(cands))
	for _, c := range cands {
		out = append(out, c.WorkID)
	}
	return out, nil
}

// RulesRecommender — все правила вместе, как их видит лента: списки правил по
// порядку Rules, без повторов — «rules» в харнессе.
type RulesRecommender struct {
	Rules []Rule
	Q     Querier
}

func (RulesRecommender) Name() string { return "rules" }

func (r RulesRecommender) Recommend(ctx context.Context, p *Profile, n int) ([]int64, error) {
	seen := map[int64]struct{}{}
	out := make([]int64, 0, n)
	for _, rule := range r.Rules {
		if len(out) >= n {
			break
		}
		cands, err := rule.Candidates(ctx, r.Q, p, n-len(out))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rule.Name(), err)
		}
		for _, c := range cands {
			if _, ok := seen[c.WorkID]; ok {
				continue
			}
			seen[c.WorkID] = struct{}{}
			out = append(out, c.WorkID)
			if len(out) >= n {
				break
			}
		}
	}
	return out, nil
}
