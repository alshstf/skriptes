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
