package importer

import (
	"context"
	"fmt"
	"math"
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
// «Известность») — зеркало serviceAuthorClassifyLockID.
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

	type agg struct {
		maxPop int64
		n      int
	}
	byAuthor := map[int64]*agg{}
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
			// renownPop — ТОЛЬКО внешние сигналы (computeWorkPopularityExternal):
			// личные просмотры/чтения/оценки владельца не делают автора
			// «известным» (иначе накликанный самиздат обгонял бы Толстого).
			if d.Kind != "" || d.renownPop <= 0 {
				continue // сборники и работы без внешних сигналов вклада не дают
			}
			for _, aid := range d.AuthorIDs {
				a := byAuthor[aid]
				if a == nil {
					a = &agg{}
					byAuthor[aid] = a
				}
				if d.renownPop > a.maxPop {
					a.maxPop = d.renownPop
				}
				if d.renownPop >= renownSignificantPop {
					a.n++
				}
			}
		}
		cursor = docs[len(docs)-1].ID
	}

	ids := make([]int64, 0, len(byAuthor))
	vals := make([]int64, 0, len(byAuthor))
	for id, a := range byAuthor {
		if r := computeAuthorRenown(a.maxPop, a.n); r > 0 {
			ids = append(ids, id)
			vals = append(vals, r)
		}
	}

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
