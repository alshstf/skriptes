package recsys

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Kind — слагаемое близости; ключ трассы Affinity.Parts.
type Kind string

const (
	KindRating      Kind = "rating"       // оценка 1–5
	KindCompleted   Kind = "completed"    // дочитано
	KindReading     Kind = "reading"      // читает сейчас (доля ≥ порога, недавно)
	KindDropped     Kind = "dropped"      // бросил (доля < порога, давно не открывал)
	KindFavorite    Kind = "favorite"     // ★ избранное
	KindShelf       Kind = "shelf"        // на своей полке
	KindAnnotations Kind = "annotations"  // подчёркивания и закладки
	KindAcquired    Kind = "acquired"     // приобретено (OPDS/Kindle), не открыто
	KindViews       Kind = "views"        // просмотры карточки
	KindDismissed   Kind = "dismissed"    // скрыто из ленты
	KindPromptNever Kind = "prompt_never" // «никогда не спрашивать оценку»
)

// MaxAuthors — у работы с бо́льшим числом авторов (антологии, сборники журналов)
// авторские сигналы не считаются: открытая антология не делает «интересными»
// сотни её авторов. Зеркало history.MaxPersonaAuthors (тест affinity_test.go
// держит их равными; history этот пакет не импортирует).
const MaxAuthors = 3

// Weights — все веса модели близости в одном месте. Шкала — гипотеза (план
// recsys/01, задача 1.1): положительные сигналы сравнимы с оценкой «4» (+3),
// явный негатив — с оценкой «2» (−3), слабые сигналы (просмотры, полка,
// приобретение) — в пределах +1…+1.5. Крутить по результатам харнесса (1.5).
type Weights struct {
	// Rating[r] — вклад оценки r (1–5); Rating[0] не используется.
	Rating [6]float64

	Completed float64

	// Читает: Fraction ≥ ReadingMinFraction и последнее движение не старше
	// ReadingMaxAge.
	Reading            float64
	ReadingMinFraction float64
	ReadingMaxAge      time.Duration

	// Бросил: Fraction < DroppedMaxFraction и последнее движение старше
	// DroppedMinAge. Отрицательный.
	Dropped            float64
	DroppedMaxFraction float64
	DroppedMinAge      time.Duration

	Favorite float64
	Shelf    float64

	// Подчёркивания: Annotation за каждое, не больше AnnotationCap.
	Annotation    float64
	AnnotationCap float64

	// Приобретено, но не открыто и не дочитано — намерение.
	Acquired float64

	// Просмотры: ViewsScale × log2(1 + n), не больше ViewsCap.
	ViewsScale float64
	ViewsCap   float64

	// Отрицательные.
	Dismissed   float64
	PromptNever float64

	// Затухание: всё, кроме оценки, умножается на 0.5^(возраст / HalfLife),
	// возраст — от последнего события по работе. 0 — без затухания.
	HalfLife time.Duration
}

// DefaultWeights — веса по плану recsys/01 (таблица 1.1).
var DefaultWeights = Weights{
	Rating:             [6]float64{0, -5, -3, 0, 3, 5},
	Completed:          3,
	Reading:            2,
	ReadingMinFraction: 0.6,
	ReadingMaxAge:      90 * 24 * time.Hour,
	Dropped:            -2,
	DroppedMaxFraction: 0.25,
	DroppedMinAge:      180 * 24 * time.Hour,
	Favorite:           2,
	Shelf:              1,
	Annotation:         0.5,
	AnnotationCap:      2,
	Acquired:           1,
	ViewsScale:         0.5,
	ViewsCap:           1.5,
	Dismissed:          -3,
	PromptNever:        -1,
	HalfLife:           2 * 365 * 24 * time.Hour,
}

// Affinity — близость пользователя к работе и трасса её слагаемых.
// Score = Parts[KindRating] + Decay × Σ остальных Parts (Parts — до затухания).
type Affinity struct {
	WorkID int64
	Score  float64
	Parts  map[Kind]float64
	Decay  float64
	LastAt time.Time
}

