package opds

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/skriptes/skriptes/backend/internal/books"
)

// OPDS 2.0 (#389, B7) — JSON-каталог параллельно Atom (v1) на /opds/v2: те же
// разделы, вход, скрытое и скачивание (/opds/books/{id}/download).
// Спецификация: https://drafts.opds.io/opds-2.0.

const (
	MIMEOPDS2       = "application/opds+json"
	MIMEOPDS2Pub    = "application/opds-publication+json"
	relAcquisition2 = "http://opds-spec.org/acquisition"
)

type feed2 struct {
	Metadata     meta2   `json:"metadata"`
	Links        []link2 `json:"links"`
	Navigation   []link2 `json:"navigation,omitempty"`
	Publications []pub2  `json:"publications,omitempty"`
}

type meta2 struct {
	Title         string `json:"title"`
	NumberOfItems int    `json:"numberOfItems,omitempty"`
	ItemsPerPage  int    `json:"itemsPerPage,omitempty"`
	CurrentPage   int    `json:"currentPage,omitempty"`
}

type link2 struct {
	Rel       string `json:"rel,omitempty"`
	Href      string `json:"href"`
	Type      string `json:"type,omitempty"`
	Title     string `json:"title,omitempty"`
	Templated bool   `json:"templated,omitempty"`
}

type pub2 struct {
	Metadata pubMeta2 `json:"metadata"`
	Links    []link2  `json:"links"`
}

type pubMeta2 struct {
	Type       string      `json:"@type"`
	Identifier string      `json:"identifier"`
	Title      string      `json:"title"`
	Author     []contrib2  `json:"author,omitempty"`
	Language   string      `json:"language,omitempty"`
	Published  string      `json:"published,omitempty"`
	Subject    []string    `json:"subject,omitempty"`
	BelongsTo  *belongsTo2 `json:"belongsTo,omitempty"`
}

type contrib2 struct {
	Name string `json:"name"`
}

type belongsTo2 struct {
	Series []seriesRef2 `json:"series"`
}

type seriesRef2 struct {
	Name     string `json:"name"`
	Position *int   `json:"position,omitempty"`
}

