package api

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/catalog"
	"github.com/skriptes/skriptes/backend/internal/metadata"
	"github.com/skriptes/skriptes/backend/internal/settings"
)

const (
	maxLazyYearBooks       = 30 // кап книг на один просмотр карточки
	lazyYearLocalParallel  = 4  // параллельность локального fb2-скана
	lazyYearSettingsBudget = 5 * time.Second
)

// yearLazyWanted — чистое решение, какие ленивые источники года включены админом.
// local гейтится мастером обработки коллекции + под-тумблером года; external —
// opt-in внешним воркером. (Тестируемо отдельно, как bookEnrichTargets.)
func yearLazyWanted(cover settings.CoverConfig, year settings.YearEnrichmentConfig) (local, external bool) {
	local = cover.Prewarm && cover.SyncYears
	external = year.Enabled
	return local, external
}

// triggerSeriesYearEnrichmentAsync — ленивое дозаполнение written_year для книг
// карточки серии/автора, у которых его ещё нет (иначе каскад series_order висит
// на фолбэке). Приоритет НА КНИГУ: локальный fb2 (written из <title-info><date>,
// затем edition из <publish-info><year> — COALESCE внутри EnsureYearLocal) →
// внешний (OpenLibrary→Wikidata) только если локально год не дал. Внешнего
// edition_year нет (OL/WD дают только написание) — шаг отсутствует.
//
// Возвращает true (pending), если для ≥1 книги год отсутствует и хотя бы один
// источник включён — фронт поллит карточку, пока true (с капом попыток).
func triggerSeriesYearEnrichmentAsync(meta MetadataDeps, refs []catalog.BookYearRef, items []books.ListItem) bool {
	if meta.Service == nil || meta.Settings == nil || len(refs) == 0 {
		return false
	}
	sctx, cancel := context.WithTimeout(context.Background(), lazyYearSettingsBudget)
	cover, _ := meta.Settings.Cover(sctx)
	year, _ := meta.Settings.YearEnrichment(sctx)
	cancel()
	localOn, externalOn := yearLazyWanted(cover, year)
	if meta.YearBackfill == nil {
		externalOn = false
	}
	if !localOn && !externalOn {
		return false
	}

	byID := make(map[int64]books.ListItem, len(items))
	for _, it := range items {
		byID[it.ID] = it
	}
	cands := make([]catalog.BookYearRef, 0, maxLazyYearBooks)
	for _, r := range refs {
		if r.HasWrittenYear {
			continue
		}
		cands = append(cands, r)
		if len(cands) >= maxLazyYearBooks {
			break
		}
	}
	if len(cands) == 0 {
		return false
	}
	go runLazyYearEnrich(meta, cands, byID, localOn, externalOn)
	return true
}

func runLazyYearEnrich(meta MetadataDeps, cands []catalog.BookYearRef, byID map[int64]books.ListItem, localOn, externalOn bool) {
	ctx, cancel := context.WithTimeout(context.Background(), metadata.EnrichDeadline)
	defer cancel()

	stillMissing := cands

	// Фаза 1: локальный fb2 (если включён). Книги, у которых локальный скан уже
	// был (LocalScanned) и года нет, сразу идут во внешние. Bounded concurrency.
	if localOn {
		var (
			mu   sync.Mutex
			rest []catalog.BookYearRef
			wg   sync.WaitGroup
		)
		sem := make(chan struct{}, lazyYearLocalParallel)
		for _, r := range cands {
			it, ok := byID[r.BookID]
			if !ok {
				continue
			}
			if r.LocalScanned {
				rest = append(rest, r)
				continue
			}
			wg.Add(1)
			sem <- struct{}{}
			go func(r catalog.BookYearRef, it books.ListItem) {
				defer wg.Done()
				defer func() { <-sem }()
				q := metadata.BookQuery{
					ID: r.BookID, Title: it.Title, Authors: it.Authors, Lang: it.Lang,
					ArchivePath: filepath.Join(meta.BooksRoot, r.Archive),
					FB2Name:     r.FileName + "." + r.Ext,
				}
				if !meta.Service.EnsureYearLocal(ctx, q) {
					mu.Lock()
					rest = append(rest, r)
					mu.Unlock()
				}
			}(r, it)
		}
		wg.Wait()
		stillMissing = rest
	}

	// Фаза 2: внешние (если включены) — один воркер с общим rate-gate.
	if externalOn && meta.YearBackfill != nil && len(stillMissing) > 0 {
		lb := make([]metadata.LazyBook, 0, len(stillMissing))
		for _, r := range stillMissing {
			it := byID[r.BookID]
			lb = append(lb, metadata.LazyBook{ID: r.BookID, Title: it.Title, Lang: it.Lang, Authors: it.Authors})
		}
		meta.YearBackfill.EnrichBooksNow(ctx, lb)
	}
}