// Score — чистая функция: сырые сигналы → близость. now — момент подсчёта
// (возраст сигналов и затухание считаются от него).
func Score(s WorkSignals, now time.Time, w Weights) Affinity {
	a := Affinity{WorkID: s.WorkID, Parts: map[Kind]float64{}, Decay: 1, LastAt: s.LastAt()}

	if s.Rating >= 1 && s.Rating <= 5 {
		a.Parts[KindRating] = w.Rating[s.Rating]
	}
	if s.Completed {
		a.Parts[KindCompleted] = w.Completed
	}
	if !s.Completed && s.Fraction != nil {
		age := now.Sub(s.ReadAt)
		switch f := *s.Fraction; {
		case f >= w.ReadingMinFraction && age <= w.ReadingMaxAge:
			a.Parts[KindReading] = w.Reading
		case f < w.DroppedMaxFraction && age >= w.DroppedMinAge:
			a.Parts[KindDropped] = w.Dropped
		}
	}
	if s.Favorite {
		a.Parts[KindFavorite] = w.Favorite
	}
	if s.OnShelf {
		a.Parts[KindShelf] = w.Shelf
	}
	if s.Annotations > 0 {
		a.Parts[KindAnnotations] = math.Min(float64(s.Annotations)*w.Annotation, w.AnnotationCap)
	}
	if s.Acquired && !s.Opened && !s.Completed {
		a.Parts[KindAcquired] = w.Acquired
	}
	if s.Views > 0 {
		a.Parts[KindViews] = math.Min(w.ViewsScale*math.Log2(1+float64(s.Views)), w.ViewsCap)
	}
	// Негатив — только «не интересно» (и скрытия без причины, до 1.57);
	// «уже читал» и «потом» вкус не отрицают.
	if s.Dismissed && (s.DismissReason == "" || s.DismissReason == DismissNotInterested) {
		a.Parts[KindDismissed] = w.Dismissed
	}
	if s.PromptNever {
		a.Parts[KindPromptNever] = w.PromptNever
	}

	if w.HalfLife > 0 && !a.LastAt.IsZero() {
		if age := now.Sub(a.LastAt); age > 0 {
			a.Decay = math.Pow(0.5, age.Hours()/w.HalfLife.Hours())
		}
	}
	rest := 0.0
	for k, v := range a.Parts {
		if k != KindRating {
			rest += v
		}
	}
	a.Score = a.Parts[KindRating] + a.Decay*rest
	return a
}

// String — трасса одной строкой: «work 42: 6.50 = rating 5 + 0.75×(completed 3 + views 0.5)».
func (a Affinity) String() string {
	kinds := make([]string, 0, len(a.Parts))
	for k := range a.Parts {
		if k != KindRating {
			kinds = append(kinds, string(k))
		}
	}
	sort.Strings(kinds)
	var b strings.Builder
	fmt.Fprintf(&b, "work %d: %.2f =", a.WorkID, a.Score)
	if r, ok := a.Parts[KindRating]; ok {
		fmt.Fprintf(&b, " rating %g", r)
		if len(kinds) > 0 {
			b.WriteString(" +")
		}
	}
	if len(kinds) > 0 {
		fmt.Fprintf(&b, " %.2f×(", a.Decay)
		for i, k := range kinds {
			if i > 0 {
				b.WriteString(" + ")
			}
			fmt.Fprintf(&b, "%s %g", k, a.Parts[Kind(k)])
		}
		b.WriteString(")")
	}
	if len(a.Parts) == 0 {
		b.WriteString(" (нет сигналов)")
	}
	return b.String()
}

// Profile — близость пользователя ко всем работам, с которыми он взаимодействовал,
// и производные профили. Снимок на момент BuiltAt; читать как read-only.
type Profile struct {
	UserID int64
	Works  map[int64]Affinity

	// Суммы Score работ по автору (работы с > MaxAuthors авторами не
	// учитываются), циклу (series.kind IS NULL), fb2-коду жанра, языку
	// оригинала и десятилетию написания. Отрицательные близости вычитаются:
	// брошенный и скрытый автор уходит в минус.
	Authors map[int64]float64
	Series  map[int64]float64
	Genres  map[string]float64
	Langs   map[string]float64
	Decades map[int]float64

	BuiltAt time.Time
}

// IsEmpty — ни одного сигнала.
func (p *Profile) IsEmpty() bool { return p == nil || len(p.Works) == 0 }

// Explain — трасса близости к работе (для логов и отладки).
func (p *Profile) Explain(workID int64) string {
	if p == nil {
		return fmt.Sprintf("work %d: профиль пуст", workID)
	}
	a, ok := p.Works[workID]
	if !ok {
		return fmt.Sprintf("work %d: 0 (нет сигналов)", workID)
	}
	return a.String()
}

// BuildProfile — сигналы из БД → близости → производные профили.
func BuildProfile(ctx context.Context, q Querier, userID int64, now time.Time, w Weights) (*Profile, error) {
	signals, err := LoadSignals(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("recsys: load signals: %w", err)
	}
	p := &Profile{
		UserID:  userID,
		Works:   make(map[int64]Affinity, len(signals)),
		Authors: map[int64]float64{},
		Series:  map[int64]float64{},
		Genres:  map[string]float64{},
		Langs:   map[string]float64{},
		Decades: map[int]float64{},
		BuiltAt: now,
	}
	if len(signals) == 0 {
		return p, nil
	}
	ids := make([]int64, 0, len(signals))
	for id, s := range signals {
		p.Works[id] = Score(*s, now, w)
		ids = append(ids, id)
	}
	meta, err := loadWorkMeta(ctx, q, ids)
	if err != nil {
		return nil, fmt.Errorf("recsys: work meta: %w", err)
	}
	for id, a := range p.Works {
		m := meta[id]
		if len(m.Authors) <= MaxAuthors {
			for _, aid := range m.Authors {
				p.Authors[aid] += a.Score
			}
		}
		for _, sid := range m.Series {
			p.Series[sid] += a.Score
		}
		for _, g := range m.Genres {
			p.Genres[g] += a.Score
		}
		if m.Lang != "" {
			p.Langs[m.Lang] += a.Score
		}
		if m.Year > 0 {
			p.Decades[m.Year/10*10] += a.Score
		}
	}
	return p, nil
}

