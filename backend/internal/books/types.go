// Package books — read-side сервис каталога.
// Список и поиск идут через Meilisearch (быстрый typeahead с typo tolerance);
// карточка одной книги собирается из Postgres (нужны связи с авторами,
// серией и жанрами).
package books

import "time"

// AuthorRef — компактная ссылка на автора в карточке книги или в списке.
type AuthorRef struct {
	ID         int64  `json:"id"`
	LastName   string `json:"last_name"`
	FirstName  string `json:"first_name,omitempty"`
	MiddleName string `json:"middle_name,omitempty"`
	FullName   string `json:"full_name"`
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
}

// Book — детальная карточка из PG (GET /api/books/:id).
type Book struct {
	ID        int64       `json:"id"`
	LibID     string      `json:"lib_id"`
	Title     string      `json:"title"`
	Authors   []AuthorRef `json:"authors"`
	Series    *SeriesRef  `json:"series,omitempty"`
	SerNo     *int        `json:"ser_no,omitempty"`
	Genres    []GenreRef  `json:"genres"`
	Lang      string      `json:"lang,omitempty"`
	DateAdded *time.Time  `json:"date_added,omitempty"`
	// WrittenYear — год написания / первого издания произведения
	// (fb2 <title-info><date> → внешние источники). EditionYear — год
	// конкретного бумажного издания этого fb2 (<publish-info><year>).
	// Это разные сущности: WrittenYear идёт в статистику, EditionYear —
	// справочное поле. Оба nil, если год недоступен.
	WrittenYear *int   `json:"written_year,omitempty"`
	EditionYear *int   `json:"edition_year,omitempty"`
	Rating      *int   `json:"rating,omitempty"`
	Annotation  string `json:"annotation,omitempty"`
	CoverPath   string `json:"cover_path,omitempty"`
	Archive     string `json:"archive"`
	FileName    string `json:"file_name"`
	Ext         string `json:"ext"`
	SizeBytes   int64  `json:"size_bytes"`
	Deleted     bool   `json:"deleted,omitempty"`
}

// ListParams — нормализованные параметры запроса /api/books.
// Все фильтры опциональны; пустые значения означают "не фильтровать
// по этому атрибуту". Sort:
//   - "year_desc" / "year_asc"   — по году издания
//   - "popularity"               — по числу просмотров (popularity:desc)
//   - "title"                    — по нормализованному названию
//   - "" (пустое)                — ранжирование по правилам Meili (с typo/relevance).
type ListParams struct {
	Query    string
	Limit    int
	Offset   int
	Genres   []string // OR-семантика: книга подходит, если у неё есть ХОТЯ БЫ один из жанров
	Lang     string
	YearFrom int
	YearTo   int
	SeriesID int64
	AuthorID int64
	Sort     string
	Facets   []string // запрашиваемые распределения; например ["genres","lang","year"]

	// ExcludeGenres / ExcludeLangs — скрытые из выдачи жанры/языки
	// (объединение глобальных admin-настроек и персональных настроек
	// пользователя, см. internal/settings.ContentResolver). Применяются как
	// `genres NOT IN [...]` / `lang NOT IN [...]` — книга с любым скрытым
	// жанром или скрытым языком не попадает в список/поиск/фасеты.
	ExcludeGenres []string
	ExcludeLangs  []string

	// UserID — если >0 и не задан Sort/AuthorID/SeriesID, выдача пере-
	// сортировывается персонализированным re-ranking'ом (см. List).
	// Пагинация: re-rank применяется ТОЛЬКО к первой странице (offset==0),
	// чтобы не путать пользователя при листании.
	UserID int64
}
