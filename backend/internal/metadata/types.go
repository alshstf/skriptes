package metadata

import (
	"context"
	"errors"
	"io"
)

// ErrNotFound — провайдер не нашёл данных для книги; не считается
// фатальной ошибкой, оркестратор просто пробует следующий.
var ErrNotFound = errors.New("metadata not found")

// BookQuery — что мы ищем. Передаётся в провайдеры; конкретный
// набор полей зависит от того, что нужно искать.
//
// ArchivePath / FB2Name заполняются handler'ом для fb2-провайдера
// (он один умеет лезть в наш zip). Внешним провайдерам не нужны.
type BookQuery struct {
	ID      int64
	Title   string
	Authors []string // полные имена в виде "Фамилия Имя Отчество"
	Lang    string   // ISO-код (ru/en/...) — помогает выбрать локаль API

	ArchivePath string // абсолютный путь к zip с книгой
	FB2Name     string // имя файла внутри zip (например "12345.fb2")
}

// CoverImage — сырая обложка для записи в /cache/covers.
// Reader живёт пока caller не вызовет Close; Mime — для проверки и
// выбора расширения файла; SourceID — описание источника (для логов
// и для записи в ext_ids книги: "ol:OL12345W" / "gb:abcdef" / "fb2").
type CoverImage struct {
	Reader   io.ReadCloser
	Mime     string
	SourceID string
}

// CoverProvider — поставщик одной обложки. Реализуется fb2/OL/GB.
//
// Возвращает ErrNotFound если для данного запроса ничего нет.
// Все остальные ошибки — серьёзные (сеть, неожиданный формат);
// Enricher логирует их и идёт дальше по цепочке.
type CoverProvider interface {
	Name() string
	FetchCover(ctx context.Context, q BookQuery) (*CoverImage, error)
}

// AnnotationProvider — поставщик аннотации (описания) книги.
// Возвращает plain-text с сохранёнными переводами строк (\n\n между
// параграфами), без HTML-тегов — фронт рендерит как whitespace-pre-wrap
// без риска XSS.
//
// Контракт ErrNotFound идентичен CoverProvider.
type AnnotationProvider interface {
	Name() string
	FetchAnnotation(ctx context.Context, q BookQuery) (string, error)
}

// AuthorQuery — то же что BookQuery, но для авторов. Wiki-провайдеры
// ищут по полному имени; Lang — какую языковую Wikipedia пробовать
// первой (ru / en).
type AuthorQuery struct {
	ID         int64
	LastName   string
	FirstName  string
	MiddleName string
	FullName   string // готовая склейка "Фамилия Имя Отчество"
	Lang       string // ISO-код страны/языка автора, может быть пустой
}

// AuthorPhotoProvider — поставщик портрета автора. Reuse CoverImage —
// формат тот же (Reader + Mime + SourceID), кэш в /cache/covers тоже общий.
type AuthorPhotoProvider interface {
	Name() string
	FetchAuthorPhoto(ctx context.Context, q AuthorQuery) (*CoverImage, error)
}

// AuthorBioProvider — поставщик био-текста автора. Контракт plain-text
// с переводами строк, как у AnnotationProvider.
type AuthorBioProvider interface {
	Name() string
	FetchAuthorBio(ctx context.Context, q AuthorQuery) (string, error)
}

// Adaptation — одна экранизация книги (фильм/сериал). Возвращается
// AdaptationProvider'ом из внешнего источника (Wikidata, TMDB) ДО
// сохранения в БД. Enricher.EnsureAdaptations downloads PosterURL
// в /cache/covers и пишет результат в таблицу book_adaptations.
//
// ExtID — идентификатор в провайдере (QID для wikidata, tt-id для
// imdb, числовой id для tmdb). Вместе с Provider даёт уникальный ключ.
//
// Kind — нормализованный тип: "film" | "tv_series" | "miniseries" |
// "anime" | "other". Маппинг с разнородных P31-значений Wikidata в
// этот узкий набор делает провайдер; фронт показывает badge.
//
// PosterURL — внешний URL картинки (commons.wikimedia.org или image.tmdb.org).
// Может быть пустой — фронт покажет плейсхолдер.
//
// ExtURL — канонический URL для "Открыть в источнике". Провайдер
// выбирает по приоритету Кинопоиск → IMDb → Wikidata (Wikidata —
// fallback, статьи не предназначены для конечных пользователей).
//
// Popularity — целое число, прокси известности фильма. Для Wikidata —
// wikibase:sitelinks (сколько языковых Wikipedia ссылаются на статью).
// 0 для неизвестных; Service.List использует как primary sort
// (DESC NULLS LAST), tiebreaker — Year DESC.
type Adaptation struct {
	Provider   string // "wikidata" | "tmdb"
	ExtID      string
	Title      string
	Year       *int // nil если неизвестен
	Director   string
	Kind       string // нормализованное значение, см. doc выше
	PosterURL  string
	ExtURL     string
	Popularity int
}

// AdaptationProvider — поставщик списка экранизаций для книги. В
// отличие от Cover/Annotation провайдеров возвращает СРЕЗ (одна книга
// → много экранизаций) и пустой срез без ошибки — это валидный
// результат "книга найдена, но экранизаций нет".
//
// ErrNotFound — книгу не удалось сопоставить с записью в источнике.
type AdaptationProvider interface {
	Name() string
	FetchAdaptations(ctx context.Context, q BookQuery) ([]Adaptation, error)
}

