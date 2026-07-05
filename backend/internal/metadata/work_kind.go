package metadata

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ClassifyWorkKinds — эвристическая типизация работ: сборники/антологии/тома
// собраний сочинений (works.kind) отделяются от обычных произведений, чтобы
// карточка автора могла вынести их в отдельную секцию (план
// compilations-author-page-plan). Три сигнала, от слабого к сильному (каждый
// следующий UPDATE перетирает предыдущий):
//
//  1. title-паттерн работы («сборник», «антология», «собрание сочинений»,
//     «избранное») → collection/omnibus. Слова целиком; «том N» СОЗНАТЕЛЬНО не
//     используется («Тихий Дон. Том 1» — половина романа, не сборник).
//  2. серия-паразит — библиотекари librusec уже разметили сборники сериями
//     («Шекли, Роберт. Сборники», «Антология фантастики», «ПСС в 90 томах»,
//     «Избранные произведения») → все работы серии omnibus/anthology.
//  3. многоавторность: ≥4 уникальных авторов у работы → anthology
//     (2–3 автора НЕ метим — обычное соавторство: Асприн+Най).
//
// Идемпотентен; правит ТОЛЬКО строки с kind_source IS NULL или 'heuristic' —
// метки fantlab (PR2) и override (PR4) эвристика не перетирает. Обратной
// очистки нет: работа, переставшая матчиться (переименование), сохраняет kind
// до правки оверрайдом/фантлабом — осознанно, переименования редки.
//
// Один SQL-проход на сигнал (полнотабличный, но по индексируемым выражениям) —
// зовётся редко: runOnce-гейт на старте + после импорта (новые работы).
func ClassifyWorkKinds(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var total int64

	// 1. Title-паттерн самой работы. ~* — регистронезависимо; \m/\M — границы
	// слова в PG-регекспах (word start/end).
	tag, err := pool.Exec(ctx, `
		UPDATE works w SET kind = CASE
			WHEN w.title ~* '\m(антолог(ия|ии)|anthology)\M' THEN 'anthology'
			WHEN w.title ~* '(собрание сочинений|избранн(ое|ые) произведения|\momnibus\M)' THEN 'omnibus'
			ELSE 'collection'
		END, kind_source = 'heuristic'
		WHERE (w.kind_source IS NULL OR w.kind_source = 'heuristic')
		  AND w.title ~* '(\m(сборник|антолог(ия|ии)|anthology|omnibus)\M|собрание сочинений|избранн(ое|ые) произведения|повести и рассказы|рассказы и повести|collected (stories|works)|complete (stories|short stories|works))'
	`)
	if err != nil {
		return 0, fmt.Errorf("classify by title: %w", err)
	}
	total += tag.RowsAffected()

	// 2. Серия-паразит: паттерн в НАЗВАНИИ СЕРИИ метит все работы, чьи живые
	// издания лежат в этой серии. «Антология» в серии → anthology, остальное
	// (сборники/ПСС/избранное/«Миры X») → omnibus.
	// ⚠️ ТОЛЬКО мн.ч. «Сборники» («Шекли, Роберт. Сборники» — серия ИЗ сборников).
	// Ед.ч. «(сборник)» — librusec-РАЗВОРОТ одного сборника на отдельные
	// fb2-рассказы («Тринадцать загадочных случаев (сборник)»): её члены —
	// рассказы, не сборники, метить их нельзя (снято с прод-данных 2026-07-05).
	tag, err = pool.Exec(ctx, `
		UPDATE works w SET kind = CASE
			WHEN s.title ~* '\mантолог' THEN 'anthology'
			ELSE 'omnibus'
		END, kind_source = 'heuristic'
		FROM books b JOIN series s ON s.id = b.series_id
		WHERE b.work_id = w.id AND b.deleted = false
		  AND (w.kind_source IS NULL OR w.kind_source = 'heuristic')
		  AND s.title ~* '(\mсборники\M|\mантолог|полное собрание сочинений|собрание сочинений|избранные произведения|^миры\M)'
	`)
	if err != nil {
		return 0, fmt.Errorf("classify by series: %w", err)
	}
	total += tag.RowsAffected()

	// 3. Многоавторность (сильнейший сигнал — последним): ≥4 уникальных авторов
	// по живым изданиям работы → антология.
	tag, err = pool.Exec(ctx, `
		UPDATE works w SET kind = 'anthology', kind_source = 'heuristic'
		WHERE (w.kind_source IS NULL OR w.kind_source = 'heuristic')
		  AND w.kind IS DISTINCT FROM 'anthology'
		  AND (SELECT count(DISTINCT ba.author_id)
		       FROM books b JOIN book_authors ba ON ba.book_id = b.id
		       WHERE b.work_id = w.id AND b.deleted = false) >= 4
	`)
	if err != nil {
		return 0, fmt.Errorf("classify by author count: %w", err)
	}
	total += tag.RowsAffected()

	return total, nil
}
