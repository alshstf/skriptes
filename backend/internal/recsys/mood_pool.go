package recsys

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// Секция «Под настроение» (#580, доработка 3.4). В 1.62.0 настроение только
// пересортировывало кандидатов секций правил — у пользователя с короткой лентой
// выбор чипа ничего видимого не менял. Теперь при выбранном настроении сверху
// ленты — своя секция: известные работы каталога в жанрах настроения
// (mood-genres.json), «короткое» — по размеру издания. Выборка по каталогу —
// секунда-полторы на настроение на проде, поэтому, как «Забытая классика», она
// считается фоном в пул MoodPool (RefreshLoop), а правило ForMood ранжирует пул
// под пользователя: его любимые жанры выше, тронутое им — нет, не больше
// moodPerAuthor работ одного автора.

const (
	// MoodPoolPerMood — работ на настроение в пуле.
	MoodPoolPerMood = 1500
	// MoodRefreshEvery — период фонового пересчёта.
	MoodRefreshEvery = 6 * time.Hour
	// moodPerAuthor — не больше стольких работ одного автора в секции.
	moodPerAuthor = 2
	// moodGenreBonusCap — предел бонуса за любимые жанры (в «изданиях», как у «На вечер»).
	moodGenreBonusCap = 3.0
)

// MoodCandidate — работа пула настроения.
type MoodCandidate struct {
	WorkID   int64
	AuthorID int64
	Editions int
	Fantlab  float64 // средняя Фантлаба 1–10 (0 — нет)
	Genres   []string
	Bytes    int64 // самое маленькое живое издание
	Match    float64
}

// MoodPool — пулы кандидатов по настроениям; наполняется Refresh.
type MoodPool struct {
	mu    sync.RWMutex
	items map[Mood][]MoodCandidate
	at    time.Time
}

// DefaultMoods — пул, который читает секция «Под настроение».
var DefaultMoods = &MoodPool{}

// Snapshot — пул настроения m и время пересчёта (нулевое — ещё не считался).
func (p *MoodPool) Snapshot(m Mood) ([]MoodCandidate, time.Time) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.items[m], p.at
}

// Set — подменить пул (тесты и Refresh).
func (p *MoodPool) Set(items map[Mood][]MoodCandidate, at time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.items, p.at = items, at
}

// Refresh — пересчитать пулы всех настроений: самые известные работы (число
// изданий, затем Фантлаб) в жанрах настроения, у «короткого» — с изданием не
// больше MaxBytes. Только видимое на инстансе, без сборников и служебных авторов.
func (p *MoodPool) Refresh(ctx context.Context, q Querier, now time.Time) (int, error) {
	out := make(map[Mood][]MoodCandidate, len(AllMoods))
	total := 0
	for _, m := range AllMoods {
		spec, _ := MoodSpecOf(m)
		items, err := refreshMood(ctx, q, m, spec)
		if err != nil {
			return 0, err
		}
		out[m] = items
		total += len(items)
	}
	p.Set(out, now)
	return total, nil
}

