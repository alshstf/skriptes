package importer

import (
	"context"
	"fmt"
	"math"

	"github.com/jackc/pgx/v5"
)

// «Известность» АВТОРА для дефолтной сортировки /authors (колонка
// authors.renown, миграция 0038). Производная от известности его работ:
//
//	renown = maxPop + renownWBreadth·log₂(1 + N)
//
// где maxPop — максимум computeWorkPopularityExternal по НЕ-сборниковым работам
// автора (ТОЛЬКО внешние сигналы: издания/LIBRATE/голоса/экранизации/Фантлаб/
// OL/Wikipedia — БЕЗ личных views/reads/оценок инстанса, иначе накликанный
// владельцем самиздат обгонял бы Толстого; личная вовлечённость — сигнал для
// дефолта /books, но не для «известности» автора),
// N — число «значимых» работ (popularity ≥ renownSignificantPop). MAX-семантика
// принципиальна: сумма не дала бы плодовитому самиздату (50 работ по 100–160 от
// LIBRATE → ~841) обогнать автора одного хита (pop 2000 → 2120); log-бонус за
// широту подтягивает классиков с большим значимым корпусом (Толстой: maxPop
// ~1500 + ~25 значимых → ~2064). Сборники/антологии вне вклада — зеркало
// catalog.notCompilationClause (известность сборника — свойство сборника, не
// автора). Авторы без значимых сигналов держат renown=0 и уходят в алфавитный
// хвост списка.
//
// Меняешь формулу/веса — бампни ключ runOnce-гейта в main.go
// (author_renown_computed_v<N>), иначе на стабильном деплое пересчёт по новой
// формуле не запустится (грабля «мёртвого popularity» 1.5.x).
const (
	renownWBreadth       = 120.0 // ·log2(1+N значимых работ) — бонус за широту корпуса
	renownSignificantPop = 120   // порог «значимой» работы: LIBRATE 4 (136) да, LIBRATE 3 (112) нет
)

// authorRenownLockID — фиксированный ключ pg_advisory_lock: сериализует
// одновременные пересчёты (runOnce-гейт старта × after-import × хук воркера
// «Известность») — зеркало serviceAuthorClassifyLockID. Тот же ключ берёт
// catalog.RecomputeAuthorStats (book_count/rating_score тех же строк authors).
const authorRenownLockID = 0x617574687265 // "authre" в hex

func computeAuthorRenown(maxPop int64, significant int) int64 {
	if maxPop <= 0 {
		return 0
	}
	return maxPop + int64(math.Round(renownWBreadth*math.Log2(1+float64(significant))))
}

