// Package importer импортирует INPX-каталог в PostgreSQL и индексирует в Meilisearch.
//
// Архитектура (синхронная, для PR 4):
//
//   - Importer.Run открывает INPX, проверяет хэш против previous import,
//     обходит все записи через inpx.Inpx.Each(), для каждой делает upsert
//     в PostgreSQL и батчит документы в Meilisearch.
//   - In-memory кэши (authorCache, seriesCache, genreCache, archiveCache)
//     избавляют от повторных round-trip-ов в БД для часто встречающихся
//     значений в пределах одного импорта.
//   - Идемпотентность: UNIQUE (collection_id, archive_id, lib_id) на books
//     гарантирует, что повторный импорт того же INPX даёт ту же таблицу.
//
// Что не сделано (намеренно, для PR 5):
//   - Нет background queue (river) — импорт запускается синхронно из main.
//   - Нет API/UI триггеров — только startup-time scan.
//   - Нет fsnotify-watcher.
//   - Нет SSE-прогресса.
package importer

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meilisearch/meilisearch-go"
	"github.com/skriptes/skriptes/backend/internal/inpx"
)

// Deps — зависимости импортёра.
type Deps struct {
	Pool   *pgxpool.Pool
	Meili  meilisearch.ServiceManager
	Logger *slog.Logger
}

// Importer — оркестратор импорта одного INPX.
type Importer struct {
	deps Deps
}

// New собирает Importer.
func New(d Deps) *Importer {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return &Importer{deps: d}
}

// Run импортирует один INPX-файл синхронно.
// Возвращает Stats и nil если всё ок (включая случай "пропущено по хэшу").
// Если в записях были ошибки — они увеличивают Stats.Errors, но Run всё
// равно завершается успешно: лучше импортировать частично, чем ничего.
// Только инфраструктурные ошибки (не открыть INPX, не достучаться до БД)
// возвращаются.
func (im *Importer) Run(ctx context.Context, inpxPath string) (Stats, error) {
	start := time.Now()
	logger := im.deps.Logger.With("inpx", filepath.Base(inpxPath))
	stats := Stats{}

	hash, err := hashFile(inpxPath)
	if err != nil {
		return stats, fmt.Errorf("hash inpx: %w", err)
	}

	ix, err := inpx.Open(inpxPath)
	if err != nil {
		return stats, fmt.Errorf("open inpx: %w", err)
	}
	defer func() { _ = ix.Close() }()

	collectionName := ix.Collection.Name
	if collectionName == "" {
		collectionName = filepath.Base(inpxPath)
	}
	collectionID, prevHash, err := upsertCollection(ctx, im.deps.Pool, filepath.Base(inpxPath), collectionName)
	if err != nil {
		return stats, err
	}

	if prevHash == hash {
		stats.Skipped = true
		stats.Duration = time.Since(start)
		logger.Info("import skipped — INPX unchanged", "hash", hash)
		return stats, nil
	}

	if err := configureIndex(ctx, im.deps.Meili); err != nil {
		return stats, fmt.Errorf("configure meili: %w", err)
	}

	caches := newCaches()
	idx := newIndexer(im.deps.Meili, 1000)

	// Прогрев archives внутри одной транзакции? Не нужно: это редкие upsert-ы,
	// делаем отдельно по мере встречи новых имён архивов.
	err = ix.Each(func(file inpx.InpFile, rec inpx.Record) error {
		stats.Records++
		// rec.Deleted (DEL=1) — книга помечена удалённой в источнике. Запись
		// всё равно создаём/обновляем (с deleted=true) чтобы хранить факт
		// существования и не потерять метаданные. В Meili такие документы
		// не индексируются (см. processRecord).
		if rerr := im.processRecord(ctx, collectionID, file, rec, caches, idx, &stats); rerr != nil {
			stats.Errors++
			logger.Warn("record import failed", "lib_id", rec.LibID, "err", rerr)
		}
		return nil
	})
	if err != nil {
		return stats, fmt.Errorf("iterate inpx: %w", err)
	}

	if err := idx.flush(ctx); err != nil {
		return stats, fmt.Errorf("flush meili: %w", err)
	}
	if err := markCollectionImported(ctx, im.deps.Pool, collectionID, hash); err != nil {
		return stats, fmt.Errorf("mark collection imported: %w", err)
	}

	// Год в поиске (Meili year) = written_year (год написания), а он
	// наполняется обогащением ПОСЛЕ импорта. Синкаем из PG: для свежего
	// импорта это no-op (written_year NULL), для повторного — подтягивает уже
	// извлечённые годы. Между импортами синк запускается из админки.
	if n, rerr := im.ResyncYears(ctx); rerr != nil {
		logger.Warn("import: resync years to meili failed", "err", rerr)
	} else if n > 0 {
		logger.Info("import: years resynced to meili", "count", n)
	}

	stats.Authors = len(caches.author)
	stats.Series = len(caches.series)
	stats.Genres = len(caches.genre)
	stats.Duration = time.Since(start)
	logger.Info("import done",
		"records", stats.Records,
		"books_inserted", stats.BooksInserted,
		"books_updated", stats.BooksUpdated,
		"books_deleted", stats.BooksDeleted,
		"books_indexed", stats.BooksIndexed,
		"authors", stats.Authors,
		"series", stats.Series,
		"genres", stats.Genres,
		"errors", stats.Errors,
		"duration", stats.Duration,
	)
	return stats, nil
}

