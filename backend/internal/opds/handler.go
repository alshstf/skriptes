package opds

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/skriptes/skriptes/backend/internal/auth"
	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/catalog"
	"github.com/skriptes/skriptes/backend/internal/converter"
	"github.com/skriptes/skriptes/backend/internal/history"
)

// Config — настройки OPDS-handler'ов.
//
// BaseURL — origin приложения без trailing slash. Используется для
// формирования абсолютных URL во всех link'ах feed'ов. Если пустой —
// fallback на схему+host из запроса (Request.URL.Scheme/Host), но
// reverse-proxy ломает это, поэтому в production cfg.AllowedOrigins[0]
// или эквивалент.
//
// CoversRoot — абсолютный путь к /cache/covers (для serve файла).
//
// PageSize — дефолтный размер страницы в acquisition feed'ах.
type Config struct {
	BaseURL    string
	CoversRoot string
	PageSize   int
}

// Deps — runtime-зависимости handler'ов.
type Deps struct {
	Books     *books.Service
	Catalog   *catalog.Service
	Converter *converter.Converter // для acquisition: convert+serve файла
	History   *history.Service     // учёт приобретения при скачивании (для запросов оценки); может быть nil
	BooksRoot string               // корень read-only volume (передаётся в converter.SourceBook)
	Logger    *slog.Logger
}

// Handler — компактный объект, держащий config+deps. Методы возвращают
// http.HandlerFunc для каждого OPDS-роута.
type Handler struct {
	cfg  Config
	deps Deps
}

func NewHandler(cfg Config, deps Deps) *Handler {
	if cfg.PageSize <= 0 {
		cfg.PageSize = 50
	}
	if deps.Logger == nil {
		deps.Logger = slog.Default()
	}
	return &Handler{cfg: cfg, deps: deps}
}

// ----- Root navigation -----

// Root — GET /opds/. Корневой navigation feed: ссылки на recent /
// authors / series / genres + search-link.
func (h *Handler) Root(w http.ResponseWriter, r *http.Request) {
	base := h.baseURL(r)
	now := time.Now().UTC().Format(time.RFC3339)

	feed := &Feed{
		ID:      "urn:skriptes:opds:root",
		Title:   "skriptes",
		Updated: now,
		Author:  &Person{Name: "skriptes"},
		Links: []Link{
			{Rel: RelSelf, Href: joinURL(base, "/opds/"), Type: MIMEFeedNavigation},
			{Rel: RelStart, Href: joinURL(base, "/opds/"), Type: MIMEFeedNavigation},
			{Rel: RelSearch, Href: joinURL(base, "/opds/opensearch.xml"), Type: MIMEOpenSearch},
		},
		Entries: []Entry{
			navEntry("recent", "Новинки", "Недавно добавленные книги", joinURL(base, "/opds/recent"), MIMEFeedAcquisition),
			navEntry("authors", "Авторы", "Алфавитный список авторов", joinURL(base, "/opds/authors"), MIMEFeedNavigation),
			navEntry("series", "Серии", "Все серии каталога", joinURL(base, "/opds/series"), MIMEFeedNavigation),
			navEntry("genres", "Жанры", "Книги по жанрам", joinURL(base, "/opds/genres"), MIMEFeedNavigation),
		},
	}
	h.writeFeed(w, MIMEFeedNavigation, feed)
}

// ----- Recent (acquisition with paging) -----

// Recent — GET /opds/recent?page=N. Постраничный acquisition feed
// новинок (sort=year_desc на стороне Meili).
func (h *Handler) Recent(w http.ResponseWriter, r *http.Request) {
	page := parsePage(r)
	base := h.baseURL(r)
	limit := h.cfg.PageSize
	offset := (page - 1) * limit

	resp, err := h.deps.Books.List(r.Context(), books.ListParams{
		Limit:  limit,
		Offset: offset,
		Sort:   "year_desc",
	})
	if err != nil {
		h.error(w, "search failed", err, http.StatusBadGateway)
		return
	}

	feed := &Feed{
		ID:      "urn:skriptes:opds:recent",
		Title:   "Новинки",
		Updated: time.Now().UTC().Format(time.RFC3339),
		Author:  &Person{Name: "skriptes"},
		Links: pagingLinks(
			base, "/opds/recent", page, int(resp.Total), limit,
			MIMEFeedAcquisition,
		),
		TotalResults: int(resp.Total),
		ItemsPerPage: limit,
		StartIndex:   offset,
	}
	for _, it := range resp.Items {
		feed.Entries = append(feed.Entries, ListItemToEntry(it, base, h.makeFormats))
	}
	h.writeFeed(w, MIMEFeedAcquisition, feed)
}

