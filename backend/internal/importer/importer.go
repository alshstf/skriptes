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
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meilisearch/meilisearch-go"
	"github.com/skriptes/skriptes/backend/internal/inpx"
)

// normalizeLang приводит код языка к канонике: нижний регистр + trim + срез
// регионального/скриптового субтега (ru-RU, en_US, zh-Hans → ru/en/zh). В INPX/fb2
// один язык встречается в разном регистре и с локалями ('ru'/'RU'/'ru-RU'), и без
// нормализации он двоится в списке языков, фильтре и настройках видимости (для
// каталога важен язык, а не локаль). Пустой → пустой.
func normalizeLang(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.IndexAny(s, "-_"); i >= 0 {
		s = s[:i]
	}
	return s
}

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
	if err := configureWorksIndex(ctx, im.deps.Meili); err != nil {
		return stats, fmt.Errorf("configure works meili: %w", err)
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
	if err := markCollectionImported(ctx, im.deps.Pool, collectionID, hash, ix.Version); err != nil {
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

	// Индекс works (фасеты по работам) перестраиваем после импорта: новые
	// singleton-работы + актуальный год/агрегаты. Upsert-only — осиротевшие
	// доки чистят таргетные удаления в точках GC (группировка/split/merge).
	if n, rerr := im.ResyncWorksIndex(ctx); rerr != nil {
		logger.Warn("import: resync works index failed", "err", rerr)
	} else if n > 0 {
		logger.Info("import: works index resynced", "count", n)
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

// ResyncLangs пере-проставляет Meili-поле lang из books.lang для всех живых
// книг (partial update по primary key id). Нужен разово после нормализации кодов
// языка (миграция 0015 чистит PG, а Meili-индекс — этот метод). Зеркало
// ResyncYears. Пустой/NULL lang → lang:null (чистит поле). Возвращает число
// обновлённых документов.
func (im *Importer) ResyncLangs(ctx context.Context) (int, error) {
	rows, err := im.deps.Pool.Query(ctx, `SELECT id, lang FROM books WHERE deleted = false`)
	if err != nil {
		return 0, fmt.Errorf("query lang: %w", err)
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
			return fmt.Errorf("meili update langs: %w", ferr)
		}
		final, ferr := im.deps.Meili.WaitForTaskWithContext(ctx, task.TaskUID, 0)
		if ferr != nil {
			return fmt.Errorf("wait lang task %d: %w", task.TaskUID, ferr)
		}
		if final.Status != meilisearch.TaskStatusSucceeded {
			return fmt.Errorf("lang task %d status %s: %v", final.UID, final.Status, final.Error)
		}
		total += len(batch)
		batch = batch[:0]
		return nil
	}

	for rows.Next() {
		var id int64
		var lang *string // NULL → nil
		if serr := rows.Scan(&id, &lang); serr != nil {
			return total, fmt.Errorf("scan lang row: %w", serr)
		}
		var lv any // nil → lang:null (partial update чистит поле)
		if lang != nil {
			if n := normalizeLang(*lang); n != "" {
				lv = n
			}
		}
		batch = append(batch, map[string]any{"id": id, "lang": lv})
		if len(batch) >= batchSize {
			if ferr := flush(); ferr != nil {
				return total, ferr
			}
		}
	}
	if rerr := rows.Err(); rerr != nil {
		return total, fmt.Errorf("iterate lang rows: %w", rerr)
	}
	if ferr := flush(); ferr != nil {
		return total, ferr
	}
	return total, nil
}

// ResyncWorkIDs пере-проставляет Meili-поле work_id из books.work_id для всех
// живых книг. Нужен: разово при включении distinctAttribute (существующие
// доки его не имели) и после прохода группировки (merge меняет work_id у
// изданий). Зеркало ResyncLangs. Возвращает число обновлённых документов.
func (im *Importer) ResyncWorkIDs(ctx context.Context) (int, error) {
	rows, err := im.deps.Pool.Query(ctx, `SELECT id, COALESCE(work_id, 0) FROM books WHERE deleted = false`)
	if err != nil {
		return 0, fmt.Errorf("query work_id: %w", err)
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
			return fmt.Errorf("meili update work_id: %w", ferr)
		}
		final, ferr := im.deps.Meili.WaitForTaskWithContext(ctx, task.TaskUID, 0)
		if ferr != nil {
			return fmt.Errorf("wait work_id task %d: %w", task.TaskUID, ferr)
		}
		if final.Status != meilisearch.TaskStatusSucceeded {
			return fmt.Errorf("work_id task %d status %s: %v", final.UID, final.Status, final.Error)
		}
		total += len(batch)
		batch = batch[:0]
		return nil
	}

	for rows.Next() {
		var id, workID int64
		if serr := rows.Scan(&id, &workID); serr != nil {
			return total, fmt.Errorf("scan work_id row: %w", serr)
		}
		batch = append(batch, map[string]any{"id": id, "work_id": workID})
		if len(batch) >= batchSize {
			if ferr := flush(); ferr != nil {
				return total, ferr
			}
		}
	}
	if rerr := rows.Err(); rerr != nil {
		return total, fmt.Errorf("iterate work_id rows: %w", rerr)
	}
	if ferr := flush(); ferr != nil {
		return total, ferr
	}
	return total, nil
}

// ── works-индекс: полный ресинк + таргетные upsert/delete ───────

// WorksIndexSchemaVersion — версия схемы документа works-индекса (workDoc +
// workDocSelect). ПРАВИЛО: меняешь состав или семантику вычисляемых полей —
// инкрементируй. Ключ one-shot гейта ресинка в main.go строится от этой
// константы (works_index_synced_v<N>), поэтому бамп ФОРСИТ полный
// ResyncWorksIndex на ближайшем старте. Без этого новое поле тихо остаётся
// нулевым на стабильном деплое: так popularity (#160) был мёртв всю 1.5.x —
// гейт v1 стоял с #91, полный ресинк не запускался.
// v1 — базовый набор (#91); v2 — popularity (#160) + src_lang (#165);
// v3 — popularity = интегральная «известность» (computeWorkPopularity);
// v4 — внешние счётчики известности (fantlab_marks / ol_*) в формуле;
// v5 — wd_sitelinks (Wikidata) в формуле;
// v6 — kind (тип работы: сборник/антология/том собрания; миграция 0034);
// v7 — orig_lang (эффективный язык оригинала = src_lang ?? lang; фасет фильтра);
// v8 — orig_lang стал WORK-LEVEL: union непустых src_lang изданий, фолбэк —
//
//	union языков изданий (перевод-сирота без src_lang больше не «натив»).
const WorksIndexSchemaVersion = 8

// WorksIndexSyncedFlagKey — ключ one-shot гейта полного ресинка works-индекса
// в app_settings, версионированный схемой дока.
func WorksIndexSyncedFlagKey() string {
	return fmt.Sprintf("works_index_synced_v%d", WorksIndexSchemaVersion)
}

// workDocSelect — общий список колонок для построения workDoc из PG. Агрегаты
// (авторы/жанры/языки/популярность) считаются по ЖИВЫМ изданиям работы
// подзапросами (а не GROUP BY с JOIN'ами) — без декартова взрыва строк.
// year = COALESCE(works.written_year, минимальный written_year изданий): даже
// если work-агрегат года ещё не пересчитан группировкой, индекс берёт год из
// изданий (паритет с карточкой books.Get).
const workDocSelect = `
	SELECT
		w.id, w.title, w.normalized_title::text,
		w.series_id, COALESCE(s.title, ''),
		COALESCE(w.edition_count, 1),
		COALESCE(w.written_year, (
			SELECT min(b.written_year) FROM books b WHERE b.work_id = w.id AND b.deleted = false
		)),
		COALESCE((
			SELECT array_agg(DISTINCT lower(btrim(b.lang)))
			FROM books b
			WHERE b.work_id = w.id AND b.deleted = false
			  AND b.lang IS NOT NULL AND btrim(b.lang) <> ''
		), '{}'),
		COALESCE((
			SELECT array_agg(DISTINCT lower(btrim(b.src_lang)))
			FROM books b
			WHERE b.work_id = w.id AND b.deleted = false
			  AND b.src_lang IS NOT NULL AND btrim(b.src_lang) <> ''
		), '{}'),
		COALESCE((
			-- orig_lang = ЭФФЕКТИВНЫЙ оригинал РАБОТЫ (v8, work-level). У изданий
			-- одной работы оригинал ОДИН, поэтому: есть хоть один непустой
			-- src_lang → оригинал(ы) работы = union src_lang; издания без src_lang
			-- при этом НЕ нативы (перевод-сирота на испанский без fb2 <src-lang>
			-- не делает Остин «оригинал: испанский»). src_lang нет ни у кого →
			-- работа нативна: union языков изданий. array_agg по нулю строк даёт
			-- NULL → COALESCE проваливается на lang-фолбэк.
			SELECT array_agg(DISTINCT lower(btrim(b.src_lang)))
			FROM books b
			WHERE b.work_id = w.id AND b.deleted = false
			  AND b.src_lang IS NOT NULL AND btrim(b.src_lang) <> ''
		), (
			SELECT array_agg(DISTINCT lower(btrim(b.lang)))
			FROM books b
			WHERE b.work_id = w.id AND b.deleted = false
			  AND b.lang IS NOT NULL AND btrim(b.lang) <> ''
		), '{}'),
		COALESCE((
			SELECT array_agg(DISTINCT g.fb2_code)
			FROM books b
			JOIN book_genres bg ON bg.book_id = b.id
			JOIN genres g       ON g.id = bg.genre_id
			WHERE b.work_id = w.id AND b.deleted = false AND g.fb2_code IS NOT NULL
		), '{}'),
		COALESCE((
			SELECT array_agg(x.full_name ORDER BY x.minpos, x.last_name)
			FROM (
				SELECT a.id, a.last_name,
				       TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name)) AS full_name,
				       min(ba.position) AS minpos
				FROM book_authors ba
				JOIN authors a ON a.id = ba.author_id
				JOIN books b   ON b.id = ba.book_id
				WHERE b.work_id = w.id AND b.deleted = false
				GROUP BY a.id, a.last_name, a.first_name, a.middle_name
			) x
		), '{}'),
		COALESCE((
			SELECT array_agg(x.id ORDER BY x.minpos, x.last_name)
			FROM (
				SELECT a.id, a.last_name, min(ba.position) AS minpos
				FROM book_authors ba
				JOIN authors a ON a.id = ba.author_id
				JOIN books b   ON b.id = ba.book_id
				WHERE b.work_id = w.id AND b.deleted = false
				GROUP BY a.id, a.last_name
			) x
		), '{}'),
		COALESCE((
			SELECT count(*) FROM views v
			JOIN books b ON b.id = v.book_id
			WHERE b.work_id = w.id AND b.deleted = false
		), 0),
		COALESCE((
			SELECT count(*) FROM reads r
			JOIN books b ON b.id = r.book_id
			WHERE b.work_id = w.id AND b.deleted = false
		), 0),
		COALESCE((
			SELECT max(b.rating) FROM books b
			WHERE b.work_id = w.id AND b.deleted = false
		), 0),
		COALESCE((
			SELECT max(b.external_rating_count) FROM books b
			WHERE b.work_id = w.id AND b.deleted = false
		), 0),
		EXISTS(
			SELECT 1 FROM book_adaptations a
			JOIN books b ON b.id = a.book_id
			WHERE b.work_id = w.id AND b.deleted = false
		),
		COALESCE((
			SELECT count(*) FROM book_ratings br WHERE br.work_id = w.id
		), 0),
		COALESCE(w.fantlab_marks, 0),
		COALESCE(w.ol_ratings_count, 0),
		COALESCE(w.ol_want_count, 0),
		COALESCE(w.wd_sitelinks, 0),
		COALESCE(w.kind, '')
	FROM works w
	LEFT JOIN series s ON s.id = w.series_id`

// scanWorkDocs выполняет workDocSelect + переданный хвост (WHERE/ORDER/LIMIT) и
// собирает документы. Возвращаются только работы с ≥1 живым изданием
// (EXISTS-фильтр в хвосте у вызывающих).
func (im *Importer) scanWorkDocs(ctx context.Context, tail string, args ...any) ([]workDoc, error) {
	rows, err := im.deps.Pool.Query(ctx, workDocSelect+tail, args...)
	if err != nil {
		return nil, fmt.Errorf("query work docs: %w", err)
	}
	defer rows.Close()
	var out []workDoc
	for rows.Next() {
		var (
			d        workDoc
			seriesID *int64
			series   string
			year     *int16
			sig      workPopSignals
		)
		if err := rows.Scan(&d.ID, &d.Title, &d.NormalizedTitle,
			&seriesID, &series, &d.EditionCount, &year,
			&d.Langs, &d.SrcLangs, &d.OrigLangs, &d.Genres, &d.Authors, &d.AuthorIDs,
			&sig.Views, &sig.Reads, &sig.LibrateMax, &sig.ExtVotes,
			&sig.HasAdaptation, &sig.UserRatings,
			&sig.FantlabMarks, &sig.OLRatings, &sig.OLWant, &sig.WDSitelinks,
			&d.Kind); err != nil {
			return nil, fmt.Errorf("scan work doc: %w", err)
		}
		// Popularity работы = интегральная «известность»: workDocSelect отдаёт сырые
		// сигналы, формула — computeWorkPopularity (popularity.go). Свежесть между
		// полными ресинками держит PopularityTracker (таргетный upsert работы при
		// просмотре/чтении — flush идёт через этот же скан).
		sig.EditionCount = int64(d.EditionCount)
		d.Popularity = computeWorkPopularity(sig)
		if seriesID != nil && series != "" {
			d.Series = series
			d.SeriesID = seriesID
		}
		if year != nil {
			v := int(*year)
			d.Year = &v
		}
		// nil-слайсы → пустые, чтобы Meili получал [] а не null.
		if d.Langs == nil {
			d.Langs = []string{}
		}
		if d.SrcLangs == nil {
			d.SrcLangs = []string{}
		}
		if d.OrigLangs == nil {
			d.OrigLangs = []string{}
		}
		if d.Genres == nil {
			d.Genres = []string{}
		}
		if d.Authors == nil {
			d.Authors = []string{}
		}
		if d.AuthorIDs == nil {
			d.AuthorIDs = []int64{}
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// addWorkDocs upsert-ит документы в индекс works и дожидается задачи.
func (im *Importer) addWorkDocs(ctx context.Context, docs []workDoc) error {
	if len(docs) == 0 {
		return nil
	}
	idx := im.deps.Meili.Index(worksIndex)
	pk := "id"
	task, err := idx.AddDocumentsWithContext(ctx, docs, &meilisearch.DocumentOptions{PrimaryKey: &pk})
	if err != nil {
		return fmt.Errorf("meili add work docs: %w", err)
	}
	final, err := im.deps.Meili.WaitForTaskWithContext(ctx, task.TaskUID, 0)
	if err != nil {
		return fmt.Errorf("wait works task %d: %w", task.TaskUID, err)
	}
	if final.Status != meilisearch.TaskStatusSucceeded {
		return fmt.Errorf("works task %d status %s: %v", final.UID, final.Status, final.Error)
	}
	return nil
}

// ResyncWorksIndex полностью перестраивает индекс works из PG: upsert всех работ
// с ≥1 живым изданием, батчами по возрастанию id. Upsert-only (не удаляет
// осиротевшие доки — это делают таргетные DeleteWorksFromIndex в точках GC).
// Зовётся на старте (one-shot) и в конце импорта. Возвращает число доков.
func (im *Importer) ResyncWorksIndex(ctx context.Context) (int, error) {
	const batchSize = 500
	var cursor int64
	total := 0
	for {
		docs, err := im.scanWorkDocs(ctx,
			` WHERE w.id > $1 AND EXISTS (SELECT 1 FROM books b WHERE b.work_id = w.id AND b.deleted = false)
			  ORDER BY w.id LIMIT $2`, cursor, batchSize)
		if err != nil {
			return total, err
		}
		if len(docs) == 0 {
			break
		}
		if err := im.addWorkDocs(ctx, docs); err != nil {
			return total, err
		}
		total += len(docs)
		cursor = docs[len(docs)-1].ID
	}
	return total, nil
}

// UpsertWorksToIndex таргетно пере-собирает и upsert-ит документы заданных работ
// (после прохода группировки / дозаполнения года). Работы из ids, у которых не
// осталось живых изданий, удаляются из индекса. Дешевле полного ResyncWorksIndex.
func (im *Importer) UpsertWorksToIndex(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	docs, err := im.scanWorkDocs(ctx,
		` WHERE w.id = ANY($1) AND EXISTS (SELECT 1 FROM books b WHERE b.work_id = w.id AND b.deleted = false)`, ids)
	if err != nil {
		return err
	}
	if err := im.addWorkDocs(ctx, docs); err != nil {
		return err
	}
	// Работы из ids без живых изданий (не вернулись) — убрать из индекса.
	got := make(map[int64]struct{}, len(docs))
	for _, d := range docs {
		got[d.ID] = struct{}{}
	}
	var missing []int64
	for _, id := range ids {
		if _, ok := got[id]; !ok {
			missing = append(missing, id)
		}
	}
	return im.DeleteWorksFromIndex(ctx, missing)
}

// DeleteWorksFromIndex удаляет документы работ из индекса works (после GC работ
// при группировке / split / merge). Дожидается задачи.
func (im *Importer) DeleteWorksFromIndex(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	strIDs := make([]string, len(ids))
	for i, id := range ids {
		strIDs[i] = strconv.FormatInt(id, 10)
	}
	idx := im.deps.Meili.Index(worksIndex)
	task, err := idx.DeleteDocumentsWithContext(ctx, strIDs, nil)
	if err != nil {
		return fmt.Errorf("meili delete work docs: %w", err)
	}
	final, err := im.deps.Meili.WaitForTaskWithContext(ctx, task.TaskUID, 0)
	if err != nil {
		return fmt.Errorf("wait works delete task %d: %w", task.TaskUID, err)
	}
	if final.Status != meilisearch.TaskStatusSucceeded {
		return fmt.Errorf("works delete task %d status %s: %v", final.UID, final.Status, final.Error)
	}
	return nil
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

	// Язык нормализуем к канонике (lower+trim): источники дают 'ru'/'RU'/' ru'
	// вперемешку, иначе один язык двоится в списке/фильтре/видимости контента.
	lang := normalizeLang(rec.Lang)

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
		lang:            lang,
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

	// Новая книга → своя singleton-работа (инвариант work_id != NULL). Существующая
	// (re-import/update) уже привязана к работе — её work_id мог быть назначен
	// джобой группировки в общую работу, поэтому только читаем.
	var workID int64
	if res.Created {
		var primaryAuthor int64
		if len(authorIDs) > 0 {
			primaryAuthor = authorIDs[0]
		}
		workID, err = ensureSingletonWork(ctx, q, res.ID, br, primaryAuthor)
		if err != nil {
			return err
		}
	} else {
		if err := q.QueryRow(ctx, `SELECT COALESCE(work_id, 0) FROM books WHERE id = $1`, res.ID).Scan(&workID); err != nil {
			return fmt.Errorf("read work_id: %w", err)
		}
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
			Lang:            lang,
			LibID:           rec.LibID,
			Archive:         file.Archive,
			WorkID:          workID,
		}
		if err := idx.add(doc); err != nil {
			return err
		}
	}
	return nil
}
