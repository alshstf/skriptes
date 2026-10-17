package metadata

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Догон года оригинала перевода (этап 4 добора года написания, #533): новые
// книги получают books.src_year при обработке коллекции (EnsureEditionMeta), а
// уже просканированные переводы — этим разовым проходом. Пробный скан на проде
// (2026-10-17): дата оригинала есть у 9,5 % переводов работ без года, сверка с
// Wikidata — 92 % в пределах ±1; переводов 184 тыс. в 228 архивах.
//
// Идёт по архивам: zip открывается один раз на архив (openFB2 открывает его на
// каждую книгу). Курсор — в app_settings: остановка процесса продолжает с того
// же архива, а пауза между архивами бережёт диск.

const (
	srcYearsStateKey     = "fb2_src_years_v1"
	srcYearsArchivePause = 2 * time.Second
	srcYearsHeaderLimit  = 1 << 20 // заголовок fb2 — до <body>; больше не читаем
)

type srcYearsState struct {
	AfterArchive int64 `json:"after_archive"`
	Done         bool  `json:"done"`
}

// BackfillSrcYears — разовый догон books.src_year у переводов. Изменённые годы
// работ пересчитываются (RecomputeWorkYears) и уходят в works-индекс.
// Возвращает число изданий, получивших год оригинала.
func BackfillSrcYears(ctx context.Context, pool *pgxpool.Pool, booksRoot string, syncer WorksIndexSyncer, logger *slog.Logger) (int, error) {
	st, err := loadSrcYearsState(ctx, pool)
	if err != nil || st.Done {
		return 0, err
	}
	total, archives := 0, 0
	for ctx.Err() == nil {
		var archiveID int64
		var fileName string
		err := pool.QueryRow(ctx, `
			SELECT a.id, a.filename FROM archives a
			WHERE a.id > $1 AND EXISTS (SELECT 1 FROM books b
			      WHERE b.archive_id = a.id AND NOT b.deleted AND b.ext = 'fb2' AND b.src_year IS NULL
			        AND (b.src_lang IS NOT NULL OR b.src_title IS NOT NULL))
			ORDER BY a.id LIMIT 1`, st.AfterArchive).Scan(&archiveID, &fileName)
		if errors.Is(err, pgx.ErrNoRows) {
			st.Done = true
			if err := saveSrcYearsState(ctx, pool, st); err != nil {
				return total, err
			}
			logger.Info("fb2 src years backfill done", "editions", total, "archives", archives)
			return total, nil
		}
		if err != nil {
			return total, err
		}
		n, err := backfillArchiveSrcYears(ctx, pool, filepath.Join(booksRoot, fileName), archiveID, syncer, logger)
		if err != nil {
			if ctx.Err() != nil {
				return total, ctx.Err()
			}
			// Битый или недоступный архив не стопорит догон: пропускаем его.
			logger.Warn("fb2 src years: archive failed — skipped", "archive", fileName, "err", err)
		}
		total += n
		archives++
		st.AfterArchive = archiveID
		if err := saveSrcYearsState(ctx, pool, st); err != nil {
			return total, err
		}
		if archives%20 == 0 {
			logger.Info("fb2 src years backfill progress", "archives", archives, "editions", total)
		}
		select {
		case <-ctx.Done():
		case <-time.After(srcYearsArchivePause):
		}
	}
	return total, ctx.Err()
}

// backfillArchiveSrcYears — годы оригинала переводов одного архива.
func backfillArchiveSrcYears(ctx context.Context, pool *pgxpool.Pool, archivePath string, archiveID int64, syncer WorksIndexSyncer, logger *slog.Logger) (int, error) {
	rows, err := pool.Query(ctx, `
		SELECT id, file_name || '.' || ext FROM books
		WHERE archive_id = $1 AND NOT deleted AND ext = 'fb2' AND src_year IS NULL
		  AND (src_lang IS NOT NULL OR src_title IS NOT NULL)`, archiveID)
	if err != nil {
		return 0, err
	}
	type book struct {
		id   int64
		name string
	}
	var books []book
	for rows.Next() {
		var b book
		if err := rows.Scan(&b.id, &b.name); err != nil {
			rows.Close()
			return 0, err
		}
		books = append(books, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(books) == 0 {
		return 0, err
	}
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return 0, err
	}
	defer func() { _ = zr.Close() }()
	byName := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		byName[f.Name] = f
		if i := strings.LastIndex(f.Name, "/"); i >= 0 {
			if _, ok := byName[f.Name[i+1:]]; !ok {
				byName[f.Name[i+1:]] = f // basename — как openFB2
			}
		}
	}
	var ids []int64
	var years []int
	for _, b := range books {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		f := byName[b.name]
		if f == nil {
			continue
		}
		r, err := f.Open()
		if err != nil {
			continue
		}
		em := scanFb2EditionMeta(io.LimitReader(r, srcYearsHeaderLimit))
		_ = r.Close()
		if em.SrcYear > 0 {
			ids = append(ids, b.id)
			years = append(years, em.SrcYear)
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}
	wrows, err := pool.Query(ctx, `
		UPDATE books b SET src_year = v.y
		FROM unnest($1::bigint[], $2::int[]) AS v(id, y)
		WHERE b.id = v.id AND b.src_year IS NULL
		RETURNING b.work_id`, ids, years)
	if err != nil {
		return 0, err
	}
	works, err := pgx.CollectRows(wrows, pgx.RowTo[*int64])
	if err != nil {
		return 0, err
	}
	var workIDs []int64
	seen := map[int64]bool{}
	for _, w := range works {
		if w != nil && !seen[*w] {
			seen[*w] = true
			workIDs = append(workIDs, *w)
		}
	}
	changed, err := RecomputeWorkYears(ctx, pool, workIDs)
	if err != nil {
		return len(ids), err
	}
	if len(changed) > 0 && syncer != nil {
		if err := syncer.UpsertWorksToIndex(ctx, changed); err != nil {
			logger.Warn("fb2 src years: works index sync failed", "err", err)
		}
	}
	return len(ids), nil
}

func loadSrcYearsState(ctx context.Context, pool *pgxpool.Pool) (srcYearsState, error) {
	var st srcYearsState
	var raw []byte
	err := pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, srcYearsStateKey).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	return st, json.Unmarshal(raw, &st)
}

func saveSrcYearsState(ctx context.Context, pool *pgxpool.Pool, st srcYearsState) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2::jsonb, now())
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, srcYearsStateKey, string(raw))
	return err
}