// ----- Authors navigation -----

// AuthorsList — GET /opds/authors?page=N. Постраничный navigation feed
// со ссылками на acquisition-feed'ы каждого автора.
func (h *Handler) AuthorsList(w http.ResponseWriter, r *http.Request) {
	page := parsePage(r)
	base := h.baseURL(r)
	limit := h.cfg.PageSize
	offset := (page - 1) * limit

	items, total, err := h.deps.Catalog.ListAuthors(r.Context(), limit, offset)
	if err != nil {
		h.error(w, "list authors failed", err, http.StatusInternalServerError)
		return
	}
	feed := &Feed{
		ID:           "urn:skriptes:opds:authors",
		Title:        "Авторы",
		Updated:      time.Now().UTC().Format(time.RFC3339),
		Author:       &Person{Name: "skriptes"},
		Links:        pagingLinks(base, "/opds/authors", page, total, limit, MIMEFeedNavigation),
		TotalResults: total,
		ItemsPerPage: limit,
		StartIndex:   offset,
	}
	for _, a := range items {
		feed.Entries = append(feed.Entries, AuthorEntryToEntry(a, base))
	}
	h.writeFeed(w, MIMEFeedNavigation, feed)
}

// AuthorBooks — GET /opds/authors/{id}. Acquisition feed с книгами автора.
//
// Используем books.List(AuthorID=id) — он отдаёт уже отсортированный
// список (год desc по дефолту, но с фильтром по автору ranker
// переключается на title-сортировку). Чтобы не загружать UI авторов
// с 1000+ книг, ограничиваем limit'ом до 500 (см. catalog.ListAuthors).
func (h *Handler) AuthorBooks(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		h.error(w, "invalid id", err, http.StatusBadRequest)
		return
	}
	page := parsePage(r)
	base := h.baseURL(r)
	limit := h.cfg.PageSize
	offset := (page - 1) * limit

	resp, err := h.deps.Books.List(r.Context(), books.ListParams{
		AuthorID: id,
		Limit:    limit,
		Offset:   offset,
		Sort:     "year_desc",
	})
	if err != nil {
		h.error(w, "search failed", err, http.StatusBadGateway)
		return
	}
	feed := &Feed{
		ID:           fmt.Sprintf("urn:skriptes:opds:author:%d", id),
		Title:        fmt.Sprintf("Автор #%d", id),
		Updated:      time.Now().UTC().Format(time.RFC3339),
		Author:       &Person{Name: "skriptes"},
		Links:        pagingLinks(base, fmt.Sprintf("/opds/authors/%d", id), page, int(resp.Total), limit, MIMEFeedAcquisition),
		TotalResults: int(resp.Total),
		ItemsPerPage: limit,
		StartIndex:   offset,
	}
	for _, it := range resp.Items {
		feed.Entries = append(feed.Entries, ListItemToEntry(it, base, h.makeFormats))
	}
	h.writeFeed(w, MIMEFeedAcquisition, feed)
}

// ----- Series navigation -----

func (h *Handler) SeriesList(w http.ResponseWriter, r *http.Request) {
	page := parsePage(r)
	base := h.baseURL(r)
	limit := h.cfg.PageSize
	offset := (page - 1) * limit

	items, total, err := h.deps.Catalog.ListSeries(r.Context(), limit, offset)
	if err != nil {
		h.error(w, "list series failed", err, http.StatusInternalServerError)
		return
	}
	feed := &Feed{
		ID:           "urn:skriptes:opds:series",
		Title:        "Серии",
		Updated:      time.Now().UTC().Format(time.RFC3339),
		Author:       &Person{Name: "skriptes"},
		Links:        pagingLinks(base, "/opds/series", page, total, limit, MIMEFeedNavigation),
		TotalResults: total,
		ItemsPerPage: limit,
		StartIndex:   offset,
	}
	for _, s := range items {
		feed.Entries = append(feed.Entries, SeriesEntryToEntry(s, base))
	}
	h.writeFeed(w, MIMEFeedNavigation, feed)
}

