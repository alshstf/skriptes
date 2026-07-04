package importer

import (
	"context"
	"log/slog"
	"math"
	"sync"
	"time"
)

// ── Интегральная «известность» работы ────────────────────────────
//
// Популярность работы в works-индексе = известность книги в мире + вовлечённость
// этого инстанса (план: ~/.claude/plans/popularity-renown-plan.md). Мировые сигналы:
// переиздания/переводы в коллекции (спрос), LIBRATE-рейтинг донора, голоса внешнего
// рейтинга (GB/OL), факт экранизации. Локальные: просмотры/чтения/оценки — живое
// чтение заметно поднимает книгу над безвестным хвостом, но классику не топит.
// Счётчики сжимаются log2 (иначе один популярный хвост забьёт всё остальное).

// Веса слагаемых известности. Подбирались на глаз под диапазон «головы» ~100–1000;
// крутить здесь, пересчёт — бамп WorksIndexSchemaVersion (полный ресинк на старте).
const (
	popWEditions    = 100.0 // ·log2(edition_count), от 2 изданий: 2→100, 10→~332
	popWLibrateBase = 40.0  // наличие LIBRATE-оценки — само по себе сигнал спроса
	popWLibrate     = 24.0  // + 24·rating (1..5) → суммарно 64..160
	popWExtVotes    = 30.0  // ·log2(1+голоса GB/OL): 2→~48, 1000→~300
	popWAdaptation  = 150.0 // факт экранизации
	popWView        = 20.0  // просмотр карточки на инстансе
	popWRead        = 60.0  // отметка «прочитано»/чтение
	popWUserRating  = 100.0 // осознанная оценка 1–5 — самый сильный локальный сигнал
	popEditionCap   = 64    // потолок edition_count в формуле (санитарный)
)

// workPopSignals — сырые сигналы известности одной работы; порядок и состав
// зеркалят хвост workDocSelect.
type workPopSignals struct {
	EditionCount int64
	Views        int64
	Reads        int64
	LibrateMax   int64 // max(books.rating) по живым изданиям; 0 = оценки нет
	ExtVotes     int64 // max(books.external_rating_count); max, не Σ — издания
	// одной работы резолвятся в ту же внешнюю запись, сумма даст двойной счёт
	HasAdaptation bool
	UserRatings   int64 // count(book_ratings) по работе
}

// computeWorkPopularity собирает интегральную известность из сырых сигналов.
// Чистая функция: веса и поведение фиксируются юнит-тестами.
func computeWorkPopularity(s workPopSignals) int64 {
	p := 0.0
	if s.EditionCount >= 2 {
		ec := s.EditionCount
		if ec > popEditionCap {
			ec = popEditionCap
		}
		p += popWEditions * math.Log2(float64(ec))
	}
	if s.LibrateMax > 0 {
		p += popWLibrateBase + popWLibrate*float64(s.LibrateMax)
	}
	if s.ExtVotes > 0 {
		p += popWExtVotes * math.Log2(1+float64(s.ExtVotes))
	}
	if s.HasAdaptation {
		p += popWAdaptation
	}
	p += popWView*float64(s.Views) + popWRead*float64(s.Reads) + popWUserRating*float64(s.UserRatings)
	return int64(math.Round(p))
}

// PopularityTracker накапливает book_id'ы с новой вовлечённостью (просмотр/чтение)
// и периодически таргетно ре-апсертит их РАБОТЫ в works-индекс — популярность
// пересчитывается из сырых сигналов workDocSelect (computeWorkPopularity). Дёшево:
// батчем и только изменившиеся работы (а не upsert на каждое событие). Без трекера
// популярность всё равно верна на момент полного ресинка (импорт/старт).
type PopularityTracker struct {
	im     *Importer
	logger *slog.Logger
	mu     sync.Mutex
	dirty  map[int64]struct{} // book_id'ы с новой вовлечённостью
}

// NewPopularityTracker — трекер на базе импортёра (его UpsertWorksToIndex + пул).
func NewPopularityTracker(im *Importer, logger *slog.Logger) *PopularityTracker {
	if logger == nil {
		logger = slog.Default()
	}
	return &PopularityTracker{im: im, logger: logger, dirty: map[int64]struct{}{}}
}

// MarkBook помечает книгу как получившую новую вовлечённость (зовётся после
// RecordView/RecordRead). Неблокирующе, безопасно из нескольких горутин; nil-трекер
// и пустой id — no-op.
func (t *PopularityTracker) MarkBook(bookID int64) {
	if t == nil || bookID <= 0 {
		return
	}
	t.mu.Lock()
	t.dirty[bookID] = struct{}{}
	t.mu.Unlock()
}

// Run флашит грязные книги в works-индекс раз в interval, пока жив ctx.
func (t *PopularityTracker) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.flush(ctx)
		}
	}
}

func (t *PopularityTracker) flush(ctx context.Context) {
	t.mu.Lock()
	if len(t.dirty) == 0 {
		t.mu.Unlock()
		return
	}
	books := make([]int64, 0, len(t.dirty))
	for id := range t.dirty {
		books = append(books, id)
	}
	t.dirty = map[int64]struct{}{}
	t.mu.Unlock()

	// книги → различимые работы (одной книги достаточно, чтобы пересчитать всю работу).
	rows, err := t.im.deps.Pool.Query(ctx,
		`SELECT DISTINCT work_id FROM books WHERE id = ANY($1) AND work_id IS NOT NULL`, books)
	if err != nil {
		t.logger.Warn("popularity flush: resolve works failed", "err", err)
		return
	}
	var workIDs []int64
	for rows.Next() {
		var w int64
		if err := rows.Scan(&w); err != nil {
			rows.Close()
			t.logger.Warn("popularity flush: scan work_id failed", "err", err)
			return
		}
		workIDs = append(workIDs, w)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.logger.Warn("popularity flush: rows err", "err", err)
		return
	}
	if len(workIDs) == 0 {
		return
	}
	if err := t.im.UpsertWorksToIndex(ctx, workIDs); err != nil {
		t.logger.Warn("popularity flush: upsert works failed", "count", len(workIDs), "err", err)
		return
	}
	// Info, не Debug: на прод-уровне логов это единственное свидетельство, что
	// живая вовлечённость доезжает до индекса. Не чаще раза в тик (30с) и
	// только при изменениях — не зашумит.
	t.logger.Info("popularity flushed to works index", "books", len(books), "works", len(workIDs))
}