// workMeta — что нужно от работы для производных профилей.
type workMeta struct {
	Authors []int64
	Series  []int64
	Genres  []string
	Lang    string
	Year    int
}

// loadWorkMeta — авторы, циклы, жанры, язык оригинала и год написания работ.
// Авторы — по правилу workauthors.Core (есть хотя бы в половине живых изданий
// или самые частые), но сразу для набора работ.
func loadWorkMeta(ctx context.Context, q Querier, ids []int64) (map[int64]*workMeta, error) {
	out := make(map[int64]*workMeta, len(ids))
	get := func(id int64) *workMeta {
		m, ok := out[id]
		if !ok {
			m = &workMeta{}
			out[id] = m
		}
		return m
	}

	if err := each(ctx, q, `
		WITH e AS (
			SELECT b.work_id, ba.author_id, count(DISTINCT ba.book_id) AS k
			FROM books b
			JOIN book_authors ba ON ba.book_id = b.id
			WHERE b.work_id = ANY($1) AND b.deleted = false
			GROUP BY b.work_id, ba.author_id
		), n AS (
			SELECT work_id, count(*) AS total FROM books
			WHERE work_id = ANY($1) AND deleted = false GROUP BY work_id
		), m AS (
			SELECT work_id, max(k) AS kmax FROM e GROUP BY work_id
		)
		SELECT e.work_id, e.author_id
		FROM e JOIN m USING (work_id) JOIN n USING (work_id)
		WHERE e.k = m.kmax OR e.k * 2 >= n.total`,
		[]any{ids}, func(rows pgx.Rows) error {
			var wid, aid int64
			if err := rows.Scan(&wid, &aid); err != nil {
				return err
			}
			m := get(wid)
			m.Authors = append(m.Authors, aid)
			return nil
		}); err != nil {
		return nil, fmt.Errorf("authors: %w", err)
	}

	// Циклы: серия работы и серии её живых изданий, только series.kind IS NULL
	// (межавторские и издательские серии — не вкус, #448/#468).
	if err := each(ctx, q, `
		SELECT DISTINCT x.work_id, x.series_id FROM (
			SELECT w.id AS work_id, w.series_id FROM works w WHERE w.id = ANY($1) AND w.series_id IS NOT NULL
			UNION
			SELECT b.work_id, b.series_id FROM books b WHERE b.work_id = ANY($1) AND b.deleted = false AND b.series_id IS NOT NULL
		) x
		JOIN series s ON s.id = x.series_id
		WHERE s.kind IS NULL`,
		[]any{ids}, func(rows pgx.Rows) error {
			var wid, sid int64
			if err := rows.Scan(&wid, &sid); err != nil {
				return err
			}
			m := get(wid)
			m.Series = append(m.Series, sid)
			return nil
		}); err != nil {
		return nil, fmt.Errorf("series: %w", err)
	}

	if err := each(ctx, q, `
		SELECT DISTINCT b.work_id, g.fb2_code
		FROM books b
		JOIN book_genres bg ON bg.book_id = b.id
		JOIN genres g ON g.id = bg.genre_id
		WHERE b.work_id = ANY($1) AND b.deleted = false`,
		[]any{ids}, func(rows pgx.Rows) error {
			var (
				wid  int64
				code string
			)
			if err := rows.Scan(&wid, &code); err != nil {
				return err
			}
			m := get(wid)
			m.Genres = append(m.Genres, code)
			return nil
		}); err != nil {
		return nil, fmt.Errorf("genres: %w", err)
	}

	// Год написания — работы; язык оригинала — первого живого издания, у
	// которого он известен (src_lang уже канонический, langcode.Canonical).
	if err := each(ctx, q, `
		SELECT w.id, COALESCE(w.written_year, 0),
		       COALESCE((SELECT b.src_lang FROM books b
		                 WHERE b.work_id = w.id AND b.deleted = false AND b.src_lang IS NOT NULL
		                 ORDER BY b.id LIMIT 1), '')
		FROM works w WHERE w.id = ANY($1)`,
		[]any{ids}, func(rows pgx.Rows) error {
			var (
				wid  int64
				year int
				lang string
			)
			if err := rows.Scan(&wid, &year, &lang); err != nil {
				return err
			}
			m := get(wid)
			m.Year, m.Lang = year, lang
			return nil
		}); err != nil {
		return nil, fmt.Errorf("year/lang: %w", err)
	}
	return out, nil
}