func (h *Handler) SeriesBooks(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		h.error(w, "invalid id", err, http.StatusBadRequest)
		return
	}
	page := parsePage(r)
	base := h.baseURL(r)
	limit := h.cfg.PageSize
	offset := (page - 1) * limit

	resp, err := h.deps.Books.List(r.Context(), books.ListParams{
		SeriesID: id,
		Limit:    limit,
		Offset:   offset,
	})
	if err != nil {
		h.error(w, "search failed", err, http.StatusBadGateway)
		return
	}
	feed := &Feed{
		ID:           fmt.Sprintf("urn:skriptes:opds:series:%d", id),
		Title:        fmt.Sprintf("Серия #%d", id),
		Updated:      time.Now().UTC().Format(time.RFC3339),
		Author:       &Person{Name: "skriptes"},
		Links:        pagingLinks(base, fmt.Sprintf("/opds/series/%d", id), page, int(resp.Total), limit, MIMEFeedAcquisition),
		TotalResults: int(resp.Total),
		ItemsPerPage: limit,
		StartIndex:   offset,
	}
	for _, it := range resp.Items {
		feed.Entries = append(feed.Entries, ListItemToEntry(it, base, h.makeFormats))
	}
	h.writeFeed(w, MIMEFeedAcquisition, feed)
}

// ----- Genres navigation -----

func (h *Handler) GenresList(w http.ResponseWriter, r *http.Request) {
	base := h.baseURL(r)
	items, err := h.deps.Catalog.ListGenres(r.Context(), 0)
	if err != nil {
		h.error(w, "list genres failed", err, http.StatusInternalServerError)
		return
	}
	feed := &Feed{
		ID:      "urn:skriptes:opds:genres",
		Title:   "Жанры",
		Updated: time.Now().UTC().Format(time.RFC3339),
		Author:  &Person{Name: "skriptes"},
		Links: []Link{
			{Rel: RelSelf, Href: joinURL(base, "/opds/genres"), Type: MIMEFeedNavigation},
			{Rel: RelStart, Href: joinURL(base, "/opds/"), Type: MIMEFeedNavigation},
			{Rel: RelUp, Href: joinURL(base, "/opds/"), Type: MIMEFeedNavigation},
		},
	}
	for _, g := range items {
		feed.Entries = append(feed.Entries, GenreEntryToEntry(g, base))
	}
	h.writeFeed(w, MIMEFeedNavigation, feed)
}

// GenreBooks — GET /opds/genres/{id}. Acquisition feed с книгами жанра.
// Используем books.List(Genres=[code]) — где code мы должны получить из id.
// Чтобы не тащить второй query на каждый запрос, делаем простой
// SELECT fb2_code FROM genres WHERE id=$1; через pool из catalog.
//
// Альтернатива: фильтровать в Meili не по жанру, а по жанру-id. Но
// сейчас Meili индексирует genres как массив строковых кодов, не id,
// и менять схему ради OPDS — оверкилл.
func (h *Handler) GenreBooks(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		h.error(w, "invalid id", err, http.StatusBadRequest)
		return
	}
	page := parsePage(r)
	base := h.baseURL(r)
	limit := h.cfg.PageSize
	offset := (page - 1) * limit

	code, display, err := h.lookupGenre(r.Context(), id)
	if err != nil {
		if errors.Is(err, errGenreNotFound) {
			h.error(w, "genre not found", err, http.StatusNotFound)
			return
		}
		h.error(w, "lookup genre failed", err, http.StatusInternalServerError)
		return
	}

	resp, err := h.deps.Books.List(r.Context(), books.ListParams{
		Genres: []string{code},
		Limit:  limit,
		Offset: offset,
		Sort:   "year_desc",
	})
	if err != nil {
		h.error(w, "search failed", err, http.StatusBadGateway)
		return
	}
	feed := &Feed{
		ID:           fmt.Sprintf("urn:skriptes:opds:genre:%d", id),
		Title:        display,
		Updated:      time.Now().UTC().Format(time.RFC3339),
		Author:       &Person{Name: "skriptes"},
		Links:        pagingLinks(base, fmt.Sprintf("/opds/genres/%d", id), page, int(resp.Total), limit, MIMEFeedAcquisition),
		TotalResults: int(resp.Total),
		ItemsPerPage: limit,
		StartIndex:   offset,
	}
	for _, it := range resp.Items {
		feed.Entries = append(feed.Entries, ListItemToEntry(it, base, h.makeFormats))
	}
	h.writeFeed(w, MIMEFeedAcquisition, feed)
}

