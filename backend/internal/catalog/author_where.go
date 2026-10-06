package catalog

import (
	"fmt"
	"strings"

	"github.com/skriptes/skriptes/backend/internal/textnorm"
)

// Фасеты фильтров списка авторов (#389, A3): у каждого — свой фильтр, который
// при подсчёте его же значений не применяется (дизъюнктивные счётчики, как на
// /books: выбранный жанр не обнуляет соседние жанры).
const (
	FacetGenre       = "genre"
	FacetLang        = "lang"
	FacetSrcLang     = "src"
	FacetAdaptations = "adapt"
)

// authorWhere — условия фильтров списка авторов и их аргументы. Аргументы
// накапливаются по мере построения; add отдаёт номер плейсхолдера. КАЖДЫЙ
// переданный аргумент обязан быть упомянут в тексте запроса (PG не выводит тип
// неупомянутого $N) — поэтому исключения видимости рендерятся фрагментом со
// СВЕЖИМИ плейсхолдерами на каждом месте использования (exclusion), а не общим
// $1. Дублирование slice-аргумента исключений по местам дёшево (массив кодов мал).
type authorWhere struct {
	p    AuthorListParams
	args []any
}

func newAuthorWhere(p AuthorListParams) *authorWhere {
	return &authorWhere{p: p, args: make([]any, 0, 24)}
}

func (w *authorWhere) add(v any) int {
	w.args = append(w.args, v)
	return len(w.args)
}

// exclusion — фрагмент " AND (lang…) AND NOT EXISTS(genre…)" по алиасу `b`, с
// собственными плейсхолдерами. Пусто, если ни язык, ни жанр не скрыты.
func (w *authorWhere) exclusion() string {
	var sb strings.Builder
	if len(w.p.ExcludeLangs) > 0 {
		n := w.add(w.p.ExcludeLangs)
		fmt.Fprintf(&sb, " AND (b.lang IS NULL OR NOT (b.lang = ANY($%d::text[])))", n)
	}
	if len(w.p.ExcludeGenres) > 0 {
		n := w.add(w.p.ExcludeGenres)
		fmt.Fprintf(&sb, " AND NOT EXISTS (SELECT 1 FROM book_genres bgx JOIN genres gx ON gx.id = bgx.genre_id"+
			" WHERE bgx.book_id = b.id AND gx.fb2_code = ANY($%d::text[]))", n)
	}
	return sb.String()
}

// aggExclusion — exclusion + ВСЕГДА исключение сборников (loose coupling):
// сборники/антологии/тома собраний (works.kind) не входят в АГРЕГАТЫ и
// СТАТИСТИКУ автора (book_count, годы, жанры, языки, рейтинг, экранизации,
// сортировки/фильтры) — они свойство сборника, не «что написал автор». В отличие
// от opt-in hideCompilations, это безусловно. НЕ применяется к базовой видимости
// автора (появляется по любой книге) и к fav_books (личное избранное).
func (w *authorWhere) aggExclusion() string {
	return w.exclusion() + notCompilationClause
}

