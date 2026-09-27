package importer

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/inpx"
)

// Разделение тёзок. librusec с выпуска 2026-09 различает тёзок уточнением в
// скобках («Антоний [Блум]», «Антоний [Храповицкий]»), а у нас они до сих пор
// одна запись «Антоний» — с подписками, био/фото, известностью. Перед импортом
// такого INPX решаем, какой вариант унаследует прежнюю запись (её id и всё, что
// к ней привязано); остальные варианты импорт заведёт новыми авторами.
//
// Наследник — вариант, которому файл приписывает больше всего книг прежней
// записи. Ничья: вариант без уточнения → больше активности пользователей на
// этих книгах (чтение, просмотры, полки, оценки) → больше книг в файле → по
// алфавиту уточнения. Каждое решение пишется в author_splits.
//
// У прежней записи, разделившейся на нескольких тёзок, сбрасываются био, фото
// и маркер попытки: они могли прийти от любого из склеенных людей и
// перекачаются заново (решение владельца, план inpx-2026-09-authors-series).

type bookKey struct{ archive, libID string }

type splitVariant struct {
	note      string // как в файле; "" — без уточнения
	claims    map[bookKey]struct{}
	before    int // книг прежней записи среди claims
	activity  int
	isHeir    bool
	heirWhy   string
	claimList []bookKey // claims ∩ книги прежней записи — для подсчёта активности
}

