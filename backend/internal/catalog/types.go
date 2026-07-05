// Package catalog содержит read-only сервисы для авторов и серий.
// Книги — в internal/books (там Meilisearch); здесь упор на связи и
// агрегации, которые быстрее берутся из Postgres.
package catalog

import "github.com/skriptes/skriptes/backend/internal/books"

// Author — детальная карточка автора (GET /api/authors/:id).
type Author struct {
	ID         int64  `json:"id"`
	LastName   string `json:"last_name"`
	FirstName  string `json:"first_name,omitempty"`
	MiddleName string `json:"middle_name,omitempty"`
	FullName   string `json:"full_name"`

	// Био и фото из metadata-enrichment (Wikipedia/OL). Заполняются
	// лениво при первом GET /api/authors/{id}, как и поля у Book.
	Bio       string `json:"bio,omitempty"`
	PhotoPath string `json:"photo_path,omitempty"`
	// EnrichmentFetched — была ли хотя бы одна попытка обогащения.
	// Фронт использует чтобы превратить вечный скелетон в fallback
	// "Описание отсутствует" по тому же принципу, что у книг.
	EnrichmentFetched bool `json:"enrichment_fetched,omitempty"`

	BookCount  int               `json:"book_count"`
	TopGenres  []GenreCount      `json:"top_genres,omitempty"`
	Series     []SeriesWithCount `json:"series,omitempty"`
	BooksTotal int               `json:"books_total"`
	Books      []books.ListItem  `json:"books"`

	// Агрегаты автора (зеркало строки в списке /authors) — чтобы карточка
	// показывала то же, что компактный список: единый ВНЕШНИЙ рейтинг +
	// источник топ-издания, оценка читателей + число, наличие экранизаций,
	// языки изданий (lang∪src_lang), годы активности (по written_year).
	ExternalRating       *float64    `json:"external_rating,omitempty"`
	ExternalRatingSource *string     `json:"external_rating_source,omitempty"`
	ReaderRating         *float64    `json:"reader_rating,omitempty"`
	ReaderRatingCount    int         `json:"reader_rating_count,omitempty"`
	HasAdaptations       bool        `json:"has_adaptations,omitempty"`
	Languages            []string    `json:"languages,omitempty"`
	YearsActive          *YearsRange `json:"years_active,omitempty"`

	// YearStats — распределение книг автора по году НАПИСАНИЯ (written_year).
	// Используется для гистограммы на странице автора (recharts).
	// Сортировка по году по возрастанию; книги без written_year не попадают.
	YearStats []YearCount `json:"year_stats,omitempty"`

	// ReadCount — сколько книг автора есть в reads текущего пользователя
	// (read = "скачана хотя бы раз" до сборки in-browser reader'а).
	// Заполняется только если в запрос пробрасывается user (см. GetAuthor),
	// иначе 0 и фронт скрывает прогресс-блок.
	ReadCount int `json:"read_count,omitempty"`

	// YearEnrichmentPending — этот запрос инициировал ленивое дозаполнение года
	// хотя бы для одной книги (порядок в серии мог «упасть» на фолбэк). Фронт
	// поллит карточку, пока true, и переставляет порядок по мере наполнения.
	YearEnrichmentPending bool `json:"year_enrichment_pending,omitempty"`

	// BookRefs — служебное (не сериализуется): данные книг карточки для ленивого
	// триггера года (api-слой строит по ним BookQuery).
	BookRefs []BookYearRef `json:"-"`
}

// BookYearRef — минимум для ленивого триггера года: идентичность fb2 + признаки
// «год уже искали». Возвращается рядом со списком книг карточки, чтобы не делать
// повторный запрос. Title/Lang/Authors берём из самих books.ListItem.
type BookYearRef struct {
	BookID         int64
	Archive        string // archives.filename — для BookQuery.ArchivePath
	FileName       string
	Ext            string
	HasWrittenYear bool
	LocalScanned   bool // year_local_scanned_at IS NOT NULL
}

// YearCount — год + число книг этого года + список книг (для тултипа
// гистограммы: при наведении на столбик показываем, ЧТО за книги).
type YearCount struct {
	Year  int        `json:"year"`
	Count int        `json:"count"`
	Books []YearBook `json:"books,omitempty"`
}

// YearBook — компактная ссылка на книгу для тултипа гистограммы.
type YearBook struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// GenreCount — пара (genre, books_in_this_genre_for_this_author).
type GenreCount struct {
	Code    string `json:"code"`
	Display string `json:"display"`
	Count   int    `json:"count"`
}

// SeriesWithCount — серия + сколько книг этого автора в ней.
// AllCompilations — серия целиком из сборников/антологий/томов собраний
// (works.kind ≠ NULL у всех работ, серия-паразит вроде «Шекли. Сборники»):
// фронт уводит её из списка серий автора в секцию «Сборники и антологии».
type SeriesWithCount struct {
	ID              int64  `json:"id"`
	Title           string `json:"title"`
	Count           int    `json:"count"`
	AllCompilations bool   `json:"all_compilations,omitempty"`
}

// AuthorSuggest — строка в typeahead-выдаче авторов.
// IsFavorite — заполняется api-handler'ом, не сервисом catalog
// (он не знает про user-сессию).
type AuthorSuggest struct {
	ID         int64  `json:"id"`
	FullName   string `json:"full_name"`
	BookCount  int    `json:"book_count"`
	IsFavorite bool   `json:"is_favorite,omitempty"`
}

// SeriesSuggest — строка в typeahead-выдаче серий.
// AuthorName заполнено только если у серии один автор (привязан в схеме).
type SeriesSuggest struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	AuthorName string `json:"author_name,omitempty"`
	BookCount  int    `json:"book_count"`
	IsFavorite bool   `json:"is_favorite,omitempty"`
}

// Series — детальная карточка серии (GET /api/series/:id).
// SeriesAuthorRef — автор книг серии. Серия может содержать книги НЕСКОЛЬКИХ авторов
// (со-авторство или ручной перенос книги в чужую серию) — шапка показывает всех, а не
// только `series.author_id`.
type SeriesAuthorRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type Series struct {
	ID         int64             `json:"id"`
	Title      string            `json:"title"`
	AuthorID   *int64            `json:"author_id,omitempty"`
	AuthorName string            `json:"author_name,omitempty"`
	Authors    []SeriesAuthorRef `json:"authors,omitempty"` // все авторы книг серии (≥1)
	BookCount  int               `json:"book_count"`
	Books      []books.ListItem  `json:"books"` // отсортированы по ser_no, deleted скрыты

	// Аналогично Author: гистограмма по годам написания и прогресс чтения.
	YearStats []YearCount `json:"year_stats,omitempty"`
	ReadCount int         `json:"read_count,omitempty"`

	// YearEnrichmentPending — см. одноимённое поле у Author.
	YearEnrichmentPending bool `json:"year_enrichment_pending,omitempty"`

	// BookRefs — служебное (не сериализуется): см. Author.BookRefs.
	BookRefs []BookYearRef `json:"-"`
}