// LocalYearSource — локальный (без сети) поставщик года из fb2.
// Возвращает год написания произведения (<title-info><date>) и год
// бумажного издания (<publish-info><year>); 0 — если поле отсутствует
// или непарсимо. Реализуется Fb2Provider; используется фоновым прогревом
// (Enricher.EnsureYearLocal) для заполнения books.written_year /
// edition_year. Внешние источники года — отдельная цепочка (отдельный PR).
type LocalYearSource interface {
	FetchYears(ctx context.Context, q BookQuery) (written int, edition int, err error)
}

// EditionMeta — атрибуты уровня ИЗДАНИЯ, извлечённые из заголовка fb2
// (см. Fb2Provider.FetchEditionMeta). Пустые поля = их в fb2 нет.
// SrcAuthor — display-форма ("Фамилия Имя"); нормализованный ключ
// (src_author_normalized) считается при записи через normalizePersonKey.
type EditionMeta struct {
	Translator   string // переводчик (первый), display-форма
	ISBN         string // нормализован (uppercase, [0-9X], len 10/13) или ""
	Publisher    string
	EditionTitle string // <publish-info><book-name>
	EditionYear  int    // <publish-info><year>, 0 — нет
	SrcLang      string // язык оригинала (<title-info><src-lang> / <src-title-info><lang>)
	SrcTitle     string // оригинальное название (<src-title-info><book-title>)
	SrcAuthor    string // первый <src-title-info><author>, display-форма
	TitleLang    string // <title-info><lang>
	FB2DocID     string // <document-info><id>
}

// LocalEditionSource — локальный (без сети) поставщик атрибутов издания из
// fb2. Реализуется Fb2Provider; используется фоновым прогревом
// (Enricher.EnsureEditionMeta) для заполнения edition-полей books.
type LocalEditionSource interface {
	FetchEditionMeta(ctx context.Context, q BookQuery) (EditionMeta, error)
}

// WorkQuery — запрос на резолв ВНЕШНЕГО идентификатора работы (Tier-2
// группировки). Для переводов выгоднее искать по оригинальному названию/языку
// (SrcTitle/SrcLang), а ISBN — самый точный ключ (резолвится без гейта).
// LastName/FirstName нужны для гейта authorNameMatches при поиске по названию.
type WorkQuery struct {
	BookID    int64
	Title     string
	SrcTitle  string
	ISBN      string
	Lang      string
	Authors   []string // display-имена авторов книги
	LastName  string   // primary-автор, для precision-гейта
	FirstName string
	// WikidataQID — уже известный QID работы (из works.ext_ids, если Tier-2
	// группировки его резолвил) — позволяет источнику wikidata пропустить
	// дорогой резолв по названию. Пустой — резолвим сами.
	WikidataQID string
}

// WorkKeyResolver — внешний источник идентификатора работы (OpenLibrary Work /
// Wikidata QID). Книги, у которых ОДИНАКОВЫЙ (Name(), work_key), сливаются в
// одну логическую книгу. Возвращает ключ работы (без префикса источника) либо
// ErrNotFound. Name() = строка source в book_work_lookups ("openlibrary"|"wikidata").
type WorkKeyResolver interface {
	Name() string
	ResolveWorkKey(ctx context.Context, q WorkQuery) (string, error)
}

// YearProvider — внешний источник года первого издания/написания для
// дозаполнения written_year (когда из fb2 год не извлёкся). Возвращает год
// (>0) либо ErrNotFound, если источник книгу/год не нашёл; прочие ошибки —
// сетевые/HTTP, воркер их логирует и помечает source как error (ретрай по TTL).
// Name() должен совпадать со строкой source в book_year_lookups /
// written_year_source ("openlibrary" | "wikidata").
type YearProvider interface {
	Name() string
	FetchYear(ctx context.Context, q BookQuery) (int, error)
}

// RatingResult — внешний рейтинг произведения: средняя (шкала 1–5) + число
// голосов у источника. Count помогает выбрать более надёжный источник, когда
// их несколько (больше голосов — выше доверие).
type RatingResult struct {
	Average float64
	Count   int
}

// RatingProvider — внешний источник рейтинга книги (Google Books / OpenLibrary)
// для дозаполнения books.external_rating. Возвращает RatingResult (Average > 0)
// либо ErrNotFound, если рейтинга там нет; прочие ошибки — сетевые/HTTP, воркер
// их логирует и помечает source как error (ретрай по TTL). Name() совпадает со
// строкой source в book_external_rating_lookups / external_rating_source
// ("googlebooks" | "openlibrary").
type RatingProvider interface {
	Name() string
	FetchRating(ctx context.Context, q WorkQuery) (RatingResult, error)
}

// RenownResult — счётчики «известности» работы у внешнего источника (сигналы
// интегральной популярности, не рейтинг): Ratings — число оценок (Fantlab
// markcount / OL ratings_count), Want — размер полки want-to-read (только OL),
// Sitelinks — число языковых разделов Википедии со статьёй (только Wikidata).
type RenownResult struct {
	Ratings   int
	Want      int
	Sitelinks int
}

// total — суммарный сигнал (для проверки «источник что-то нашёл»).
func (r RenownResult) total() int { return r.Ratings + r.Want + r.Sitelinks }

// RenownProvider — внешний источник счётчиков известности работы (Fantlab /
// OpenLibrary) для дозаполнения works.fantlab_marks / ol_*_count. Возвращает
// RenownResult (Ratings+Want > 0) либо ErrNotFound; прочие ошибки — сетевые/
// HTTP (воркер помечает source как error, ретрай по TTL). Name() совпадает со
// строкой source в work_renown_lookups ("fantlab" | "openlibrary").
type RenownProvider interface {
	Name() string
	FetchRenown(ctx context.Context, q WorkQuery) (RenownResult, error)
}
