package recsys

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// «Забытая классика» (план recsys/02, задача 2.5): работы с высоким внешним
// рейтингом, написанные ≥ ClassicMinAge лет назад, у авторов из верхней трети
// по известности, которые на инстансе никто не открывал. Выборка по всему
// каталогу — секунды на проде (сотни тысяч работ с коррелированными
// подзапросами), поэтому считается не на запрос, а фоновым RefreshLoop в
// пул ClassicsPool; правило ForgottenClassic читает пул и ранжирует под
// пользователя (его жанры и язык оригинала — выше). Пул пуст до первого
// пересчёта (секция не рендерится).

const (
	// ClassicMinAge — «классика» — написано не позже, чем столько лет назад.
	ClassicMinAge = 30
	// ClassicFantlabMinMark / ClassicFantlabMinVotes — средняя Фантлаба (1–10) и
	// минимум голосов за ней.
	ClassicFantlabMinMark  = 8.0
	ClassicFantlabMinVotes = 100
	// ClassicLibrateMin / ClassicLibrateMinEditions — иначе LIBRATE (1–5) хотя бы
	// у одного издания при стольких изданиях в коллекции.
	ClassicLibrateMin         = 4
	ClassicLibrateMinEditions = 3
	// ClassicRenownQuantile — автор в верхней трети по известности.
	ClassicRenownQuantile = 0.667
	// ClassicPoolMax — потолок пула.
	ClassicPoolMax = 5000
	// ClassicRefreshEvery — период фонового пересчёта.
	ClassicRefreshEvery = 6 * time.Hour
)

// ClassicCandidate — работа пула с тем, что нужно для ранжирования и причины.
type ClassicCandidate struct {
	WorkID   int64
	Year     int
	Rating   float64 // средняя Фантлаба (1–10) или LIBRATE (1–5)
	Source   string  // "fantlab" | "librate"
	Editions int
	Genres   []string
	Lang     string
}

// Base — вес кандидата без пользователя, в шкале ≈ 4…5: Фантлаб — средняя / 2
// (9.4 → 4.7); LIBRATE — целые 1–5 от горстки голосов, поэтому −0.5 и до +0.5
// за число изданий (5 при 20+ изданиях → 5.0, при трёх → 4.58). Та же формула —
// в ORDER BY пересчёта.
func (c ClassicCandidate) Base() float64 {
	if c.Source == "fantlab" {
		return c.Rating / 2
	}
	ed := c.Editions
	if ed > 20 {
		ed = 20
	}
	return c.Rating - 0.5 + float64(ed)/40
}

// ClassicsPool — пул кандидатов «забытой классики»; наполняется Refresh.
type ClassicsPool struct {
	mu    sync.RWMutex
	items []ClassicCandidate
	at    time.Time
}

// DefaultClassics — пул, который читает правило из Rules.
var DefaultClassics = &ClassicsPool{}

// Snapshot — копия пула и время пересчёта (нулевое — ещё не считался).
func (c *ClassicsPool) Snapshot() ([]ClassicCandidate, time.Time) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.items, c.at
}

// Set — подменить пул (тесты и Refresh).
func (c *ClassicsPool) Set(items []ClassicCandidate, at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items, c.at = items, at
}