// planAuthorSplits — проход по INPX до импорта. Возвращает число имён, чьи
// прежние записи разделились на тёзок.
func (im *Importer) planAuthorSplits(ctx context.Context, ix *inpx.Inpx, inpxFile string) (int, error) {
	// 1. Имена, у которых в файле есть варианты с уточнением.
	noted := map[string]bool{}
	err := ix.Each(func(_ inpx.InpFile, rec inpx.Record) error {
		for _, a := range rec.Authors {
			if a.Note != "" {
				if base := normalizedAuthorName(a); base != "" {
					noted[base] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("scan author notes: %w", err)
	}
	if len(noted) == 0 {
		return 0, nil
	}

	// 2. Из них — те, что в базе ещё одна запись без уточнения (не разделены).
	bases := make([]string, 0, len(noted))
	for b := range noted {
		bases = append(bases, b)
	}
	owners := map[string]int64{}
	rows, err := im.deps.Pool.Query(ctx, `
		SELECT a.normalized_name::text, a.id
		FROM authors a
		WHERE a.normalized_name = ANY($1::citext[]) AND a.name_note IS NULL
		  AND NOT EXISTS (SELECT 1 FROM authors x
		                  WHERE x.normalized_name = a.normalized_name AND x.name_note IS NOT NULL)`, bases)
	if err != nil {
		return 0, fmt.Errorf("find authors to split: %w", err)
	}
	for rows.Next() {
		var base string
		var id int64
		if err := rows.Scan(&base, &id); err != nil {
			rows.Close()
			return 0, err
		}
		owners[base] = id
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(owners) == 0 {
		return 0, nil
	}

	// 3. Книги каждого варианта этих имён в файле.
	variants := map[string]map[string]*splitVariant{} // base → lower(note) → вариант
	err = ix.Each(func(f inpx.InpFile, rec inpx.Record) error {
		for _, a := range rec.Authors {
			base := normalizedAuthorName(a)
			if _, ok := owners[base]; !ok {
				continue
			}
			byNote := variants[base]
			if byNote == nil {
				byNote = map[string]*splitVariant{}
				variants[base] = byNote
			}
			k := strings.ToLower(strings.TrimSpace(a.Note))
			v := byNote[k]
			if v == nil {
				v = &splitVariant{note: strings.TrimSpace(a.Note), claims: map[bookKey]struct{}{}}
				byNote[k] = v
			}
			v.claims[bookKey{f.Archive, rec.LibID}] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("scan author variants: %w", err)
	}

	split := 0
	for base, byNote := range variants {
		oldID := owners[base]
		vs := make([]*splitVariant, 0, len(byNote))
		for _, v := range byNote {
			vs = append(vs, v)
		}
		if err := im.chooseHeir(ctx, oldID, vs); err != nil {
			return split, err
		}
		isSplit := len(vs) > 1
		if err := im.applySplit(ctx, base, oldID, vs, isSplit, inpxFile); err != nil {
			return split, err
		}
		if isSplit {
			split++
		}
	}
	return split, nil
}

// chooseHeir считает, сколько книг прежней записи досталось каждому варианту,
// и помечает наследника (см. doc файла).
func (im *Importer) chooseHeir(ctx context.Context, oldID int64, vs []*splitVariant) error {
	rows, err := im.deps.Pool.Query(ctx, `
		SELECT ar.filename, b.lib_id
		FROM book_authors ba
		JOIN books b     ON b.id = ba.book_id
		JOIN archives ar ON ar.id = b.archive_id
		WHERE ba.author_id = $1`, oldID)
	if err != nil {
		return fmt.Errorf("books of author %d: %w", oldID, err)
	}
	owned := map[bookKey]struct{}{}
	for rows.Next() {
		var k bookKey
		if err := rows.Scan(&k.archive, &k.libID); err != nil {
			rows.Close()
			return err
		}
		owned[k] = struct{}{}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, v := range vs {
		for k := range v.claims {
			if _, ok := owned[k]; ok {
				v.before++
				v.claimList = append(v.claimList, k)
			}
		}
	}

	// Детерминированный порядок: по уточнению.
	sort.Slice(vs, func(i, j int) bool { return strings.ToLower(vs[i].note) < strings.ToLower(vs[j].note) })
	best := 0
	for _, v := range vs {
		best = max(best, v.before)
	}
	tied := []*splitVariant{}
	for _, v := range vs {
		if v.before == best {
			tied = append(tied, v)
		}
	}
	if len(tied) == 1 {
		tied[0].isHeir, tied[0].heirWhy = true, "majority"
		return nil
	}
	for _, v := range tied {
		if v.note == "" {
			v.isHeir, v.heirWhy = true, "tie:plain"
			return nil
		}
	}
	for _, v := range tied {
		n, err := im.bookActivity(ctx, v.claimList)
		if err != nil {
			return err
		}
		v.activity = n
	}
	pick := func(key func(*splitVariant) int, why string) bool {
		top, count := -1, 0
		var winner *splitVariant
		for _, v := range tied {
			switch k := key(v); {
			case k > top:
				top, count, winner = k, 1, v
			case k == top:
				count++
			}
		}
		if count == 1 && winner != nil {
			winner.isHeir, winner.heirWhy = true, why
			return true
		}
		// оставляем в игре только лучших по этому признаку
		kept := tied[:0]
		for _, v := range tied {
			if key(v) == top {
				kept = append(kept, v)
			}
		}
		tied = kept
		return false
	}
	if pick(func(v *splitVariant) int { return v.activity }, "tie:activity") {
		return nil
	}
	if pick(func(v *splitVariant) int { return len(v.claims) }, "tie:books") {
		return nil
	}
	tied[0].isHeir, tied[0].heirWhy = true, "tie:name" // vs отсортированы по уточнению
	return nil
}

// bookActivity — сколько следов пользователей на книгах (по ключу файла):
// чтение, просмотры, полки, оценки их работ.
func (im *Importer) bookActivity(ctx context.Context, keys []bookKey) (int, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	archives := make([]string, len(keys))
	libIDs := make([]string, len(keys))
	for i, k := range keys {
		archives[i], libIDs[i] = k.archive, k.libID
	}
	var n int
	err := im.deps.Pool.QueryRow(ctx, `
		WITH bk AS (
			SELECT b.id, b.work_id
			FROM unnest($1::text[], $2::text[]) AS k(archive, lib_id)
			JOIN archives ar ON ar.filename = k.archive
			JOIN books b     ON b.archive_id = ar.id AND b.lib_id = k.lib_id
		)
		SELECT (SELECT count(*) FROM reads r WHERE r.book_id IN (SELECT id FROM bk))
		     + (SELECT count(*) FROM views v WHERE v.book_id IN (SELECT id FROM bk))
		     + (SELECT count(*) FROM user_collection_books c WHERE c.book_id IN (SELECT id FROM bk))
		     + (SELECT count(*) FROM book_ratings r WHERE r.work_id IN (SELECT work_id FROM bk))`,
		archives, libIDs).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("book activity: %w", err)
	}
	return n, nil
}

// applySplit — наследнику отдаётся прежняя запись (уточнение дописывается к
// ней), при разделении на тёзок у неё сбрасывается обогащение; решение — в журнал.
func (im *Importer) applySplit(ctx context.Context, base string, oldID int64, vs []*splitVariant, isSplit bool, inpxFile string) error {
	tx, err := im.deps.Pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, v := range vs {
		if !v.isHeir {
			continue
		}
		if v.note != "" {
			if _, err := tx.Exec(ctx, `UPDATE authors SET name_note = $2 WHERE id = $1`, oldID, v.note); err != nil {
				return fmt.Errorf("set note of author %d: %w", oldID, err)
			}
		}
	}
	if isSplit {
		if _, err := tx.Exec(ctx, `
			UPDATE authors SET bio = NULL, photo_path = NULL, metadata_fetched_at = NULL
			WHERE id = $1`, oldID); err != nil {
			return fmt.Errorf("reset enrichment of author %d: %w", oldID, err)
		}
	}
	for _, v := range vs {
		why := v.heirWhy
		if !v.isHeir {
			why = "namesake"
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO author_splits (base_name, old_author_id, note, is_heir, books_before, books_in_file, reason, inpx_file)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			base, oldID, v.note, v.isHeir, v.before, len(v.claims), why, inpxFile); err != nil {
			return fmt.Errorf("log split of %q: %w", base, err)
		}
	}
	return tx.Commit(ctx)
}

// fixWorkPrimaryAuthors — работам, чей основной автор больше не среди авторов
// их живых изданий (книги ушли к тёзке или автор исправлен в выпуске), ставит
// первого автора представительного издания (якорь → min id). Работы с ручной
// правкой авторов (metadata_overrides) не трогает.
func fixWorkPrimaryAuthors(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE works w SET primary_author_id = p.author_id, updated_at = now()
		FROM (
			SELECT DISTINCT ON (w2.id) w2.id AS work_id, ba.author_id
			FROM works w2
			JOIN books b         ON b.work_id = w2.id AND b.deleted = false
			JOIN book_authors ba ON ba.book_id = b.id
			WHERE w2.primary_author_id IS NOT NULL
			  AND NOT EXISTS (
			      SELECT 1 FROM books b2 JOIN book_authors ba2 ON ba2.book_id = b2.id
			      WHERE b2.work_id = w2.id AND b2.deleted = false AND ba2.author_id = w2.primary_author_id)
			  AND NOT EXISTS (
			      SELECT 1 FROM metadata_overrides o
			      WHERE o.target_kind = 'work' AND o.target_id = w2.id AND o.field = 'authors')
			ORDER BY w2.id, (b.normalized_title = w2.normalized_title) DESC, b.id, ba.position
		) p
		WHERE w.id = p.work_id`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// fixStaleWorkSeries — работам, чья серия больше не стоит ни у одного живого
// издания (импорт обновляет серию книги, но не работы — после разделения тёзок
// книга уходит в серию нового автора), берёт серию и номер представительного
// издания (якорь → min id; может быть и «без серии»). Ручные правки серии/номера
// не трогает.
func fixStaleWorkSeries(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	tag, err := pool.Exec(ctx, `
		UPDATE works w SET series_id = p.series_id, ser_no = p.ser_no, updated_at = now()
		FROM (
			SELECT DISTINCT ON (w2.id) w2.id AS work_id, b.series_id, b.ser_no
			FROM works w2
			JOIN books b ON b.work_id = w2.id AND b.deleted = false
			WHERE w2.series_id IS NOT NULL
			  AND NOT EXISTS (
			      SELECT 1 FROM books b2
			      WHERE b2.work_id = w2.id AND b2.deleted = false AND b2.series_id = w2.series_id)
			  AND NOT EXISTS (
			      SELECT 1 FROM metadata_overrides o
			      WHERE o.target_kind = 'work' AND o.target_id = w2.id AND o.field IN ('series', 'ser_no'))
			ORDER BY w2.id, (b.normalized_title = w2.normalized_title) DESC, b.id
		) p
		WHERE w.id = p.work_id`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// deleteEmptySeries — удаляет серии без книг и работ, на которые никто не
// подписан и у которых нет ручных правок: после переноса книг (разделение
// тёзок, правки выпуска) такие серии висели бы в подсказках и поиске.
func deleteEmptySeries(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	tag, err := pool.Exec(ctx, `
		DELETE FROM series s
		WHERE NOT EXISTS (SELECT 1 FROM books b WHERE b.series_id = s.id)
		  AND NOT EXISTS (SELECT 1 FROM works w WHERE w.series_id = s.id)
		  AND NOT EXISTS (SELECT 1 FROM favorite_series f WHERE f.series_id = s.id)
		  AND NOT EXISTS (SELECT 1 FROM metadata_overrides o WHERE o.target_kind = 'series' AND o.target_id = s.id)`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