// RecomputeAuthorRenown пересчитывает authors.renown ЦЕЛИКОМ: курсорный скан
// живых работ через workDocSelect/scanWorkDocs (та же формула популярности, что
// в works-индексе — не дублируем её в SQL; Meili не трогается), агрегация по
// AuthorIDs в памяти, батч-UPDATE. Идемпотентен; стоимость ≈ полный ресинк
// works-индекса минус запись в Meili (рутинная операция). Возвращает число
// изменённых строк authors.
func (im *Importer) RecomputeAuthorRenown(ctx context.Context) (int64, error) {
	conn, err := im.deps.Pool.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("author renown: acquire conn: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, authorRenownLockID); err != nil {
		return 0, fmt.Errorf("author renown: advisory lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, authorRenownLockID)
	}()

	byAuthor := renownAggs{}
	const batchSize = 500
	var cursor int64
	for {
		docs, err := im.scanWorkDocs(ctx,
			` WHERE w.id > $1 AND EXISTS (SELECT 1 FROM books b WHERE b.work_id = w.id AND b.deleted = false)
			  ORDER BY w.id LIMIT $2`, cursor, batchSize)
		if err != nil {
			return 0, fmt.Errorf("author renown: scan works: %w", err)
		}
		if len(docs) == 0 {
			break
		}
		for _, d := range docs {
			byAuthor.add(d, nil)
		}
		cursor = docs[len(docs)-1].ID
	}

	ids, vals := byAuthor.values()

	var updated int64
	const updBatch = 5000
	for i := 0; i < len(ids); i += updBatch {
		j := min(i+updBatch, len(ids))
		tag, uerr := conn.Exec(ctx, `
			UPDATE authors a SET renown = v.renown
			FROM (SELECT unnest($1::bigint[]) AS id, unnest($2::bigint[]) AS renown) v
			WHERE a.id = v.id AND a.renown IS DISTINCT FROM v.renown`,
			ids[i:j], vals[i:j])
		if uerr != nil {
			return updated, fmt.Errorf("author renown: update batch: %w", uerr)
		}
		updated += tag.RowsAffected()
	}
	// Сброс устаревших: авторы, выпавшие из множества «с известностью»
	// (например, единственная сигнальная работа стала сборником/удалилась).
	tag, err := conn.Exec(ctx,
		`UPDATE authors SET renown = 0 WHERE renown <> 0 AND NOT (id = ANY($1::bigint[]))`, ids)
	if err != nil {
		return updated, fmt.Errorf("author renown: reset stale: %w", err)
	}
	updated += tag.RowsAffected()
	return updated, nil
}

// renownAggs — агрегат известности по авторам: максимум и число значимых работ.
type renownAggs map[int64]*renownAgg

type renownAgg struct {
	maxPop int64
	n      int
}

// add учитывает работу у её авторов (only != nil — только у авторов из only).
// renownPop — ТОЛЬКО внешние сигналы (computeWorkPopularityExternal): личные
// просмотры/чтения/оценки владельца не делают автора «известным» (иначе
// накликанный самиздат обгонял бы Толстого).
func (ra renownAggs) add(d workDoc, only map[int64]bool) {
	if d.Kind != "" || d.renownPop <= 0 {
		return // сборники и работы без внешних сигналов вклада не дают
	}
	for _, aid := range d.AuthorIDs {
		if only != nil && !only[aid] {
			continue
		}
		a := ra[aid]
		if a == nil {
			a = &renownAgg{}
			ra[aid] = a
		}
		if d.renownPop > a.maxPop {
			a.maxPop = d.renownPop
		}
		if d.renownPop >= renownSignificantPop {
			a.n++
		}
	}
}

// values — авторы с ненулевой известностью и её значения.
func (ra renownAggs) values() (ids, vals []int64) {
	for id, a := range ra {
		if r := computeAuthorRenown(a.maxPop, a.n); r > 0 {
			ids = append(ids, id)
			vals = append(vals, r)
		}
	}
	return ids, vals
}

// RecomputeAuthorRenownFor пересчитывает authors.renown только у авторов работ
// workIDs — по ВСЕМ их работам (известность автора — максимум по корпусу).
// Воркер «Известность» находит новые счётчики у сотен работ за проход, а полный
// RecomputeAuthorRenown сканирует все 437 тыс. работ (~3,6 мин каждые 30 мин,
// #300). Возвращает число изменённых строк authors.
func (im *Importer) RecomputeAuthorRenownFor(ctx context.Context, workIDs []int64) (int64, error) {
	if len(workIDs) == 0 {
		return 0, nil
	}
	conn, err := im.deps.Pool.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("author renown: acquire conn: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, authorRenownLockID); err != nil {
		return 0, fmt.Errorf("author renown: advisory lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, authorRenownLockID)
	}()

	ids := func(sql string, args ...any) ([]int64, error) {
		rows, err := conn.Query(ctx, sql, args...)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, pgx.RowTo[int64])
	}
	authors, err := ids(`
		SELECT DISTINCT ba.author_id FROM books b JOIN book_authors ba ON ba.book_id = b.id
		WHERE b.deleted = false AND b.work_id = ANY($1)`, workIDs)
	if err != nil {
		return 0, fmt.Errorf("author renown: authors of works: %w", err)
	}
	if len(authors) == 0 {
		return 0, nil
	}
	works, err := ids(`
		SELECT DISTINCT b.work_id FROM book_authors ba JOIN books b ON b.id = ba.book_id
		WHERE ba.author_id = ANY($1) AND b.deleted = false AND b.work_id IS NOT NULL`, authors)
	if err != nil {
		return 0, fmt.Errorf("author renown: works of authors: %w", err)
	}
	only := make(map[int64]bool, len(authors))
	for _, id := range authors {
		only[id] = true
	}
	byAuthor := renownAggs{}
	const batchSize = 500
	for i := 0; i < len(works); i += batchSize {
		docs, err := im.scanWorkDocs(ctx, ` WHERE w.id = ANY($1)`, works[i:min(i+batchSize, len(works))])
		if err != nil {
			return 0, fmt.Errorf("author renown: scan works: %w", err)
		}
		for _, d := range docs {
			byAuthor.add(d, only)
		}
	}
	rIDs, rVals := byAuthor.values()
	// Авторы набора без известности — 0, остальные — посчитанное.
	tag, err := conn.Exec(ctx, `
		UPDATE authors a SET renown = COALESCE(v.renown, 0)
		FROM unnest($1::bigint[]) AS s(id)
		LEFT JOIN (SELECT unnest($2::bigint[]) AS id, unnest($3::bigint[]) AS renown) v ON v.id = s.id
		WHERE a.id = s.id AND a.renown IS DISTINCT FROM COALESCE(v.renown, 0)`,
		authors, rIDs, rVals)
	if err != nil {
		return 0, fmt.Errorf("author renown: update: %w", err)
	}
	return tag.RowsAffected(), nil
}
