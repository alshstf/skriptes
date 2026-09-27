package metadata

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/authorkind"
)

// serviceAuthorClassifyLockID — фиксированный ключ pg_advisory_lock, под
// которым сериализуется ClassifyServiceAuthors (runOnce на старте + вызов
// после импорта могут совпасть на первом деплое — зеркало гонки
// ClassifyWorkKinds, workKindClassifyLockID). Значение не пересекается с
// другими advisory-локами кодовой базы.
const serviceAuthorClassifyLockID = 0x73766361757468 // "svcauth" в hex

// ClassifyServiceAuthors — эвристическая разметка «служебных авторов»:
// агрегатов-псевдоавторов из INPX («Коллектив авторов», «Народные сказки»,
// «Газета Завтра», «неизвестный автор»…), которые не являются людьми и
// замусоривают раздел «Авторы» (находка прод-аудита 2026-07: топ «самых
// плодовитых» состоял из них — 805/741/737/504 книг).
//
// Паттерны — authorkind.ServiceNamePatterns, СОЗНАТЕЛЬНО консервативные
// (precision > recall, грабля №8/№13 — не выдумывать семантику): полные фразы,
// префиксы явно-издательских сущностей и «автор неизвестен»/«коллектив авторов»
// отдельными словами (librusec: «Эротика и секс | Автор неизвестен | --»).
// Однофамильцы-люди («Сборщиков», «Газетов») не задеваются. Недоборы (БСЭ,
// ГОСТ…) правит админ вручную (переключатель на карточке автора).
//
// Идемпотентен; НЕ трогает строки с is_service_source='manual' — ручное
// решение админа (в обе стороны) эвристика не перетирает. Обратной очистки
// нет: перестал матчиться — снимается только вручную (переименования редки).
func ClassifyServiceAuthors(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("acquire conn: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, serviceAuthorClassifyLockID); err != nil {
		return 0, fmt.Errorf("advisory lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock($1)`, serviceAuthorClassifyLockID)
	}()

	// Матчим по normalized_name (CITEXT, "фамилия имя отчество" lower) — правило
	// общее с планом межавторских серий импорта, см. authorkind.
	tag, err := conn.Exec(ctx, `
		UPDATE authors SET is_service = true, is_service_source = 'heuristic'
		WHERE is_service = false
		  AND is_service_source IS DISTINCT FROM 'manual'
		  AND normalized_name::text ~ ANY($1::text[])
	`, authorkind.ServiceNamePatterns)
	if err != nil {
		return 0, fmt.Errorf("classify service authors: %w", err)
	}
	return tag.RowsAffected(), nil
}
