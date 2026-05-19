package opds

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/catalog"
)

// BookToEntry — конвертирует books.Book в acquisition-entry.
//
// baseURL — origin без trailing slash (например "https://skriptes.localhost").
// Все link'и в entry — абсолютные URL, чтобы e-reader без дополнительной
// нормализации мог скачать файл. KOReader относительные пути обрабатывает
// корректно, но Moon+Reader и старый FBReader — нет, поэтому отдаём
// абсолютные на всякий случай.
//
// formats — список форматов, для которых сгенерировать acquisition links
// (epub3 + что захочет пользователь через query-параметр). По умолчанию
// один epub3 — он принимается всеми устройствами включая Kindle.
//
// coverMIME — реальный mime обложки взять неоткуда: имя файла в
// cover_path содержит расширение, мапим расширение на mime. Для
// неизвестных расширений используем image/jpeg как fallback (большинство
// обложек jpeg).
func BookToEntry(b books.Book, baseURL string, formats []FormatLink) Entry {
	e := Entry{
		ID:      bookURN(b.ID),
		Title:   b.Title,
		Updated: bookUpdated(b),
	}
	for _, a := range b.Authors {
		e.Authors = append(e.Authors, Person{Name: a.FullName})
	}
	if b.Lang != "" {
		e.Language = b.Lang
	}
	for _, g := range b.Genres {
		e.Categories = append(e.Categories, Category{
			Term:   g.Code,
			Label:  g.Display,
			Scheme: "https://specs.opds.io/opds-1.2#category-scheme",
		})
	}
	if b.Annotation != "" {
		e.Content = &Text{Type: "text", Body: b.Annotation}
	}
	// Summary — короткая строка вида "Серия / №2 · Размер 412 KiB".
	// Многие OPDS-клиенты показывают summary в списке книг, content —
	// только в детальном просмотре. Без summary entry визуально пустая.
	e.Summary = &Text{Type: "text", Body: bookSummary(b)}

	// Обложки (полная и thumbnail — у нас один файл, но OPDS требует
	// два rel'а для совместимости со старыми клиентами).
	if b.CoverPath != "" {
		coverURL := joinURL(baseURL, "/opds/covers/"+b.CoverPath)
		mime := guessImageMIME(b.CoverPath)
		e.Links = append(e.Links,
			Link{Rel: RelImage, Href: coverURL, Type: mime},
			Link{Rel: RelImageThumbnail, Href: coverURL, Type: mime},
		)
	}

	// Acquisition links — по одному на формат.
	for _, fmt := range formats {
		e.Links = append(e.Links, Link{
			Rel:   RelAcquisition,
			Href:  joinURL(baseURL, fmt.HrefPath),
			Type:  fmt.MIME,
			Title: fmt.Title,
		})
	}

	return e
}

// FormatLink — описание одного acquisition-link для книги.
type FormatLink struct {
	HrefPath string // относительный путь, например "/opds/books/123/download?format=epub3"
	MIME     string // application/epub+zip и т.п.
	Title    string // человекочитаемое имя — "Скачать EPUB"
}

// ListItemToEntry — упрощённая версия BookToEntry для записей из
// books.ListItem (без annotation, без архивных деталей). Используется
// в feed'ах "Книги автора" / "Книги серии" — там у нас нет полной
// карточки, только список из Meili.
//
// Acquisition-link всё равно нужен (e-reader должен иметь возможность
// скачать прямо из списка); поэтому передаём ту же FormatLink, но
// HrefPath строится снаружи на основе ID.
func ListItemToEntry(it books.ListItem, baseURL string, makeFormats func(bookID int64) []FormatLink) Entry {
	e := Entry{
		ID:      bookURN(it.ID),
		Title:   it.Title,
		Updated: nowISO(),
	}
	for _, a := range it.Authors {
		e.Authors = append(e.Authors, Person{Name: a})
	}
	if it.Lang != "" {
		e.Language = it.Lang
	}
	if it.Year != nil {
		e.Issued = strconv.Itoa(*it.Year)
	}
	for _, g := range it.Genres {
		e.Categories = append(e.Categories, Category{Term: g, Label: g})
	}
	// Summary в ListItem строим короче — есть только Series/SerNo.
	if it.Series != "" {
		s := it.Series
		if it.SerNo != nil {
			s = fmt.Sprintf("%s · #%d", s, *it.SerNo)
		}
		e.Summary = &Text{Type: "text", Body: s}
	}
	for _, f := range makeFormats(it.ID) {
		e.Links = append(e.Links, Link{
			Rel:   RelAcquisition,
			Href:  joinURL(baseURL, f.HrefPath),
			Type:  f.MIME,
			Title: f.Title,
		})
	}
	return e
}

