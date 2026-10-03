package metadata

import (
	"fmt"
	"time"
)

// lookupTTL — сроки перепроверки источника по исходу прошлой попытки (строка
// book_*_lookups / work_renown_lookups): нет строки → спрашиваем; found → через
// found (0 — никогда); not_found / error — через свои сроки; незнакомый исход →
// спрашиваем. Одни правила для Go (isDue) и для SQL (dueCond).
type lookupTTL struct {
	found    time.Duration // 0 = found не перепроверяем
	notFound time.Duration
	error    time.Duration
}

// retryTTL — сроки воркеров, у которых found окончательный (год, обложка,
// рейтинг, язык оригинала): not_found — в днях, error — в часах.
func retryTTL(notFoundDays, errorHours int) lookupTTL {
	return lookupTTL{
		notFound: time.Duration(notFoundDays) * 24 * time.Hour,
		error:    time.Duration(errorHours) * time.Hour,
	}
}

// isDue — пора ли (пере)спрашивать источник.
func (t lookupTTL) isDue(l lookupRow, now time.Time) bool {
	switch l.outcome {
	case "":
		return true // строки не было
	case "found":
		if t.found <= 0 {
			return false
		}
		return now.Sub(l.checkedAt) >= t.found
	case "not_found":
		return now.Sub(l.checkedAt) >= t.notFound
	case "error":
		return now.Sub(l.checkedAt) >= t.error
	default:
		return true
	}
}

// dueCond — SQL-предикат «кандидата пора спросить хотя бы у одного источника»:
// для какого-то источника из списка нет строки учёта со свежим исходом. То же,
// что isDue, но на стороне БД: без него проход раз в 30 минут перечитывал ВСЕХ
// кандидатов охвата (сотни тысяч строк с array_agg авторов) и отбрасывал их уже
// в Go — на большой коллекции Postgres держал ~100% CPU впустую (#244).
//
// table — таблица учёта, keyCol — её колонка ключа (book_id / work_id), keyExpr —
// ключ кандидата в запросе (b.id / w.id), n — номер первого из четырёх
// параметров, которые возвращает dueArgs. Пустой список источников → ложь
// (спрашивать некого — кандидатов нет).
func dueCond(table, keyCol, keyExpr string, n int) string {
	return fmt.Sprintf(`EXISTS (
		SELECT 1 FROM unnest($%[4]d::text[]) AS due_src(source)
		WHERE NOT EXISTS (
			SELECT 1 FROM %[1]s l
			WHERE l.%[2]s = %[3]s AND l.source = due_src.source
			  AND CASE l.outcome
			          WHEN 'found'     THEN $%[5]d::interval IS NULL OR l.checked_at > now() - $%[5]d::interval
			          WHEN 'not_found' THEN l.checked_at > now() - $%[6]d::interval
			          WHEN 'error'     THEN l.checked_at > now() - $%[7]d::interval
			          ELSE false
			      END
		)
	)`, table, keyCol, keyExpr, n, n+1, n+2, n+3)
}

// dueArgs — параметры dueCond: имена источников и сроки (found 0 → NULL, то
// есть found не перепроверяется).
func dueArgs(sources []string, ttl lookupTTL) []any {
	var found *time.Duration
	if ttl.found > 0 {
		f := ttl.found
		found = &f
	}
	if sources == nil {
		sources = []string{}
	}
	return []any{sources, found, ttl.notFound, ttl.error}
}
