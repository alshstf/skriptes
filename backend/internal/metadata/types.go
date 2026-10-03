package metadata

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// ErrNotFound — провайдер не нашёл данных для книги; не считается
// фатальной ошибкой, оркестратор просто пробует следующий.
var ErrNotFound = errors.New("metadata not found")

// ErrUpstream — транзиентная/операционная ошибка внешнего источника (429 rate
// limit, 400/403 невалидный/ограниченный ключ, 5xx). ВАЖНО: это НЕ ErrNotFound.
// Backfill-воркеры (cover/rating/year/renown) на ErrNotFound пишут outcome
// "not_found" с длинным TTL (90 дней), а на прочие ошибки — "error" с коротким
// ретраем. Раньше провайдеры возвращали ErrNotFound на любой не-200 → один
// 429/битый ключ ОТРАВЛЯЛ книгу как «не обогащается» на 90 дней (кейс GB: 110k
// книг в not_found из-за анонимных 429 и невалидного ключа, воркер их не
// перепроверял). Теперь транзиент → ErrUpstream → короткий ретрай.
var ErrUpstream = errors.New("upstream error")

// statusErr классифицирует не-2xx HTTP-статус внешнего провайдера:
//   - 404 → ErrNotFound (честное отсутствие для path-запросов вида
//     /isbn/{isbn}.json ∥ /works/{key}/ratings.json; search-эндпоинты не 404,
//     у них «не найдено» = 200 с пустым списком);
//   - всё остальное (429/400/403/5xx) → ErrUpstream (транзиент, короткий ретрай).
func statusErr(code int) error {
	if code == http.StatusNotFound {
		return ErrNotFound
	}
	return fmt.Errorf("%w: status %d", ErrUpstream, code)
}

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

	// WikidataQID — уже известный QID работы (works.ext_ids->>'wd_qid': его
	// находят Tier-2 группировки и «Известность»). Источник wikidata берёт его
	// вместо поиска книги по названию (#294). Пустой — ищем сами.
	WikidataQID string
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

	// Тёзки (грабля №22). Enricher заполняет сам по ID (withNamesakeContext):
	// Note — уточнение из INPX («Блум», «фантаст»), Namesakes — в базе есть
	// другой автор с тем же именем, BookTitles — названия книг автора (свои и
	// оригинальные) для якоря по книгам.
	Note       string
	Namesakes  bool
	BookTitles []string
	// LatinName — имя автора латиницей из fb2 его переводов (src_author_normalized:
	// «фамилия имя отчество» в нижнем регистре), за которое голосует большинство
	// книг, кроме сборников. Английская Википедия и OpenLibrary ищут по нему
	// (latinQuery): по кириллице иностранца они не находят (case study #280).
	LatinName string
	// Профиль книг автора для политики приёма кандидата (candidate_policy.go):
	// типичный год книг — медиана годов написания или издания (0 — неизвестен;
	// не минимум: переложения древних текстов и ошибки дат давали «книги с 1532»),
	// доля сетевой литературы среди работ и коды жанров.
	BooksYear int
	NetShare  float64
	Genres    []string
}

// cacheKey — всё, от чего зависит найденная статья: автор, имя, тёзки и книги
// (строгий путь подтверждает по ним).
func (q AuthorQuery) cacheKey() string {
	return strings.Join([]string{strconv.FormatInt(q.ID, 10), q.FullName, q.MiddleName, q.Note,
		strconv.FormatBool(q.Strict()), strings.Join(q.BookTitles, "\x1f")}, "|")
}

// latinQuery — тот же автор латиницей для источников на латинице. Фамилия —
// столько первых слов LatinName, сколько частей у нашей фамилии («Ле Гуин» →
// «le guin»), имя — следующее слово, остальное — второе имя. ok=false — латинского
// имени нет или оно не похоже на наше (первая буква фамилии не соответствует:
// голос мог дать составитель антологии).
func (q AuthorQuery) latinQuery() (AuthorQuery, bool) {
	toks := strings.Fields(q.LatinName)
	lastParts := strings.Fields(q.LastName)
	n := len(lastParts)
	if n == 0 || len(toks) < n || (strings.TrimSpace(q.FirstName) != "" && len(toks) < n+1) {
		return q, false
	}
	if !sameInitialSound(translitName(lastParts[0]), translitName(toks[0])) {
		return q, false
	}
	l := AuthorQuery{
		ID: q.ID, LastName: strings.Join(toks[:n], " "), FullName: strings.Join(toks, " "),
		Note: q.Note, Namesakes: q.Namesakes, BookTitles: q.BookTitles,
		BooksYear: q.BooksYear, NetShare: q.NetShare, Genres: q.Genres,
	}
	if len(toks) > n {
		l.FirstName = toks[n]
	}
	if len(toks) > n+1 {
		l.MiddleName = strings.Join(toks[n+1:], " ")
	}
	return l, true
}