func (h *Handler) writeFeed2(w http.ResponseWriter, f feed2) {
	w.Header().Set("Content-Type", MIMEOPDS2+"; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(f)
}

// base2 — корень v2 и общие ссылки ленты.
func (h *Handler) links2(base, self string) []link2 {
	return []link2{
		{Rel: "self", Href: joinURL(base, self), Type: MIMEOPDS2},
		{Rel: "start", Href: joinURL(base, "/opds/v2/"), Type: MIMEOPDS2},
		{Rel: "search", Href: joinURL(base, "/opds/v2/search{?query}"), Type: MIMEOPDS2, Templated: true},
	}
}

// paging2 — first/prev/next/last для постраничной ленты; path без page.
func paging2(base, path string, page, total, limit int) []link2 {
	pages := max((total+limit-1)/limit, 1)
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	href := func(p int) string { return joinURL(base, path) + sep + "page=" + strconv.Itoa(p) }
	out := []link2{{Rel: "first", Href: href(1), Type: MIMEOPDS2}, {Rel: "last", Href: href(pages), Type: MIMEOPDS2}}
	if page > 1 {
		out = append(out, link2{Rel: "previous", Href: href(page - 1), Type: MIMEOPDS2})
	}
	if page < pages {
		out = append(out, link2{Rel: "next", Href: href(page + 1), Type: MIMEOPDS2})
	}
	return out
}

func (h *Handler) publication2(it books.ListItem, base string) pub2 {
	p := pub2{Metadata: pubMeta2{
		Type: "http://schema.org/Book", Identifier: bookURN(it.ID), Title: it.Title,
		Language: it.Lang, Subject: it.Genres,
	}}
	for _, a := range it.Authors {
		p.Metadata.Author = append(p.Metadata.Author, contrib2{Name: a})
	}
	if it.Year != nil {
		p.Metadata.Published = strconv.Itoa(*it.Year)
	}
	if it.Series != "" {
		p.Metadata.BelongsTo = &belongsTo2{Series: []seriesRef2{{Name: it.Series, Position: it.SerNo}}}
	}
	for _, f := range h.makeFormats(it.ID) {
		p.Links = append(p.Links, link2{Rel: relAcquisition2, Href: joinURL(base, f.HrefPath), Type: f.MIME, Title: f.Title})
	}
	return p
}

// booksFeed2 — лента книг: список по параметрам со скрытым пользователя.
func (h *Handler) booksFeed2(w http.ResponseWriter, r *http.Request, title, path string, params books.ListParams) {
	page := parsePage(r)
	base := h.baseURL(r)
	limit := h.cfg.PageSize
	params.Limit, params.Offset = limit, (page-1)*limit
	params.ExcludeGenres, params.ExcludeLangs = h.exclusions(r)
	resp, err := h.deps.Books.List(r.Context(), params)
	if err != nil {
		h.error(w, "search failed", err, http.StatusBadGateway)
		return
	}
	self := path
	if page > 1 {
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		self = path + sep + "page=" + strconv.Itoa(page)
	}
	f := feed2{
		Metadata: meta2{Title: title, NumberOfItems: int(resp.Total), ItemsPerPage: limit, CurrentPage: page},
		Links:    append(h.links2(base, self), paging2(base, path, page, int(resp.Total), limit)...),
	}
	f.Publications = make([]pub2, 0, len(resp.Items))
	for _, it := range resp.Items {
		f.Publications = append(f.Publications, h.publication2(it, base))
	}
	h.writeFeed2(w, f)
}

// V2Root — GET /opds/v2/: разделы каталога.
func (h *Handler) V2Root(w http.ResponseWriter, r *http.Request) {
	base := h.baseURL(r)
	nav := func(title, path string) link2 {
		return link2{Href: joinURL(base, path), Title: title, Type: MIMEOPDS2}
	}
	h.writeFeed2(w, feed2{
		Metadata: meta2{Title: "skriptes"},
		Links:    h.links2(base, "/opds/v2/"),
		Navigation: []link2{
			nav("Новинки", "/opds/v2/recent"),
			nav("Авторы", "/opds/v2/authors"),
			nav("Серии", "/opds/v2/series"),
			nav("Жанры", "/opds/v2/genres"),
		},
	})
}

// V2Recent — GET /opds/v2/recent.
func (h *Handler) V2Recent(w http.ResponseWriter, r *http.Request) {
	h.booksFeed2(w, r, "Новинки", "/opds/v2/recent", books.ListParams{Sort: "year_desc"})
}

// V2Search — GET /opds/v2/search?query=…
func (h *Handler) V2Search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("query"))
	if q == "" {
		q = strings.TrimSpace(r.URL.Query().Get("q"))
	}
	if q == "" {
		h.error(w, "empty query", nil, http.StatusBadRequest)
		return
	}
	h.booksFeed2(w, r, "Поиск: "+q, "/opds/v2/search?query="+url.QueryEscape(q), books.ListParams{Query: q})
}

// V2AuthorBooks — GET /opds/v2/authors/{id}.
func (h *Handler) V2AuthorBooks(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		h.error(w, "invalid id", err, http.StatusBadRequest)
		return
	}
	h.booksFeed2(w, r, fmt.Sprintf("Автор #%d", id), fmt.Sprintf("/opds/v2/authors/%d", id),
		books.ListParams{AuthorID: id, Sort: "year_desc"})
}

// V2SeriesBooks — GET /opds/v2/series/{id}.
func (h *Handler) V2SeriesBooks(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		h.error(w, "invalid id", err, http.StatusBadRequest)
		return
	}
	h.booksFeed2(w, r, fmt.Sprintf("Серия #%d", id), fmt.Sprintf("/opds/v2/series/%d", id), books.ListParams{SeriesID: id})
}