// ----- Search -----

// OpenSearchDescription — GET /opds/opensearch.xml. Документ OpenSearch
// 1.1, на который указывает RelSearch в root feed'е. E-reader парсит
// его, видит URL-template и подставляет туда query.
//
// Используем encoding/xml вместо raw-printf чтобы базовые символы (& <
// в base-URL — теоретически возможно если кто-то задаст cfg.BaseURL
// руками) были escape'нуты корректно. {searchTerms} — литерал из спеки
// OpenSearch, его не трогаем, поэтому собираем через placeholder + Replace.
func (h *Handler) OpenSearchDescription(w http.ResponseWriter, r *http.Request) {
	base := strings.TrimRight(h.baseURL(r), "/")
	// Placeholder %%SEARCHTERMS%% переживёт xml.MarshalIndent, после чего
	// мы заменим на литерал {searchTerms}; xml encoder заэскейпит
	// фигурные скобки если оставить их прямо в struct.
	doc := struct {
		XMLName       xml.Name `xml:"OpenSearchDescription"`
		XMLNS         string   `xml:"xmlns,attr"`
		ShortName     string   `xml:"ShortName"`
		Description   string   `xml:"Description"`
		InputEncoding string   `xml:"InputEncoding"`
		URL           struct {
			Type     string `xml:"type,attr"`
			Template string `xml:"template,attr"`
		} `xml:"Url"`
	}{
		XMLNS:         "http://a9.com/-/spec/opensearch/1.1/",
		ShortName:     "skriptes",
		Description:   "Поиск по каталогу skriptes",
		InputEncoding: "UTF-8",
	}
	doc.URL.Type = MIMEFeedAcquisition
	doc.URL.Template = base + "/opds/search?q=%%SEARCHTERMS%%"

	body, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		h.error(w, "marshal opensearch", err, http.StatusInternalServerError)
		return
	}
	// Восстанавливаем литерал {searchTerms} (encoding/xml не трогает %).
	bodyStr := strings.Replace(string(body), "%%SEARCHTERMS%%", "{searchTerms}", 1)

	w.Header().Set("Content-Type", MIMEOpenSearch+"; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, xml.Header)
	_, _ = io.WriteString(w, bodyStr)
}

// Search — GET /opds/search?q=…&page=N. Acquisition feed с результатами
// поиска по Meili.
func (h *Handler) Search(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		h.error(w, "empty query", nil, http.StatusBadRequest)
		return
	}
	page := parsePage(r)
	base := h.baseURL(r)
	limit := h.cfg.PageSize
	offset := (page - 1) * limit

	resp, err := h.deps.Books.List(r.Context(), books.ListParams{
		Query:  query,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		h.error(w, "search failed", err, http.StatusBadGateway)
		return
	}
	feed := &Feed{
		ID:           "urn:skriptes:opds:search:" + query,
		Title:        "Поиск: " + query,
		Updated:      time.Now().UTC().Format(time.RFC3339),
		Author:       &Person{Name: "skriptes"},
		Links:        pagingLinks(base, "/opds/search?q="+query, page, int(resp.Total), limit, MIMEFeedAcquisition),
		TotalResults: int(resp.Total),
		ItemsPerPage: limit,
		StartIndex:   offset,
	}
	for _, it := range resp.Items {
		feed.Entries = append(feed.Entries, ListItemToEntry(it, base, h.makeFormats))
	}
	h.writeFeed(w, MIMEFeedAcquisition, feed)
}

// ----- Acquisition (download) -----

// Download — GET /opds/books/{id}/download?format=epub3. Реальный
// серве файла. Логически дубль /api/books/{id}/download, но
// смонтирован в OPDS-tree чтобы:
//
//  1. Базовая авторизация работала там же где остальные OPDS-роуты
//     (Basic вместо session cookie);
//  2. Можно было дёргать без CSRF (e-reader не передаёт CSRF-token).
//
// Реализация — короткая обёртка над converter.Convert + http.ServeFile.
// recordAcquisitionAsync — fire-and-forget фиксация приобретения (OPDS-скачивание).
// Детачнутый контекст (запись должна пережить ответ); svc==nil → no-op. Отдельная
// функция (не inline-горутина) — чтобы не тянуть request-ctx в горутину (gosec G118).
func recordAcquisitionAsync(svc *history.Service, logger *slog.Logger, userID, bookID int64) {
	if svc == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := svc.RecordAcquisition(ctx, userID, bookID); err != nil {
			logger.Warn("opds record acquisition failed", "user_id", userID, "book_id", bookID, "err", err)
		}
	}()
}

