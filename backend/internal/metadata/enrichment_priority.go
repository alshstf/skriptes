package metadata

import "time"

// bookCoreCond — SQL-условие «книга из ЯДРА коллекции» для приоритизации
// внешнего обогащения (год, язык оригинала): работа книги имеет переиздания
// (≥2 живых изданий) ∪ экранизацию ∪ рейтинг LIBRATE. Book-level зеркало
// candidateCond воркера «Известность» (renown_backfill.go, work-level).
//
// Зачем: полный проход книг по id — это дни/недели при вежливых RPM, а голова
// таблицы books на реальном проде — современный самиздат, которого нет во
// внешних источниках (замер 2026-07-19: первые 528 кандидатов src_lang — все
// not_found, сплошь litnet-нативы). Двухфазный обход «сначала ядро, потом
// хвост» приносит год/оригинал знаменитым книгам (которые реально открывают)
// на дни раньше, не меняя ни охвата, ни итоговой полноты прохода.
//
// Употребление: drain-циклы year_backfill / src_lang_backfill гоняют кандидатов
// двумя фазами — `AND bookCoreCond`, затем `AND NOT bookCoreCond`. Ссылается
// только на алиас b (books) — вставлять в запросы, где books = b.
const bookCoreCond = `(
	EXISTS (SELECT 1 FROM books b2 WHERE b2.work_id = b.work_id AND b2.deleted = false AND b2.id <> b.id)
	OR EXISTS (
		SELECT 1 FROM book_adaptations ad
		JOIN books bb ON bb.id = ad.book_id
		WHERE bb.work_id = b.work_id AND bb.deleted = false
	)
	OR EXISTS (SELECT 1 FROM books bb WHERE bb.work_id = b.work_id AND bb.deleted = false AND bb.rating > 0)
)`

// tailNotFoundTTL — «не найдено» у книги вне ядра перепроверяется не чаще раза в
// год, какой бы срок ни стоял в настройках воркера (решение по #294): на проде
// перепроверки хвоста находили 0,06–0,6 % полезного, а язык оригинала с августа
// не нашёл ничего. Срок из настроек действует для ядра. После доработки поиска
// перепроверить раньше — кнопка «Сбросить неудачные попытки».
const tailNotFoundTTL = 365 * 24 * time.Hour

// enrichPhase — фаза обхода «сначала ядро, потом хвост».
type enrichPhase struct {
	cond string // "AND <core>" / "AND NOT <core>" — доп. условие выборки (алиас books = b)
	tail bool   // хвост: «не найдено» не чаще tailNotFoundTTL
}

// corePhases — фазы drain-циклов воркеров год / язык оригинала / рейтинг.
var corePhases = []enrichPhase{
	{cond: "AND " + bookCoreCond},
	{cond: "AND NOT " + bookCoreCond, tail: true},
}

// forPhase — сроки перепроверки для фазы: в хвосте not_found — не чаще
// tailNotFoundTTL.
func (t lookupTTL) forPhase(p enrichPhase) lookupTTL {
	if p.tail && t.notFound < tailNotFoundTTL {
		t.notFound = tailNotFoundTTL
	}
	return t
}