// ResyncYears пере-проставляет Meili-поле year из books.written_year для
// всех живых книг (partial update по primary key id). Источник правды по
// году в поиске — written_year (год написания), но он наполняется
// обогащением уже ПОСЛЕ импорта; этот метод синкает PG→Meili (в конце Run и
// по кнопке в админке «Год издания»). year:null чистит возможный старый
// (date_added) год у книг без written_year. Возвращает число обновлённых
// документов.
func (im *Importer) ResyncYears(ctx context.Context) (int, error) {
	rows, err := im.deps.Pool.Query(ctx, `SELECT id, written_year FROM books WHERE deleted = false`)
	if err != nil {
		return 0, fmt.Errorf("query written_year: %w", err)
	}
	defer rows.Close()

	const batchSize = 1000
	pk := "id"
	batch := make([]map[string]any, 0, batchSize)
	total := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		idx := im.deps.Meili.Index(booksIndex)
		task, ferr := idx.UpdateDocumentsWithContext(ctx, batch, &meilisearch.DocumentOptions{PrimaryKey: &pk})
		if ferr != nil {
			return fmt.Errorf("meili update years: %w", ferr)
		}
		final, ferr := im.deps.Meili.WaitForTaskWithContext(ctx, task.TaskUID, 0)
		if ferr != nil {
			return fmt.Errorf("wait year task %d: %w", task.TaskUID, ferr)
		}
		if final.Status != meilisearch.TaskStatusSucceeded {
			return fmt.Errorf("year task %d status %s: %v", final.UID, final.Status, final.Error)
		}
		total += len(batch)
		batch = batch[:0]
		return nil
	}

	for rows.Next() {
		var id int64
		var wy *int16 // written_year SMALLINT; NULL → nil
		if serr := rows.Scan(&id, &wy); serr != nil {
			return total, fmt.Errorf("scan year row: %w", serr)
		}
		var yv any // nil → year:null (partial update чистит поле)
		if wy != nil {
			yv = int(*wy)
		}
		batch = append(batch, map[string]any{"id": id, "year": yv})
		if len(batch) >= batchSize {
			if ferr := flush(); ferr != nil {
				return total, ferr
			}
		}
	}
	if rerr := rows.Err(); rerr != nil {
		return total, fmt.Errorf("iterate year rows: %w", rerr)
	}
	if ferr := flush(); ferr != nil {
		return total, ferr
	}
	return total, nil
}