// queryForLang — запрос для раздела Википедии: латиницей для латинских разделов,
// если латинское имя есть; иначе как есть.
func queryForLang(q AuthorQuery, lang string) AuthorQuery {
	switch lang {
	case "ru", "uk", "be", "bg", "sr", "kk":
		return q
	}
	if l, ok := q.latinQuery(); ok {
		return l
	}
	return q
}

// sameInitialSound — первые буквы транслита кириллической фамилии и латинской
// соответствуют друг другу: Г ~ H (Гюго — Hugo), Ф ~ Ph, Ц ~ C/Ts, К ~ C/Q, Х ~ H/Kh,
// Й/И/Э ~ I/Y/J/E/A, В ~ V/W, Дж ~ J/G, З ~ Z/S.
func sameInitialSound(cyrLat, lat string) bool {
	if cyrLat == "" || lat == "" {
		return false
	}
	class := func(s string) string {
		switch {
		case strings.HasPrefix(s, "dzh"):
			return "j"
		case strings.HasPrefix(s, "ph"):
			return "f"
		case strings.HasPrefix(s, "kh"):
			return "h"
		case strings.HasPrefix(s, "zh"):
			return "zh"
		case strings.HasPrefix(s, "ch"):
			return "ch"
		case strings.HasPrefix(s, "sh"), strings.HasPrefix(s, "sch"):
			return "sh"
		}
		switch s[0] {
		case 'g', 'h':
			return "h"
		case 'c', 'k', 'q':
			return "k"
		case 'i', 'y', 'j', 'e', 'a':
			return "v0"
		case 'v', 'w':
			return "v"
		case 'z', 's':
			return "s"
		}
		return s[:1]
	}
	a, b := class(cyrLat), class(lat)
	if a == b {
		return true
	}
	// Ц передают и как C, и как Ts; Ч — Ch и Tch; Дж — J и G.
	pairs := map[[2]string]bool{{"k", "t"}: true, {"ch", "t"}: true, {"j", "h"}: true, {"j", "v0"}: true}
	return pairs[[2]string{a, b}] || pairs[[2]string{b, a}] || (a == "s" && b == "k") || (b == "s" && a == "k")
}

// Strict — автора нельзя искать просто по имени: у него есть тёзки или
// уточнение, или имя из одного слова («София», «2B», «ScrLock» — по одной
// фамилии находились столица Болгарии, поп-дуэт и клавиша, #280). Кандидата
// принимаем, только если его подтвердило уточнение (статья «ФИО (уточнение)» в
// Википедии) или одна из книг автора.
func (q AuthorQuery) Strict() bool {
	oneWord := strings.TrimSpace(q.LastName) != "" && strings.TrimSpace(q.FirstName) == ""
	return q.Note != "" || q.Namesakes || oneWord
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
	// TMDBMovieID / TMDBTVID — id в The Movie Database (Wikidata P4947 /
	// P4983, тот же SPARQL-ответ). Приоритетный источник постера: TMDB
	// хостит настоящие постеры, а Commons (P18) у фильмов почти пуст —
	// постеры копирайтные (см. TMDBPosterProvider).
	TMDBMovieID string
	TMDBTVID    string
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

// SrcLangProvider — внешний источник ЯЗЫКА ОРИГИНАЛА произведения для
// дозаполнения books.src_lang (перевод без fb2 <src-lang>). Возвращает
// нормализованный ISO 639-1 код («fr») либо ErrNotFound — книга не сопоставлена
// / язык не размечен / precision-гейты источника отсекли неоднозначность;
// прочие ошибки — сетевые/HTTP (воркер помечает 'error', короткий ретрай).
// Name() должен совпадать со строкой source в book_src_lang_lookups.
// v1 реализация одна — Wikidata P407 (см. wikidata_srclang.go); OpenLibrary
// сознательно НЕ источник: поля «язык оригинала» у него нет, а languages
// работы — union языков всех изданий (для оригинала это гадание).
type SrcLangProvider interface {
	Name() string
	FetchSrcLang(ctx context.Context, q BookQuery) (string, error)
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
	// Kind — тип произведения от источника (заполняет только Fantlab —
	// work_type_id приходит в том же ответе search-works): "collection" /
	// "anthology" — сборник; "novel" — уверенно ОБЫЧНОЕ произведение
	// (роман/повесть/рассказ — снимает ошибочную эвристику works.kind);
	// "" — тип неизвестен/нерелевантен (цикл, статья…), ничего не решаем.
	// НЕ входит в total(): kind — бонус к found-матчу, не сигнал находки.
	Kind string
	// Year — год первой публикации от источника (Fantlab, #288); 0 — неизвестен.
	// Тоже бонус к найденному, не сигнал находки.
	Year int
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