// Refresh — пересчитать пул по каталогу. «Никто не открывал» — ни просмотра
// карточки, ни строки чтения ни у одного пользователя по любому изданию
// работы. Порядок — лучшие по рейтингу первыми (Фантлаб нормирован к шкале 5).
func (c *ClassicsPool) Refresh(ctx context.Context, q Querier, now time.Time) (int, error) {
	items := make([]ClassicCandidate, 0, 1024)
	err := each(ctx, q, `
		WITH thr AS (
			SELECT percentile_cont($1::float8) WITHIN GROUP (ORDER BY renown) AS r
			FROM authors WHERE NOT is_service AND renown > 0
		),
		base AS MATERIALIZED (
			SELECT w.id, COALESCE(w.written_year, 0)::int AS year,
			       w.fantlab_midmark::float8 AS fl, w.fantlab_marks, w.edition_count
			FROM works w
			JOIN authors a ON a.id = w.primary_author_id, thr
			WHERE NOT w.instance_hidden AND COALESCE(w.kind, '') = ''
			  AND w.written_year IS NOT NULL AND w.written_year <= $2
			  AND a.renown >= thr.r AND NOT a.is_service AND NOT a.instance_hidden
			  AND ((w.fantlab_midmark >= $3 AND w.fantlab_marks >= $4) OR w.edition_count >= $6)
		),
		cand AS (
			SELECT b.id, b.year, b.edition_count,
			       CASE WHEN b.fl >= $3 AND b.fantlab_marks >= $4 THEN b.fl ELSE
			            (SELECT max(bb.rating)::float8 FROM books bb WHERE bb.work_id = b.id) END AS rating,
			       CASE WHEN b.fl >= $3 AND b.fantlab_marks >= $4 THEN 'fantlab' ELSE 'librate' END AS source
			FROM base b
			WHERE EXISTS (SELECT 1 FROM books bb WHERE bb.work_id = b.id AND NOT bb.deleted AND NOT bb.instance_hidden)
			  AND NOT EXISTS (SELECT 1 FROM views v JOIN books vb ON vb.id = v.book_id WHERE vb.work_id = b.id)
			  AND NOT EXISTS (SELECT 1 FROM reads r JOIN books rb ON rb.id = r.book_id WHERE rb.work_id = b.id)
		)
		SELECT c.id, c.year, c.rating, c.source, c.edition_count,
		       COALESCE((SELECT array_agg(DISTINCT g.fb2_code) FROM books bb
		                 JOIN book_genres bg ON bg.book_id = bb.id JOIN genres g ON g.id = bg.genre_id
		                 WHERE bb.work_id = c.id AND NOT bb.deleted), ARRAY[]::text[]),
		       COALESCE((SELECT bb.src_lang FROM books bb WHERE bb.work_id = c.id AND NOT bb.deleted AND bb.src_lang IS NOT NULL ORDER BY bb.id LIMIT 1), '')
		FROM cand c
		WHERE c.rating IS NOT NULL AND (c.source = 'fantlab' OR c.rating >= $5)
		ORDER BY CASE WHEN c.source = 'fantlab' THEN c.rating / 2
		              ELSE c.rating - 0.5 + LEAST(c.edition_count, 20) / 40.0 END DESC, c.id
		LIMIT $7`,
		[]any{ClassicRenownQuantile, now.Year() - ClassicMinAge, ClassicFantlabMinMark, ClassicFantlabMinVotes,
			ClassicLibrateMin, ClassicLibrateMinEditions, ClassicPoolMax},
		func(rows pgx.Rows) error {
			var it ClassicCandidate
			if err := rows.Scan(&it.WorkID, &it.Year, &it.Rating, &it.Source, &it.Editions, &it.Genres, &it.Lang); err != nil {
				return err
			}
			items = append(items, it)
			return nil
		})
	if err != nil {
		return 0, fmt.Errorf("recsys: classics refresh: %w", err)
	}
	c.Set(items, now)
	return len(items), nil
}

// RefreshLoop — пересчёт на старте (с задержкой delay) и раз в ClassicRefreshEvery;
// запускать через metadata.Go (грабля 23). Ошибка — в лог, следующий раз по расписанию.
func (c *ClassicsPool) RefreshLoop(ctx context.Context, q Querier, delay time.Duration, logger *slog.Logger) {
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
		n, err := c.Refresh(rctx, q, time.Now())
		cancel()
		if err != nil {
			if ctx.Err() == nil && logger != nil {
				logger.Warn("forgotten classics: refresh failed", "err", err)
			}
		} else if logger != nil {
			logger.Info("forgotten classics: pool refreshed", "works", n, "took", time.Since(started).Round(time.Millisecond))
		}
		timer.Reset(ClassicRefreshEvery)
	}
}

// ForgottenClassic — «Забытая классика»: пул → под пользователя. Выше — работы
// в жанрах и на языке оригинала, которые пользователь любит (положительная
// близость в Profile.Genres/Langs); уже тронутое им (в том числе скрытое из
// ленты) — нет. Вес: ClassicCandidate.Base (≈ 4…5) + 1 за любимый жанр + 0.5 за язык.
type ForgottenClassic struct {
	Pool *ClassicsPool
}

func (ForgottenClassic) Name() string         { return "forgotten_classic" }
func (ForgottenClassic) Section() SectionKind { return SectionForgottenClassic }

func (r ForgottenClassic) Candidates(_ context.Context, _ Querier, p *Profile, n int) ([]Candidate, error) {
	if r.Pool == nil || n <= 0 {
		return nil, nil
	}
	items, _ := r.Pool.Snapshot()
	out := make([]Candidate, 0, n)
	for _, it := range items {
		if p.Engaged(it.WorkID) {
			continue
		}
		score := it.Base()
		if p != nil {
			for _, g := range it.Genres {
				if p.Genres[g] > 0 {
					score++
					break
				}
			}
			if it.Lang != "" && p.Langs[it.Lang] > 0 {
				score += 0.5
			}
		}
		out = append(out, Candidate{
			WorkID: it.WorkID,
			Score:  score,
			Reason: Reason{Kind: ReasonForgottenClassic, Year: it.Year, Rating: it.Rating, Source: it.Source},
		})
	}
	sortCandidates(out)
	if len(out) > n {
		out = out[:n]
	}
	return out, nil
}