// processRecord обрабатывает одну запись внутри транзакции.
// Откат транзакции при любой ошибке — состояние БД не пачкается полу-импортом одной книги.
func (im *Importer) processRecord(
	ctx context.Context, collectionID int64, file inpx.InpFile, rec inpx.Record,
	caches *cacheSet, idx *indexer, stats *Stats,
) error {
	tx, err := im.deps.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := txQuerier{tx}

	archiveID, err := caches.ensureArchive(ctx, q, collectionID, file.Archive)
	if err != nil {
		return err
	}

	authorIDs := make([]int64, 0, len(rec.Authors))
	for _, a := range rec.Authors {
		if normalizedAuthorName(a) == "" {
			continue
		}
		aid, err := caches.ensureAuthor(ctx, q, a)
		if err != nil {
			return err
		}
		authorIDs = append(authorIDs, aid)
	}

	var seriesPtr *int64
	if rec.Series != "" {
		var primaryAuthor int64
		if len(authorIDs) > 0 {
			primaryAuthor = authorIDs[0]
		}
		sid, err := caches.ensureSeries(ctx, q, rec.Series, primaryAuthor)
		if err != nil {
			return err
		}
		seriesPtr = &sid
	}

	genreIDs := make([]int64, 0, len(rec.Genres))
	for _, g := range rec.Genres {
		gid, err := caches.ensureGenre(ctx, q, g)
		if err != nil {
			return err
		}
		genreIDs = append(genreIDs, gid)
	}

	var serNoPtr *int
	if rec.SerNo > 0 {
		v := rec.SerNo
		serNoPtr = &v
	}
	var ratingPtr *int
	if rec.Rating > 0 {
		v := rec.Rating
		ratingPtr = &v
	}

	br := bookRow{
		collectionID:    collectionID,
		archiveID:       archiveID,
		libID:           rec.LibID,
		fileName:        rec.File,
		ext:             rec.Ext,
		size:            rec.Size,
		title:           rec.Title,
		normalizedTitle: normalize(rec.Title),
		seriesID:        seriesPtr,
		serNo:           serNoPtr,
		lang:            rec.Lang,
		dateAdded:       rec.Date,
		rating:          ratingPtr,
		keywords:        rec.Keywords,
		deleted:         rec.Deleted,
	}
	res, err := upsertBook(ctx, q, br)
	if err != nil {
		return err
	}
	if err := replaceBookAuthors(ctx, q, res.ID, authorIDs); err != nil {
		return err
	}
	if err := replaceBookGenres(ctx, q, res.ID, genreIDs); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	stats.Books++
	if res.Created {
		stats.BooksInserted++
	} else {
		stats.BooksUpdated++
	}
	if rec.Deleted {
		stats.BooksDeleted++
	}

	// Удалённые в Meili не индексируем — они не должны всплывать в поиске.
	if !rec.Deleted {
		stats.BooksIndexed++
		authorNames := make([]string, 0, len(rec.Authors))
		for _, a := range rec.Authors {
			authorNames = append(authorNames, fullAuthorName(a))
		}
		// Year НЕ берём из date_added (это дата добавления в коллекцию, не год
		// книги — см. граблю про date_added). Поле year в поиске = written_year
		// (год написания); оно наполняется обогащением ПОСЛЕ импорта и синкается
		// в Meili через ResyncYears (в конце Run и по кнопке в админке).
		doc := bookDoc{
			ID:              res.ID,
			Title:           rec.Title,
			NormalizedTitle: normalize(rec.Title),
			Authors:         authorNames,
			AuthorIDs:       authorIDs,
			Series:          rec.Series,
			SeriesID:        seriesPtr,
			Genres:          rec.Genres,
			Lang:            rec.Lang,
			LibID:           rec.LibID,
			Archive:         file.Archive,
		}
		if err := idx.add(doc); err != nil {
			return err
		}
	}
	return nil
}
