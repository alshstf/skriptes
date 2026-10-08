// Package books — read-side сервис каталога.
// Список и поиск идут через Meilisearch (быстрый typeahead с typo tolerance);
// карточка одной книги собирается из Postgres (нужны связи с авторами,
// серией и жанрами).
package books

import (
	"fmt"
	"strings"
	"time"
)

// AuthorRef — компактная ссылка на автора в карточке книги или в списке.
type AuthorRef struct {
	ID         int64  `json:"id"`
	LastName   string `json:"last_name"`
	FirstName  string `json:"first_name,omitempty"`
	MiddleName string `json:"middle_name,omitempty"`
	FullName   string `json:"full_name"`
	// Note — уточнение, отличающее тёзок («Блум», «фантаст»); см. DisplayNote.
	Note string `json:"note,omitempty"`
}

// SeriesRef — компактная ссылка на серию.
type SeriesRef struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// GenreRef — для отображения чипов жанров.
type GenreRef struct {
	ID      int64  `json:"id"`
	Code    string `json:"code"`
	NameRu  string `json:"name_ru,omitempty"`
	NameEn  string `json:"name_en,omitempty"`
	Display string `json:"display"` // лучший доступный display name
}

// ListItem — строка в /api/books (приходит из Meilisearch).
//
// AuthorIDs / SeriesID — нужны для двух вещей:
//   - персонализированный re-ranking (см. internal/history.PersonaProfile);
//   - clickable-имена в списке книг на фронте (без отдельного запроса).
type ListItem struct {
	ID        int64    `json:"id"`
	Title     string   `json:"title"`
	Authors   []string `json:"authors"`
	AuthorIDs []int64  `json:"author_ids,omitempty"`
	Series    string   `json:"series,omitempty"`
	SeriesID  *int64   `json:"series_id,omitempty"`
	// SerNo — номер книги в серии (если есть). Используется фронтом для
	// группировки и сортировки внутри серии на странице автора.
	SerNo *int `json:"ser_no,omitempty"`
	// SeriesOrder — 0-based позиция книги ВНУТРИ своей серии после backend-каскада
	// сортировки (ser_no → written_year → edition_year → эвристика названия →
	// date_added). Считается в catalog, чтобы фронт сортировал группу серии одним
	// ключом. nil для книг вне серии (и в /books-листинге — там не вычисляется).
	SeriesOrder *int     `json:"series_order,omitempty"`
	Genres      []string `json:"genres,omitempty"`
	Year        *int     `json:"year,omitempty"`
	Lang        string   `json:"lang,omitempty"`
	LibID       string   `json:"lib_id"`
	// CoverPath — относительный путь до обложки (если уже обогащена).
	// В Meili-индексе его нет (обложки проставляются лениво после
	// индексации), поэтому List догидрачивает его из Postgres по id
	// текущей страницы. Пусто, если обложка ещё не скачана — фронт
	// тогда показывает placeholder.
	CoverPath string `json:"cover_path,omitempty"`
	// IsFavorite — user-specific флаг "книга в избранном текущего
	// пользователя". Заполняется не в books-сервисе (он user-agnostic),
	// а в api-handler'ах, которые знают про сессию.
	IsFavorite bool `json:"is_favorite,omitempty"`
	// EditionCount — сколько изданий (fb2-файлов) у этой логической книги.
	// >1 → фронт показывает бейдж «N изданий». Догидрачивается из PG.
	EditionCount int `json:"edition_count,omitempty"`
	// CoverEditionID — id издания, из которого брать обложку (для works-выдачи:
	// ListItem.ID = works.id, а on-demand-обложка /api/covers/book/{id} требует
	// id ИЗДАНИЯ). Догидрачивается из PG (издание с обложкой в приоритете).
	CoverEditionID int64 `json:"cover_edition_id,omitempty"`
	// WorkID — логическая книга (works.id). Ссылки на карточку ведут на
	// /works/{work_id}. В works-выдаче (ListWorks) ID и так = works.id, поле не
	// заполняется; в catalog-выдаче (автор/серия) ID = представительное ИЗДАНИЕ,
	// поэтому WorkID несёт id работы для ссылки. Фронт: /works/{work_id ?? id}.
	WorkID int64 `json:"work_id,omitempty"`
	// Kind — тип работы (works.kind): "" — обычное произведение, иначе
	// collection (авторский сборник) | anthology | omnibus (том собрания
	// сочинений). Карточка автора выносит kind≠"" в секцию «Сборники и
	// антологии». Заполняется catalog-выдачей (автор/серия).
	Kind string `json:"kind,omitempty"`

	// Сигналы для обогащённой плашки (как строка автора). Не-user поля
	// (ExternalRating/Source, ReaderRating/Count, HasAdaptations) гидрируются
	// HydrateListMeta по work_id; user-поля (IsRead/ReadingFraction; IsFavorite
	// выше) — в api-слое (есть history+userID). Внешний рейтинг = max(COALESCE(
	// rating, external_rating)) по изданиям работы + источник топ-издания.
	ExternalRating       *float64 `json:"external_rating,omitempty"`
	ExternalRatingSource *string  `json:"external_rating_source,omitempty"`
	ReaderRating         *float64 `json:"reader_rating,omitempty"`
	ReaderRatingCount    int      `json:"reader_rating_count,omitempty"`
	HasAdaptations       bool     `json:"has_adaptations,omitempty"`
	IsRead               bool     `json:"is_read,omitempty"`
	ReadingFraction      *float64 `json:"reading_fraction,omitempty"`
}