// AuthorEntryToEntry — навигационная запись для feed'а авторов.
// Href ведёт на /opds/authors/{id} (acquisition feed с книгами автора).
func AuthorEntryToEntry(a catalog.AuthorEntry, baseURL string) Entry {
	return Entry{
		ID:      fmt.Sprintf("urn:skriptes:author:%d", a.ID),
		Title:   a.FullName,
		Updated: nowISO(),
		Summary: &Text{Type: "text", Body: fmt.Sprintf("%d книг", a.BookCount)},
		Links: []Link{{
			Rel:   RelSubsection,
			Href:  joinURL(baseURL, fmt.Sprintf("/opds/authors/%d", a.ID)),
			Type:  MIMEFeedAcquisition,
			Title: "Книги автора",
		}},
	}
}

// SeriesEntryToEntry — навигационная запись для feed'а серий.
func SeriesEntryToEntry(s catalog.SeriesEntry, baseURL string) Entry {
	title := s.Title
	if s.AuthorName != "" {
		title = fmt.Sprintf("%s — %s", s.Title, s.AuthorName)
	}
	return Entry{
		ID:      fmt.Sprintf("urn:skriptes:series:%d", s.ID),
		Title:   title,
		Updated: nowISO(),
		Summary: &Text{Type: "text", Body: fmt.Sprintf("%d книг", s.BookCount)},
		Links: []Link{{
			Rel:   RelSubsection,
			Href:  joinURL(baseURL, fmt.Sprintf("/opds/series/%d", s.ID)),
			Type:  MIMEFeedAcquisition,
			Title: "Книги серии",
		}},
	}
}

// GenreEntryToEntry — навигационная запись для feed'а жанров.
func GenreEntryToEntry(g catalog.GenreEntry, baseURL string) Entry {
	return Entry{
		ID:      fmt.Sprintf("urn:skriptes:genre:%d", g.ID),
		Title:   g.Display,
		Updated: nowISO(),
		Summary: &Text{Type: "text", Body: fmt.Sprintf("%d книг", g.BookCount)},
		Links: []Link{{
			Rel:   RelSubsection,
			Href:  joinURL(baseURL, fmt.Sprintf("/opds/genres/%d", g.ID)),
			Type:  MIMEFeedAcquisition,
			Title: "Книги жанра",
		}},
	}
}

// --- internal helpers ---

func bookURN(id int64) string { return fmt.Sprintf("urn:skriptes:book:%d", id) }

// bookUpdated — отметка времени для <updated>. Используем date_added
// (DATE из INPX, без часового пояса) если есть, иначе — текущее время
// (это даст ложно-свежий entry, но это лучше чем пустое поле, обязательное
// в Atom).
func bookUpdated(b books.Book) string {
	if b.DateAdded != nil {
		return b.DateAdded.Format(time.RFC3339)
	}
	return nowISO()
}

// bookSummary — короткая строка для атома summary.
func bookSummary(b books.Book) string {
	var parts []string
	if b.Series != nil {
		s := b.Series.Title
		if b.SerNo != nil {
			s = fmt.Sprintf("%s · #%d", s, *b.SerNo)
		}
		parts = append(parts, s)
	}
	if b.SizeBytes > 0 {
		parts = append(parts, fmt.Sprintf("%.1f KiB", float64(b.SizeBytes)/1024))
	}
	return strings.Join(parts, " · ")
}

// nowISO — текущее время в RFC3339 (Atom <updated> требует именно его).
func nowISO() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// joinURL — конкатенация origin'а и абсолютного path с trim'ом
// слэшей. Не используем net/url.URL потому что path уже валидный.
func joinURL(base, path string) string {
	return strings.TrimRight(base, "/") + path
}

// guessImageMIME — мапит расширение файла обложки на mime. У нас
// в кэше хранятся jpg/png/webp/gif, выбор делается по расширению
// (нет нужды декодировать magic bytes).
func guessImageMIME(filename string) string {
	switch {
	case strings.HasSuffix(filename, ".png"):
		return "image/png"
	case strings.HasSuffix(filename, ".webp"):
		return "image/webp"
	case strings.HasSuffix(filename, ".gif"):
		return "image/gif"
	default:
		return "image/jpeg"
	}
}
