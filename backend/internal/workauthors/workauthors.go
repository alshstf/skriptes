// Package workauthors — кто считается автором РАБОТЫ (#464).
//
// Раньше авторы работы были объединением авторов всех её изданий, и антология
// или сборник с тем же названием приносили в работу чужие фамилии: у
// «Мира-Кольца» — Олдисс, у «Патруля времени» — 16 авторов антологии (прод
// 2026-10: 65 работ, 194 таких автора). Теперь автор работы — тот, кто есть хотя
// бы в половине её живых изданий, плюс самые частые авторы (чтобы работа без
// «большинства» не осталась без авторов). У работы из одного-двух изданий это
// по-прежнему все авторы. Остальные видны только на строке своего издания.
//
// Правило общее для индекса работ (importer.workDocSelect: authors, author_ids —
// они же фильтр страницы автора) и шапки карточки (books.queryWorkAuthors),
// поэтому живёт отдельно.
package workauthors

// Core — подзапрос со столбцами (author_id, minpos): авторы работы workExpr
// (SQL-выражение с её id: «w.id», «$1»). minpos — наименьшая позиция автора в
// изданиях, для порядка.
func Core(workExpr string) string {
	return `SELECT x.author_id, x.minpos FROM (
		SELECT ba.author_id, min(ba.position) AS minpos, count(DISTINCT ba.book_id) AS k,
		       max(count(DISTINCT ba.book_id)) OVER () AS kmax
		FROM book_authors ba
		JOIN books b ON b.id = ba.book_id
		WHERE b.work_id = ` + workExpr + ` AND b.deleted = false
		GROUP BY ba.author_id
	) x
	WHERE x.k = x.kmax
	   OR x.k * 2 >= (SELECT count(*) FROM books e WHERE e.work_id = ` + workExpr + ` AND e.deleted = false)`
}