// ListResponse — обёртка для GET /api/books.
type ListResponse struct {
	Items       []ListItem `json:"items"`
	Total       int64      `json:"total"`
	Limit       int        `json:"limit"`
	Offset      int        `json:"offset"`
	Query       string     `json:"query,omitempty"`
	ProcessTime int64      `json:"processing_ms"` // время обработки в Meili
	// Facets — распределения по запрошенным facetable атрибутам.
	// Ключ внешней мапы — имя атрибута (genres, lang, year),
	// внутренней — значение и сколько книг ему соответствует.
	// Пустая мапа если facets не запросили — экономит трафик.
	Facets map[string]map[string]int64 `json:"facets,omitempty"`
	// MatchedAuthors — известные авторы, которых называет запрос (#290): их
	// работы идут в выдаче первыми, над выдачей — плашка со ссылкой на автора.
	// Только на первой странице.
	MatchedAuthors []MatchedAuthor `json:"matched_authors,omitempty"`
}

// Book — детальная карточка из PG (GET /api/books/:id).
type Book struct {
	ID      int64       `json:"id"`
	LibID   string      `json:"lib_id"`
	Title   string      `json:"title"`
	Authors []AuthorRef `json:"authors"`
	Series  *SeriesRef  `json:"series,omitempty"`
	SerNo   *int        `json:"ser_no,omitempty"`
	Genres  []GenreRef  `json:"genres"`
	Lang    string      `json:"lang,omitempty"`
	// SrcLang — язык оригинала переводной книги (fb2 <src-lang>): открытого
	// издания, иначе любого издания работы (переводы одной работы делят
	// оригинал). Пусто = оригинал/неизвестен (в fb2 поле разрежённое).
	SrcLang   string     `json:"src_lang,omitempty"`
	DateAdded *time.Time `json:"date_added,omitempty"`
	// WrittenYear — год написания / первого издания произведения
	// (fb2 <title-info><date> → внешние источники). EditionYear — год
	// конкретного бумажного издания этого fb2 (<publish-info><year>).
	// Это разные сущности: WrittenYear идёт в статистику, EditionYear —
	// справочное поле. Оба nil, если год недоступен.
	WrittenYear *int `json:"written_year,omitempty"`
	EditionYear *int `json:"edition_year,omitempty"`
	Rating      *int `json:"rating,omitempty"`
	// Внешний рейтинг из сети (Google Books/OpenLibrary), ОТДЕЛЬНО от Rating
	// (LIBRATE из INPX). На UI оба объединяются в единый «Внешний рейтинг» с
	// приоритетом LIBRATE → web; Source — источник web-рейтинга
	// ('google_books'|'openlibrary'), Count — число голосов у источника.
	// Заполняется фоновым воркером обогащения (nil, пока не обогащено).
	ExternalRating       *float64 `json:"external_rating,omitempty"`
	ExternalRatingSource *string  `json:"external_rating_source,omitempty"`
	ExternalRatingCount  *int     `json:"external_rating_count,omitempty"`
	// FantlabRating — средняя оценка работы на fantlab.ru (1–10) и число оценок;
	// только от 10 оценок (#296; заполняет воркер «Известность»).
	FantlabRating *float64 `json:"fantlab_rating,omitempty"`
	FantlabMarks  int      `json:"fantlab_marks,omitempty"`
	Annotation    string   `json:"annotation,omitempty"`
	CoverPath     string   `json:"cover_path,omitempty"`
	Archive       string   `json:"archive"`
	FileName      string   `json:"file_name"`
	Ext           string   `json:"ext"`
	SizeBytes     int64    `json:"size_bytes"`
	Deleted       bool     `json:"deleted,omitempty"`
	// WorkID — логическая книга. Editions — ВСЕ издания этой работы (включая
	// открытое). Title/WrittenYear/Series/SerNo/Authors/Genres — уровня работы
	// (union по изданиям); остальные поля выше — открытого издания (id в URL),
	// для обратной совместимости и кнопок скачать/читать конкретного издания.
	WorkID   int64        `json:"work_id,omitempty"`
	Editions []EditionRef `json:"editions,omitempty"`
}