func (h *Handler) Download(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		h.error(w, "invalid id", err, http.StatusBadRequest)
		return
	}
	format, err := converter.ParseFormat(r.URL.Query().Get("format"))
	if err != nil {
		h.error(w, err.Error(), err, http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	book, err := h.deps.Books.Get(ctx, id)
	if err != nil {
		if errors.Is(err, books.ErrNotFound) {
			h.error(w, "book not found", err, http.StatusNotFound)
			return
		}
		h.error(w, "query failed", err, http.StatusInternalServerError)
		return
	}
	if book.Deleted {
		h.error(w, "book deleted", nil, http.StatusGone)
		return
	}

	// Учёт приобретения (для блока «Оцените прочитанное»): OPDS-скачивание —
	// такой же канал, как web-скачивание / Send-to-Kindle. Fire-and-forget;
	// пользователь — из Basic-auth контекста (requireBasicAuth кладёт его).
	if u, ok := auth.UserFromContext(r.Context()); ok {
		recordAcquisitionAsync(h.deps.History, h.deps.Logger, u.ID, book.ID)
	}

	res, err := h.deps.Converter.Convert(ctx, converter.SourceBook{
		ID:       book.ID,
		Archive:  book.Archive,
		FileName: book.FileName,
		Ext:      book.Ext,
	}, format)
	if err != nil {
		h.error(w, "convert failed", err, http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", res.ContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+res.Filename+`"`)
	http.ServeFile(w, r, res.Path) //nolint:gosec // path computed by converter, не из URL
}

// Cover — GET /opds/covers/{name}. Прямой serve файла из cache.
// Защита от path traversal такая же как в api/covers.go.
func (h *Handler) Cover(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		http.NotFound(w, r)
		return
	}
	if h.cfg.CoversRoot == "" {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(h.cfg.CoversRoot, name)
	if !strings.HasPrefix(filepath.Clean(full), filepath.Clean(h.cfg.CoversRoot)) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=2592000, immutable") // 30 дней
	http.ServeFile(w, r, full)                                            //nolint:gosec // name прошёл sanity-check выше
}

// ----- helpers -----

// navEntry — шаблон для записей в navigation feed'е (root).
func navEntry(slug, title, summary, href, mime string) Entry {
	return Entry{
		ID:      "urn:skriptes:opds:" + slug,
		Title:   title,
		Updated: time.Now().UTC().Format(time.RFC3339),
		Summary: &Text{Type: "text", Body: summary},
		Links: []Link{{
			Rel:  RelSubsection,
			Href: href,
			Type: mime,
		}},
	}
}

// pagingLinks — собирает self/start/up/first/prev/next/last для
// постраничного feed'а. basePath без query (например "/opds/recent"
// или "/opds/search?q=war" — отдельная щепетильность для search,
// чтобы query сохранялся в постраничных ссылках).
func pagingLinks(baseURL, basePath string, page, total, limit int, mime string) []Link {
	pages := (total + limit - 1) / limit
	if pages < 1 {
		pages = 1
	}
	sep := "?"
	if strings.Contains(basePath, "?") {
		sep = "&"
	}

	makeHref := func(p int) string {
		return joinURL(baseURL, basePath) + sep + "page=" + strconv.Itoa(p)
	}

	links := []Link{
		{Rel: RelSelf, Href: makeHref(page), Type: mime},
		{Rel: RelStart, Href: joinURL(baseURL, "/opds/"), Type: MIMEFeedNavigation},
		{Rel: RelUp, Href: joinURL(baseURL, "/opds/"), Type: MIMEFeedNavigation},
		{Rel: RelFirst, Href: makeHref(1), Type: mime},
		{Rel: RelLast, Href: makeHref(pages), Type: mime},
	}
	if page > 1 {
		links = append(links, Link{Rel: RelPrev, Href: makeHref(page - 1), Type: mime})
	}
	if page < pages {
		links = append(links, Link{Rel: RelNext, Href: makeHref(page + 1), Type: mime})
	}
	return links
}

// makeFormats — список acquisition links для одной книги.
//
// Порядок важен: первым идёт fb2 (нативный формат нашей коллекции,
// zero-conversion — converter просто отдаёт байты из zip-архива без
// re-encoding). KOReader / CoolReader / Cool Reader Android и
// большинство «русских» e-reader приложений умеют fb2 нативно и
// выберут его. EPUB3 вторым — для Kindle / Books / Apple Books и
// прочих которые fb2 не понимают.
//
// MIME для fb2 — application/x-fictionbook+xml; этот же тип возвращает
// download-handler в Content-Type (см. converter.mimeType), поэтому
// клиент после скачивания не упирается в конфликт типов.
//
// kepub/kfx/azw8 НЕ анонсируем по умолчанию: они тяжелее (конверсия
// в реальный отличный формат), целевая аудитория узкая (специфичные
// e-reader'ы), а добавление их сюда раздуло бы entry без явной пользы
// для большинства. Если кто-то нашёл нужным — пусть пользователь сам
// дёрнет /opds/books/{id}/download?format=kepub (handler уже принимает).
func (h *Handler) makeFormats(bookID int64) []FormatLink {
	return []FormatLink{
		{
			HrefPath: fmt.Sprintf("/opds/books/%d/download?format=fb2", bookID),
			MIME:     "application/x-fictionbook+xml",
			Title:    "Скачать FB2",
		},
		{
			HrefPath: fmt.Sprintf("/opds/books/%d/download?format=epub3", bookID),
			MIME:     "application/epub+zip",
			Title:    "Скачать EPUB",
		},
	}
}

// baseURL — для каждого запроса вычисляем origin. Сначала пробуем
// cfg.BaseURL (production behind reverse proxy); если пуст — берём
// схему+host из заголовков (X-Forwarded-Proto / Host).
func (h *Handler) baseURL(r *http.Request) string {
	if h.cfg.BaseURL != "" {
		return h.cfg.BaseURL
	}
	scheme := "http"
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	} else if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	return scheme + "://" + host
}

// parsePage — page query-параметр. Дефолт 1 (1-indexed для удобства
// конкатенации с pagingLinks). Невалидное / меньше 1 → 1.
func parsePage(r *http.Request) int {
	p, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || p < 1 {
		return 1
	}
	return p
}

// parseID — общий парсер id-параметра из URL. Все наши пути используют
// {id} как имя; если когда-нибудь добавится {author_id} / {series_id},
// — расширим в parseInt(r, name).
func parseID(r *http.Request) (int64, error) {
	v := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid id")
	}
	return id, nil
}

// writeFeed — единая точка записи Atom-ответа. Логирует marshal-ошибки.
func (h *Handler) writeFeed(w http.ResponseWriter, mime string, feed *Feed) {
	body, err := Marshal(feed)
	if err != nil {
		h.deps.Logger.Error("opds: marshal failed", "err", err, "id", feed.ID)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", mime+"; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// error — пишем ошибку как короткий text/plain (OPDS-клиенты обычно
// показывают самим пользователю; XML-feed с ошибкой не вернёт лучшего
// UX). Логируем underlying err если есть.
func (h *Handler) error(w http.ResponseWriter, msg string, err error, code int) {
	if err != nil {
		h.deps.Logger.Info("opds: handler error", "msg", msg, "err", err, "code", code)
	}
	http.Error(w, msg, code)
}

// errGenreNotFound — sentinel для lookupGenre.
var errGenreNotFound = errors.New("genre not found")

// lookupGenre — отдельный helper для GenreBooks: нам нужен code+display
// чтобы передать в Meili-фильтр и в заголовок feed'а.
func (h *Handler) lookupGenre(ctx context.Context, id int64) (code, display string, err error) {
	// Используем low-level pool через catalog (внутри Pool exposed нет).
	// Делаем минимальный helper в catalog.Service: но это утяжелит API.
	// Проще — взять данные через ListGenres + linear search; жанров ~250,
	// O(N) на лишний запрос терпимо.
	items, err := h.deps.Catalog.ListGenres(ctx, 0)
	if err != nil {
		return "", "", err
	}
	for _, g := range items {
		if g.ID == id {
			return g.Code, g.Display, nil
		}
	}
	return "", "", errGenreNotFound
}
