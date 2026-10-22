package recsys

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
)

// Настроение сессии (план recsys/03, задача 3.4). До фазы 7 (атрибуты
// работ) настроение переводится в веса fb2-кодов жанров по курируемой таблице
// mood-genres.json — владелец правит её руками, как genres/aliases.json:
//
//	{"funny": {"genres": {"humor_prose": 1, "det_irony": 1, "vaudeville": 0.5}},
//	 "short": {"max_bytes": 300000, "genres": {"short_story": 1}}}
//
// Вес 1 — жанр отвечает настроению, 0.5 — отчасти; max_bytes — потолок размера
// самого маленького живого издания («короткое» — ещё и по размеру, как «На
// вечер»). Коды — из словаря genres/dictionary.json (тест проверяет).
//
// Влияние: у секций правил (Feed.Section) кандидаты пересортировываются —
// подходящие вперёд по убыванию совпадения, остальные в прежнем порядке
// (ApplyMood); «Забытая классика» фильтрует пул сама (MoodAware). Поднятая
// карточка несёт Reason.Mood — фронт дописывает «под настроение «…»».

//go:embed mood-genres.json
var rawMoodGenres []byte

// MoodSpec — как одно настроение переводится в признаки работы.
type MoodSpec struct {
	// Genres — fb2-код → вес совпадения (0…1).
	Genres map[string]float64 `json:"genres"`
	// MaxBytes — издание не больше стольких байт — совпадение 1 (0 — не по размеру).
	MaxBytes int64 `json:"max_bytes"`
}

var moodTable = func() map[Mood]MoodSpec {
	m := map[Mood]MoodSpec{}
	if err := json.Unmarshal(rawMoodGenres, &m); err != nil {
		panic(fmt.Sprintf("recsys: parse mood-genres.json: %v", err))
	}
	for _, mood := range AllMoods {
		if _, ok := m[mood]; !ok {
			panic(fmt.Sprintf("recsys: mood-genres.json: нет настроения %q", mood))
		}
	}
	for mood := range m {
		if !IsMood(string(mood)) {
			panic(fmt.Sprintf("recsys: mood-genres.json: неизвестное настроение %q", mood))
		}
	}
	return m
}()

// MoodSpecOf — таблица настроения m (ok=false — неизвестное).
func MoodSpecOf(m Mood) (MoodSpec, bool) {
	spec, ok := moodTable[m]
	return spec, ok
}

// MoodMatch — насколько работа с жанрами genres и самым маленьким живым
// изданием bytes (0 — неизвестно) отвечает настроению m: 0 — никак, 1 —
// точно. Максимум весов совпавших жанров; при MaxBytes — 1, если издание
// укладывается. Пустое или неизвестное настроение — 0.
func MoodMatch(m Mood, genres []string, bytes int64) float64 {
	spec, ok := moodTable[m]
	if !ok {
		return 0
	}
	best := 0.0
	for _, g := range genres {
		if w := spec.Genres[g]; w > best {
			best = w
		}
	}
	if spec.MaxBytes > 0 && bytes > 0 && bytes <= spec.MaxBytes {
		best = 1
	}
	return best
}

// MoodAware — правило, которое учитывает настроение само (фильтром, а не
// пересортировкой ApplyMood): WithMood — копия правила под настроение.
type MoodAware interface {
	Rule
	WithMood(m Mood) Rule
}

// moodFeatures — что нужно от работы для MoodMatch.
type moodFeatures struct {
	genres []string
	bytes  int64
}

// loadMoodFeatures — жанры живых изданий работ и размер самого маленького.
func loadMoodFeatures(ctx context.Context, q Querier, ids []int64) (map[int64]moodFeatures, error) {
	out := make(map[int64]moodFeatures, len(ids))
	err := each(ctx, q, `
		SELECT b.work_id,
		       COALESCE(min(b.size_bytes) FILTER (WHERE b.size_bytes > 0), 0),
		       COALESCE(array_agg(DISTINCT g.fb2_code) FILTER (WHERE g.fb2_code IS NOT NULL), ARRAY[]::text[])
		FROM books b
		LEFT JOIN book_genres bg ON bg.book_id = b.id
		LEFT JOIN genres g ON g.id = bg.genre_id
		WHERE b.work_id = ANY($1) AND NOT b.deleted
		GROUP BY b.work_id`,
		[]any{ids}, func(rows pgx.Rows) error {
			var (
				id int64
				f  moodFeatures
			)
			if err := rows.Scan(&id, &f.bytes, &f.genres); err != nil {
				return err
			}
			out[id] = f
			return nil
		})
	if err != nil {
		return nil, fmt.Errorf("mood features: %w", err)
	}
	return out, nil
}

// ApplyMood — пересортировать кандидатов под настроение m: подходящие
// (MoodMatch > 0) — вперёд по убыванию совпадения, при равенстве — прежний
// порядок; у поднятых Reason.Mood = m. Пустое настроение — как есть.
func ApplyMood(ctx context.Context, q Querier, cands []Candidate, m Mood) ([]Candidate, error) {
	if m == "" || len(cands) == 0 {
		return cands, nil
	}
	ids := make([]int64, 0, len(cands))
	for _, c := range cands {
		ids = append(ids, c.WorkID)
	}
	feats, err := loadMoodFeatures(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	type scored struct {
		c     Candidate
		match float64
	}
	sc := make([]scored, 0, len(cands))
	for _, c := range cands {
		f := feats[c.WorkID]
		match := MoodMatch(m, f.genres, f.bytes)
		if match > 0 {
			c.Reason.Mood = m
		}
		sc = append(sc, scored{c: c, match: match})
	}
	sort.SliceStable(sc, func(i, j int) bool { return sc[i].match > sc[j].match })
	out := make([]Candidate, 0, len(sc))
	for _, s := range sc {
		out = append(out, s.c)
	}
	return out, nil
}