// EditionRef — одно физическое издание (fb2-файл) логической книги. Для секции
// «Издания» на карточке: атрибуты + ссылки на скачивание/чтение по id.
type EditionRef struct {
	ID int64 `json:"id"`
	// Title/Series — СОБСТВЕННЫЕ название и серия этого издания (из books.title /
	// books.series_id). Обычно совпадают с work-level, но при ошибочном слиянии
	// «чужое» издание видно по отличному названию/серии — нужно для осознанного
	// split (какое издание вынести). Work-level title/series — в самом Book.
	Title        string     `json:"title,omitempty"`
	Series       *SeriesRef `json:"series,omitempty"`
	Lang         string     `json:"lang,omitempty"`
	Translator   string     `json:"translator,omitempty"`
	EditionYear  *int       `json:"edition_year,omitempty"`
	Publisher    string     `json:"publisher,omitempty"`
	ISBN         string     `json:"isbn,omitempty"`
	EditionTitle string     `json:"edition_title,omitempty"`
	PageCount    *int       `json:"page_count,omitempty"`
	CoverPath    string     `json:"cover_path,omitempty"`
	SizeBytes    int64      `json:"size_bytes"`
	Ext          string     `json:"ext"`
	Archive      string     `json:"archive"`
	FileName     string     `json:"file_name"`
	Deleted      bool       `json:"deleted,omitempty"`
	// IsAnchor — «якорное» издание работы (title-derived: его normalized_title
	// совпадает с названием работы; тай → min id; fallback → min id). Якорь
	// определяет идентичность работы и НЕ выносится через split (фронт его лочит,
	// бэкенд запрещает). Ровно одно издание работы — якорь.
	IsAnchor bool `json:"is_anchor,omitempty"`
	// User-specific (заполняет API-слой, не books-сервис): прогресс чтения и
	// флаг «прочитано» ИМЕННО этого издания (позиция/CFI привязаны к файлу).
	ReadingFraction *float64 `json:"reading_fraction,omitempty"`
	IsRead          bool     `json:"is_read,omitempty"`
}

// ListParams — нормализованные параметры запроса /api/books.
// Все фильтры опциональны; пустые значения означают "не фильтровать
// по этому атрибуту". Sort:
//   - "year_desc" / "year_asc"   — по году издания
//   - "popularity"               — по числу просмотров (popularity:desc)
//   - "title"                    — по нормализованному названию
//   - "" (пустое)                — ранжирование по правилам Meili (с typo/relevance).
type ListParams struct {
	Query   string
	Limit   int
	Offset  int
	Genres  []string // OR-семантика: книга подходит, если у неё есть ХОТЯ БЫ один из жанров
	Lang    string
	SrcLang string // язык ОРИГИНАЛА (fb2 src-lang); фасет ТОЛЬКО works-индекса — веб-список (books/OPDS его не индексирует)
	// Kind — тип работы (works.kind, только works-индекс): "book" — обычные
	// книги (не сборники), "collection" | "anthology" | "omnibus" — этот тип.
	// Пусто или неизвестное значение — без фильтра.
	Kind     string
	YearFrom int
	YearTo   int
	SeriesID int64
	AuthorID int64
	Sort     string
	Facets   []string // запрашиваемые распределения; например ["genres","lang","year"]
	// ExcludeWorkIDs — работы, которых не должно быть в выдаче (works-индекс):
	// «Только непрочитанные» — прочитанные пользователем (умные полки, #389).
	ExcludeWorkIDs []int64

	// ExcludeGenres / ExcludeLangs — скрытые из выдачи жанры/языки
	// (объединение глобальных admin-настроек и персональных настроек
	// пользователя, см. internal/settings.ContentResolver). Применяются как
	// `genres NOT IN [...]` / `lang NOT IN [...]` — книга с любым скрытым
	// жанром или скрытым языком не попадает в список/поиск/фасеты.
	ExcludeGenres []string
	ExcludeLangs  []string
	// ExcludeCompilations — скрыть сборники/антологии/тома собраний (opt-in
	// профильная настройка): works-фильтр `kind NOT IN [...]`. Применяется
	// только в works-путях (веб) — books-индекс (OPDS) поля kind не несёт.
	ExcludeCompilations bool

	// UserID — если >0 и не задан Sort/AuthorID/SeriesID, выдача пере-
	// сортировывается персонализированным re-ranking'ом (см. List).
	// Пагинация: re-rank применяется ТОЛЬКО к первой странице (offset==0),
	// чтобы не путать пользователя при листании.
	UserID int64
}

