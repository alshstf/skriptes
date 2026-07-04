package importer

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// PopularityTracker накапливает book_id'ы с новой вовлечённостью (просмотр/чтение)
// и периодически таргетно ре-апсертит их РАБОТЫ в works-индекс — там популярность
// = Σ изданий (views + 3×reads), пересчитывается из PG в workDocSelect. Дёшево:
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