func refreshMood(ctx context.Context, q Querier, m Mood, spec MoodSpec) ([]MoodCandidate, error) {
	var (
		sql  string
		args []any
	)
	if spec.MaxBytes > 0 {
		// «Короткое» — по размеру: жанр любой, жанры — для бонуса и причины.
		sql = `
			SELECT w.id, COALESCE(w.primary_author_id, 0), w.edition_count, COALESCE(w.fantlab_rating, 0)::float8,
			       COALESCE(array_agg(DISTINCT g.fb2_code) FILTER (WHERE g.fb2_code IS NOT NULL), ARRAY[]::text[]),
			       min(b.size_bytes)
			FROM works w
			JOIN books b ON b.work_id = w.id AND NOT b.deleted AND NOT b.instance_hidden
			     AND b.size_bytes > 0 AND b.size_bytes <= $1
			LEFT JOIN book_genres bg ON bg.book_id = b.id
			LEFT JOIN genres g ON g.id = bg.genre_id
			LEFT JOIN authors a ON a.id = w.primary_author_id
			WHERE NOT w.instance_hidden AND COALESCE(w.kind, '') = '' AND NOT COALESCE(a.is_service, false)
			GROUP BY w.id
			ORDER BY w.edition_count DESC, 4 DESC, w.id
			LIMIT $2`
		args = []any{spec.MaxBytes, MoodPoolPerMood}
	} else {
		genres := make([]string, 0, len(spec.Genres))
		for g, w := range spec.Genres {
			if w > 0 {
				genres = append(genres, g)
			}
		}
		sort.Strings(genres)
		sql = `
			SELECT w.id, COALESCE(w.primary_author_id, 0), w.edition_count, COALESCE(w.fantlab_rating, 0)::float8,
			       array_agg(DISTINCT g.fb2_code),
			       COALESCE(min(b.size_bytes) FILTER (WHERE b.size_bytes > 0), 0)
			FROM works w
			JOIN books b ON b.work_id = w.id AND NOT b.deleted AND NOT b.instance_hidden
			JOIN book_genres bg ON bg.book_id = b.id
			JOIN genres g ON g.id = bg.genre_id AND g.fb2_code = ANY($1)
			LEFT JOIN authors a ON a.id = w.primary_author_id
			WHERE NOT w.instance_hidden AND COALESCE(w.kind, '') = '' AND NOT COALESCE(a.is_service, false)
			GROUP BY w.id
			ORDER BY w.edition_count DESC, 4 DESC, w.id
			LIMIT $2`
		args = []any{genres, MoodPoolPerMood}
	}
	var items []MoodCandidate
	err := each(ctx, q, sql, args, func(rows pgx.Rows) error {
		var it MoodCandidate
		if err := rows.Scan(&it.WorkID, &it.AuthorID, &it.Editions, &it.Fantlab, &it.Genres, &it.Bytes); err != nil {
			return err
		}
		if it.Match = MoodMatch(m, it.Genres, it.Bytes); it.Match > 0 {
			items = append(items, it)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("recsys: mood pool %s: %w", m, err)
	}
	return items, nil
}

// RefreshLoop — пересчёт на старте (с задержкой delay) и раз в MoodRefreshEvery;
// запускать через metadata.Go (грабля 23). Ошибка — в лог, следующий раз по расписанию.
func (p *MoodPool) RefreshLoop(ctx context.Context, q Querier, delay time.Duration, logger *slog.Logger) {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		rctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		started := time.Now()
		n, err := p.Refresh(rctx, q, time.Now())
		cancel()
		if err != nil {
			if ctx.Err() == nil && logger != nil {
				logger.Warn("mood pool: refresh failed", "err", err)
			}
		} else if logger != nil {
			logger.Info("mood pool: refreshed", "works", n, "took", time.Since(started).Round(time.Millisecond))
		}
		timer.Reset(MoodRefreshEvery)
	}
}

// ForMood — секция «Под настроение»: пул настроения → под пользователя. Вес —
// известность (издания + Фантлаб/10) × совпадение с настроением (жанр с весом
// 0.5 — вдвое ниже) + бонус за любимые жанры пользователя (до
// moodGenreBonusCap). Тронутое пользователем — нет; не больше moodPerAuthor
// работ автора. Пустой профиль — просто известное под настроение.
// BytesPerMin — скорость для «≈ N минут» у «короткого» (0 — DefaultBytesPerMin).
type ForMood struct {
	Pool        *MoodPool
	Mood        Mood
	BytesPerMin float64
}

func (ForMood) Name() string         { return "mood" }
func (ForMood) Section() SectionKind { return SectionMood }

func (r ForMood) Candidates(_ context.Context, _ Querier, p *Profile, n int) ([]Candidate, error) {
	if r.Pool == nil || r.Mood == "" || n <= 0 {
		return nil, nil
	}
	spec, ok := MoodSpecOf(r.Mood)
	if !ok {
		return nil, nil
	}
	items, _ := r.Pool.Snapshot(r.Mood)
	out := make([]Candidate, 0, min(len(items), 4*n))
	for _, it := range items {
		if p.Engaged(it.WorkID) {
			continue
		}
		bonus := 0.0
		best, bestW, bestAff := "", 0.0, 0.0
		for _, g := range it.Genres {
			aff := 0.0
			if p != nil {
				aff = p.Genres[g]
			}
			if aff > 0 {
				bonus += math.Min(aff, tonightGenrePerCap) / tonightGenrePerCap
			}
			if w := spec.Genres[g]; w > bestW || (w == bestW && w > 0 && (aff > bestAff || (aff == bestAff && g < best))) {
				best, bestW, bestAff = g, w, aff
			}
		}
		score := (float64(it.Editions)+it.Fantlab/10)*it.Match + math.Min(bonus, moodGenreBonusCap)
		reason := Reason{Kind: ReasonMood, Mood: r.Mood, Genre: best}
		if spec.MaxBytes > 0 && it.Bytes > 0 {
			reason.Minutes = FitsTonight{BytesPerMin: r.BytesPerMin}.MinutesFor(it.Bytes)
		}
		out = append(out, Candidate{WorkID: it.WorkID, Score: score, Reason: reason})
	}
	sortCandidates(out)
	// Разнообразие: не больше moodPerAuthor работ одного автора.
	authorOf := make(map[int64]int64, len(items))
	for _, it := range items {
		authorOf[it.WorkID] = it.AuthorID
	}
	perAuthor := map[int64]int{}
	kept := out[:0]
	for _, c := range out {
		a := authorOf[c.WorkID]
		if a != 0 && perAuthor[a] >= moodPerAuthor {
			continue
		}
		perAuthor[a]++
		kept = append(kept, c)
		if len(kept) == n {
			break
		}
	}
	return kept, nil
}