// MinExternalRatingVotes — веб-оценка (Google Books / OpenLibrary) учитывается,
// только если за ней столько голосов (#296): 53 % веб-оценок — один голос, и у
// 540 авторов «5.0» держалось на одном-двух голосах. LIBRATE (оценка донорской
// библиотеки) — как есть: числа голосов у неё нет.
const MinExternalRatingVotes = 5

// ExternalRatingSQL — <alias>.external_rating с порогом голосов: ниже порога NULL.
func ExternalRatingSQL(alias string) string {
	return fmt.Sprintf("(CASE WHEN %[1]s.external_rating_count >= %[2]d THEN %[1]s.external_rating END)", alias, MinExternalRatingVotes)
}

// MinFantlabMarks — средняя оценка Фантлаба показывается от стольких оценок
// (#296): у работ с парой оценок средняя случайна. От того же числа оценок
// Фантлаб входит в рейтинг автора (#394).
const MinFantlabMarks = 10

// fantlabScale — рейтинг Фантлаба (works.fantlab_rating, сглаженный самим
// Фантлабом, 0–10) в шкале LIBRATE (1–5) для рейтинга автора (#394).
// Квантильное соответствие на 12 004 работах с обеими оценками (прод
// 2026-10-06): доля работ с рейтингом Фантлаба ниже точки = доля LIBRATE ниже
// значения (у LIBRATE 1 — 3,9 %, 2 — 5,9 %, 3 — 26,1 %, 4 — 35,2 %, 5 — 28,9 %;
// класс k занимает отрезок [k−0,5; k+0,5]). Между точками — линейно, вне — края.
// Простое деление на 2 занижало бы Фантлаб: «Мастер и Маргарита» 8,97 → 4,5
// против любой «пятёрки» LIBRATE. Связь шкал слабая (корреляция 0,35), но
// монотонная: средняя LIBRATE растёт с рейтингом Фантлаба от 3,5 до 4,4.
var fantlabScale = []struct{ fantlab, librate float64 }{
	{3.10, 1.0}, {3.43, 1.5}, {3.96, 2.5}, {5.61, 3.5}, {7.16, 4.5}, {7.68, 5.0},
}

// FantlabOnLibrateScale переводит рейтинг Фантлаба в шкалу LIBRATE (см. fantlabScale).
func FantlabOnLibrateScale(r float64) float64 {
	if r <= fantlabScale[0].fantlab {
		return fantlabScale[0].librate
	}
	for i := 1; i < len(fantlabScale); i++ {
		a, b := fantlabScale[i-1], fantlabScale[i]
		if r <= b.fantlab {
			return a.librate + (r-a.fantlab)*(b.librate-a.librate)/(b.fantlab-a.fantlab)
		}
	}
	return fantlabScale[len(fantlabScale)-1].librate
}

// FantlabOnLibrateScaleSQL — то же в SQL для работы <alias> (works): NULL, если
// оценок Фантлаба меньше MinFantlabMarks.
func FantlabOnLibrateScaleSQL(alias string) string {
	r := alias + ".fantlab_rating"
	var b strings.Builder
	fmt.Fprintf(&b, "(CASE WHEN %s.fantlab_marks >= %d AND %s IS NOT NULL THEN CASE", alias, MinFantlabMarks, r)
	fmt.Fprintf(&b, " WHEN %s <= %g THEN %g", r, fantlabScale[0].fantlab, fantlabScale[0].librate)
	for i := 1; i < len(fantlabScale); i++ {
		a, c := fantlabScale[i-1], fantlabScale[i]
		fmt.Fprintf(&b, " WHEN %s <= %g THEN %g + (%s - %g) * %g", r, c.fantlab, a.librate, r, a.fantlab,
			(c.librate-a.librate)/(c.fantlab-a.fantlab))
	}
	fmt.Fprintf(&b, " ELSE %g END END)", fantlabScale[len(fantlabScale)-1].librate)
	return b.String()
}
