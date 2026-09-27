package metadata

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// workKindClassifyLockID — произвольный, но фиксированный ключ pg_advisory_lock,
// под которым сериализуется ClassifyWorkKinds (см. её док). Значение не
// пересекается с другими advisory-локами кодовой базы (их пока нет).
const workKindClassifyLockID = 0x776b636c61737379 // "wkclassy" в hex

// ClassifyWorkKinds — эвристическая типизация работ: сборники/антологии/тома
// собраний сочинений (works.kind) отделяются от обычных произведений, чтобы
// карточка автора могла вынести их в отдельную секцию (план
// compilations-author-page-plan). Три сигнала, от слабого к сильному (при
// нескольких берётся самый сильный):
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
//
// ⚠️ Сериализуется session-level advisory-lock'ом: на ПЕРВОМ деплое фичи
// runOnce-классификация (горутина старта) и after-import классификация
// (в runStartupImport) стартуют одновременно, оба делают полнотабличные
// UPDATE works в разном порядке блокировки строк → deadlock, один падает
// (наблюдали на проде 1.9.0). Лок гарантирует, что второй вызов ЖДЁТ первый,
// а не конфликтует (первый расставит kind → второй пройдёт по 0 строк).
func ClassifyWorkKinds(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	return classifyWorkKinds(ctx, pool, nil)
}

// ReclassifyWorkKinds — ClassifyWorkKinds, но у работ ids эвристический тип
// сначала сбрасывается: он мог держаться на прежнем названии («Собрание
// сочинений… Золотой теленок» → «Золотой теленок», #306), а обратной очистки
// у эвристики нет. Сигналы, которые остались (серия-паразит, ≥4 авторов),
// вернут тип тем же проходом. Метки fantlab/override не трогаются.
func ReclassifyWorkKinds(ctx context.Context, pool *pgxpool.Pool, ids []int64) (int64, error) {
	return classifyWorkKinds(ctx, pool, ids)
}

func classifyWorkKinds(ctx context.Context, pool *pgxpool.Pool, reset []int64) (int64, error) {
	var total int64

	// Захватываем отдельное соединение под advisory-lock: лок сессионный
	// (держится тем conn, что его взял), поэтому и UPDATE'ы гоним по нему —
	// одна сессия на всю критическую секцию.
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("acquire conn: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, workKindClassifyLockID); err != nil {
		return 0, fmt.Errorf("advisory lock: %w", err)
	}
	// Разлочиваем на context.Background(): если исходный ctx уже отменён (напр.
	// шатдаун), Exec по нему был бы no-op и лок повис бы до конца сессии conn.
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, workKindClassifyLockID)
	}()

	if len(reset) > 0 {
		if _, err := conn.Exec(ctx, `
			UPDATE works SET kind = NULL, kind_source = NULL
			WHERE id = ANY($1) AND kind_source = 'heuristic'`, reset); err != nil {
			return 0, fmt.Errorf("reset heuristic kinds: %w", err)
		}
	}

	// Один UPDATE: тип каждой работы по самому сильному сигналу (≥4 авторов →
	// серия → название), и запись только если тип реально меняется. Раньше три
	// UPDATE шли подряд и перетирали друг друга: работа с двумя сигналами
	// переписывалась на каждом прогоне (прод: 12 825 строк на каждом старте),
	// держа блокировки строк — отсюда и deadlock с разовыми шагами старта (#300).
	//
	// title_sig: ~* — регистронезависимо; \m/\M — границы слова в PG-регекспах.
	// series_sig: «Антология» в серии → anthology, остальное (сборники/ПСС/
	// избранное/«Миры X») → omnibus; при нескольких сериях у изданий работы
	// антология важнее. ⚠️ ТОЛЬКО мн.ч. «Сборники» («Шекли, Роберт. Сборники» —
	// серия ИЗ сборников). Ед.ч. «(сборник)» — librusec-РАЗВОРОТ одного сборника
	// на отдельные fb2-рассказы («Тринадцать загадочных случаев (сборник)»): её
	// члены — рассказы, не сборники (снято с прод-данных 2026-07-05).
	// author_sig: ≥4 уникальных авторов по живым изданиям → антология.
	tag, err := conn.Exec(ctx, `
		WITH title_sig AS (
			SELECT w.id, CASE
				WHEN w.title ~* '\m(антолог(ия|ии)|anthology)\M' THEN 'anthology'
				WHEN w.title ~* '(собрание сочинений|избранн(ое|ые) произведения|\momnibus\M)' THEN 'omnibus'
				ELSE 'collection'
			END AS kind
			FROM works w
			WHERE w.title ~* '(\m(сборник|антолог(ия|ии)|anthology|omnibus)\M|собрание сочинений|избранн(ое|ые) произведения|повести и рассказы|рассказы и повести|collected (stories|works)|complete (stories|short stories|works))'
		), series_sig AS (
			SELECT b.work_id AS id,
			       CASE WHEN bool_or(s.title ~* '\mантолог') THEN 'anthology' ELSE 'omnibus' END AS kind
			FROM books b JOIN series s ON s.id = b.series_id
			WHERE b.deleted = false AND b.work_id IS NOT NULL
			  AND s.title ~* '(\mсборники\M|\mантолог|полное собрание сочинений|собрание сочинений|избранные произведения|^миры\M)'
			GROUP BY b.work_id
		), author_sig AS (
			SELECT b.work_id AS id, 'anthology' AS kind
			FROM books b JOIN book_authors ba ON ba.book_id = b.id
			WHERE b.deleted = false AND b.work_id IS NOT NULL
			GROUP BY b.work_id
			HAVING count(DISTINCT ba.author_id) >= 4
		), sig AS (
			SELECT DISTINCT ON (id) id, kind FROM (
				SELECT id, kind, 1 AS prio FROM author_sig
				UNION ALL SELECT id, kind, 2 FROM series_sig
				UNION ALL SELECT id, kind, 3 FROM title_sig
			) x
			ORDER BY id, prio
		)
		UPDATE works w SET kind = sig.kind, kind_source = 'heuristic'
		FROM sig
		WHERE w.id = sig.id
		  AND (w.kind_source IS NULL OR w.kind_source = 'heuristic')
		  AND (w.kind IS DISTINCT FROM sig.kind OR w.kind_source IS NULL)
	`)
	if err != nil {
		return 0, fmt.Errorf("classify work kinds: %w", err)
	}
	total += tag.RowsAffected()

	return total, nil
}