// filters — предикаты списка (склеиваются через AND). skip — фасет, чей
// собственный фильтр не применять ("" — все фильтры).
func (w *authorWhere) filters(skip string) []string {
	p := w.p
	// where — условия-фильтры для авторов (склеиваются через AND). Каждый
	// предикат — EXISTS по видимым книгам автора (либо строка автора). Эти
	// предикаты ОБЩИЕ для COUNT и главного запроса (одни плейсхолдеры).
	var where []string

	// База (всегда): только авторы с ≥1 ВИДИМОЙ книгой. Без неё в списке
	// всплывали «пустые» авторы (0 книг в каталоге) — это и шум, и клик по
	// такому автору ронял карточку (author.books == null). Исключения
	// видимости учитываются w.exclusion().
	// Участник только чужих антологий и выпусков журналов (все его работы —
	// сборники, и ни в одной он не основной автор) в список не попадает: на проде
	// таких 28,9 тыс. из 29,2 тыс. авторов «только со сборниками», и все они
	// показывались с «0 книг» (#265). Карточка по прямой ссылке и поиск остаются.
	where = append(where, "EXISTS (SELECT 1 FROM book_authors ba JOIN books b ON b.id = ba.book_id AND b.deleted = false"+
		" LEFT JOIN works wv ON wv.id = b.work_id"+
		" WHERE ba.author_id = a.id AND (COALESCE(wv.kind, '') = '' OR wv.primary_author_id = a.id)"+w.exclusion()+")")
	// Служебные авторы (агрегаты-псевдоавторы: «Коллектив авторов», «Народные
	// сказки», «Газета Завтра»…) — вон из СПИСКА и всех его сортировок (они
	// замусоривали топ «плодовитых», находка аудита). Карточка по прямой ссылке
	// (с карточки книги) и suggest/Cmd+K сознательно НЕ фильтруются — найти
	// агрегат намеренно можно. Метки: эвристика ClassifyServiceAuthors +
	// admin-переключатель на карточке автора.
	where = append(where, "NOT a.is_service")

	if q := strings.TrimSpace(p.Query); q != "" {
		// Префиксный ILIKE по normalized_name без различия «ё»/«е» (как в
		// SuggestAuthors, #278): GIN trigram index по тому же выражению
		// (authors_name_yo_trgm) ускоряет на длинных запросах.
		n := w.add(textnorm.FoldYo(escapeLike(q)))
		// Латиницей — по латинскому имени из fb2 переводов (миграция 0046).
		where = append(where, fmt.Sprintf(`(replace(a.normalized_name::text, 'ё', 'е') ILIKE $%[1]d || '%%' ESCAPE '\'`+
			` OR a.latin_name ILIKE $%[1]d || '%%' ESCAPE '\')`, n))
	}

	if p.FavoritesOnly && p.UserID > 0 {
		n := w.add(p.UserID)
		where = append(where, fmt.Sprintf(
			"EXISTS (SELECT 1 FROM favorite_authors fa WHERE fa.author_id = a.id AND fa.user_id = $%d)", n))
	}

	if len(p.Genres) > 0 && skip != FacetGenre {
		n := w.add(p.Genres)
		where = append(where, fmt.Sprintf(
			"EXISTS (SELECT 1 FROM book_authors ba JOIN books b ON b.id = ba.book_id AND b.deleted = false"+
				" JOIN book_genres bg ON bg.book_id = b.id JOIN genres g ON g.id = bg.genre_id"+
				" WHERE ba.author_id = a.id AND g.fb2_code = ANY($%d::text[])"+w.aggExclusion()+")", n))
	}

	if len(p.Langs) > 0 && skip != FacetLang {
		n := w.add(p.Langs)
		// Язык ИЗДАНИЯ (books.lang). Раньше этот фильтр матчил lang∪src_lang
		// одним условием — расщеплён на два независимых («Язык» и «Язык
		// оригинала», как на /books). Нормализуем на лету (lower+btrim) —
		// defensive, хотя импорт уже нормализует lang.
		where = append(where, fmt.Sprintf(
			"EXISTS (SELECT 1 FROM book_authors ba JOIN books b ON b.id = ba.book_id AND b.deleted = false"+
				" WHERE ba.author_id = a.id"+
				" AND lower(btrim(b.lang)) = ANY($%d::text[])"+
				w.aggExclusion()+")", n))
	}

	if len(p.SrcLangs) > 0 && skip != FacetSrcLang {
		n := w.add(p.SrcLangs)
		// Язык ОРИГИНАЛА, WORK-LEVEL (зеркало orig_lang works-индекса v8):
		// оригинал(ы) работы = непустые src_lang её изданий, и только когда
		// src_lang нет ни у одного издания — работа нативна (язык издания).
		// Книга автора матчит, если src_lang кого-то из со-изданий её работы ∈
		// набора, ИЛИ её lang ∈ набора при полном отсутствии src_lang у работы
		// (перевод-сирота на испанский при русском соседе с src_lang=en больше
		// не делает автора «оригинал: испанский»). Со-издания ищутся по индексу
		// books(work_id); `s.id = b.id` — defensive на случай книги без work_id.
		sibling := " FROM books s WHERE (s.work_id = b.work_id OR s.id = b.id) AND s.deleted = false" +
			" AND s.src_lang IS NOT NULL AND btrim(s.src_lang) <> ''"
		where = append(where, fmt.Sprintf(
			"EXISTS (SELECT 1 FROM book_authors ba JOIN books b ON b.id = ba.book_id AND b.deleted = false"+
				" WHERE ba.author_id = a.id"+
				" AND (EXISTS (SELECT 1"+sibling+" AND lower(btrim(s.src_lang)) = ANY($%d::text[]))"+
				" OR (lower(btrim(b.lang)) = ANY($%d::text[])"+
				" AND NOT EXISTS (SELECT 1"+sibling+")))"+
				w.aggExclusion()+")", n, n))
	}

	if p.YearFrom > 0 || p.YearTo > 0 {
		// Пересечение диапазона активности автора [min,max] с [from,to]:
		// существует видимая книга автора с written_year в [from,to].
		lo, hi := p.YearFrom, p.YearTo
		yearCond := "b.written_year IS NOT NULL"
		if lo > 0 {
			n := w.add(lo)
			yearCond += fmt.Sprintf(" AND b.written_year >= $%d", n)
		}
		if hi > 0 {
			n := w.add(hi)
			yearCond += fmt.Sprintf(" AND b.written_year <= $%d", n)
		}
		where = append(where, "EXISTS (SELECT 1 FROM book_authors ba JOIN books b ON b.id = ba.book_id AND b.deleted = false"+
			" WHERE ba.author_id = a.id AND "+yearCond+w.aggExclusion()+")")
	}

	if p.HasAdaptations && skip != FacetAdaptations {
		where = append(where,
			"EXISTS (SELECT 1 FROM book_authors ba JOIN books b ON b.id = ba.book_id AND b.deleted = false"+
				" JOIN book_adaptations ad ON ad.book_id = b.id WHERE ba.author_id = a.id"+w.aggExclusion()+")")
	}

	if p.MinRating > 0 {
		// Рейтинг автора — хранимый rating_score (среднее лучших работ, #296), как
		// и сортировка; личные скрытия в него не входят.
		n := w.add(p.MinRating)
		where = append(where, fmt.Sprintf("a.rating_score >= $%d", n))
	}

	if p.MinReaderRating > 0 {
		n := w.add(p.MinReaderRating)
		where = append(where, fmt.Sprintf(
			"(SELECT avg(br.rating) FROM book_ratings br WHERE br.work_id IN ("+
				"SELECT b.work_id FROM book_authors ba JOIN books b ON b.id = ba.book_id"+
				" WHERE ba.author_id = a.id AND b.deleted = false AND b.work_id IS NOT NULL"+w.aggExclusion()+")) >= $%d", n))
	}
	return where
}