// V2GenreBooks — GET /opds/v2/genres/{id}.
func (h *Handler) V2GenreBooks(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		h.error(w, "invalid id", err, http.StatusBadRequest)
		return
	}
	code, display, err := h.lookupGenre(r.Context(), id)
	if err != nil {
		if errors.Is(err, errGenreNotFound) {
			h.error(w, "genre not found", err, http.StatusNotFound)
			return
		}
		h.error(w, "lookup genre failed", err, http.StatusInternalServerError)
		return
	}
	if exG, _ := h.exclusions(r); hidden([]string{code}, "", exG, nil) {
		h.error(w, "genre not found", nil, http.StatusNotFound)
		return
	}
	h.booksFeed2(w, r, display, fmt.Sprintf("/opds/v2/genres/%d", id),
		books.ListParams{Genres: []string{code}, Sort: "year_desc"})
}

// V2Authors — GET /opds/v2/authors: навигация по авторам.
func (h *Handler) V2Authors(w http.ResponseWriter, r *http.Request) {
	page := parsePage(r)
	base := h.baseURL(r)
	limit := h.cfg.PageSize
	items, total, err := h.deps.Catalog.ListAuthors(r.Context(), limit, (page-1)*limit)
	if err != nil {
		h.error(w, "list authors failed", err, http.StatusInternalServerError)
		return
	}
	f := feed2{
		Metadata: meta2{Title: "Авторы", NumberOfItems: total, ItemsPerPage: limit, CurrentPage: page},
		Links:    append(h.links2(base, "/opds/v2/authors"), paging2(base, "/opds/v2/authors", page, total, limit)...),
	}
	for _, a := range items {
		f.Navigation = append(f.Navigation, link2{Href: joinURL(base, fmt.Sprintf("/opds/v2/authors/%d", a.ID)),
			Title: a.FullName, Type: MIMEOPDS2})
	}
	h.writeFeed2(w, f)
}

// V2Series — GET /opds/v2/series: навигация по сериям.
func (h *Handler) V2Series(w http.ResponseWriter, r *http.Request) {
	page := parsePage(r)
	base := h.baseURL(r)
	limit := h.cfg.PageSize
	items, total, err := h.deps.Catalog.ListSeries(r.Context(), limit, (page-1)*limit)
	if err != nil {
		h.error(w, "list series failed", err, http.StatusInternalServerError)
		return
	}
	f := feed2{
		Metadata: meta2{Title: "Серии", NumberOfItems: total, ItemsPerPage: limit, CurrentPage: page},
		Links:    append(h.links2(base, "/opds/v2/series"), paging2(base, "/opds/v2/series", page, total, limit)...),
	}
	for _, s := range items {
		title := s.Title
		if s.AuthorName != "" {
			title = fmt.Sprintf("%s — %s", s.Title, s.AuthorName)
		}
		f.Navigation = append(f.Navigation, link2{Href: joinURL(base, fmt.Sprintf("/opds/v2/series/%d", s.ID)),
			Title: title, Type: MIMEOPDS2})
	}
	h.writeFeed2(w, f)
}

// V2Genres — GET /opds/v2/genres: навигация по жанрам без скрытых.
func (h *Handler) V2Genres(w http.ResponseWriter, r *http.Request) {
	base := h.baseURL(r)
	items, err := h.deps.Catalog.ListGenres(r.Context(), 0)
	if err != nil {
		h.error(w, "list genres failed", err, http.StatusInternalServerError)
		return
	}
	exG, _ := h.exclusions(r)
	f := feed2{Metadata: meta2{Title: "Жанры"}, Links: h.links2(base, "/opds/v2/genres")}
	for _, g := range items {
		if hidden([]string{g.Code}, "", exG, nil) {
			continue
		}
		f.Navigation = append(f.Navigation, link2{Href: joinURL(base, fmt.Sprintf("/opds/v2/genres/%d", g.ID)),
			Title: g.Display, Type: MIMEOPDS2})
	}
	h.writeFeed2(w, f)
}
