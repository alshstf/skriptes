package opds

import (
	"bytes"
	"encoding/xml"
	"fmt"
)

// MIME-типы — фиксированные строки из OPDS 1.2 спецификации
// (https://specs.opds.io/opds-1.2#section-2). Кладём как константы
// чтобы handler'ы не разъезжались с rendering-кодом.
const (
	// MIMEFeedNavigation — Content-Type для navigation feed (категории/папки).
	MIMEFeedNavigation = "application/atom+xml;profile=opds-catalog;kind=navigation"
	// MIMEFeedAcquisition — Content-Type для acquisition feed (книги).
	MIMEFeedAcquisition = "application/atom+xml;profile=opds-catalog;kind=acquisition"
	// MIMEOpenSearch — OpenSearch description документ.
	MIMEOpenSearch = "application/opensearchdescription+xml"

	// Rel'ы Atom-ссылок. OPDS использует и стандартные Atom-rel'ы
	// (self/start/next/prev), и свои с префиксом http://opds-spec.org.
	RelSelf       = "self"
	RelStart      = "start"
	RelUp         = "up"
	RelNext       = "next"
	RelPrev       = "prev"
	RelFirst      = "first"
	RelLast       = "last"
	RelSearch     = "search"
	RelSubsection = "subsection"

	// OPDS-specific rel'ы для книг и обложек.
	RelAcquisition    = "http://opds-spec.org/acquisition"
	RelAcquisitionOA  = "http://opds-spec.org/acquisition/open-access"
	RelImage          = "http://opds-spec.org/image"
	RelImageThumbnail = "http://opds-spec.org/image/thumbnail"
)

// Feed — корневой элемент Atom XML.
//
// XMLName с пустым namespace важен: encoding/xml пишет xmlns="..."
// атрибут только если задан в struct tag, иначе генерирует
// неконсистентный output. opds:Price, dc:language, opensearch:totalResults
// идут через namespace-префиксы — Go xml encoder использует "Foo" как
// префикс если он не зарегистрирован, мы фиксируем три префикса вручную
// в начале Marshal'а (см. encode ниже).
type Feed struct {
	XMLName xml.Name `xml:"feed"`

	// Атрибуты xmlns* — пишем вручную как Attr; encoding/xml
	// сам не умеет добавлять namespace declarations.
	XMLNS           string `xml:"xmlns,attr"`
	XMLNSOpenSearch string `xml:"xmlns:opensearch,attr,omitempty"`
	XMLNSDC         string `xml:"xmlns:dc,attr,omitempty"`

	ID      string  `xml:"id"`
	Title   string  `xml:"title"`
	Updated string  `xml:"updated"`
	Author  *Person `xml:"author,omitempty"`
	Icon    string  `xml:"icon,omitempty"`
	Links   []Link  `xml:"link"`

	// OpenSearch-пагинация. Заполняется только в acquisition-feed'ах
	// с пагинацией; для navigation обычно пропускается.
	TotalResults int `xml:"opensearch:totalResults,omitempty"`
	ItemsPerPage int `xml:"opensearch:itemsPerPage,omitempty"`
	StartIndex   int `xml:"opensearch:startIndex,omitempty"`

	Entries []Entry `xml:"entry"`
}

// Entry — одна запись в feed'е. Универсальная для navigation/acquisition:
// различие только в наборе Links (для navigation — на под-feed,
// для acquisition — на скачивание + cover).
type Entry struct {
	XMLName xml.Name `xml:"entry"`

	ID      string   `xml:"id"`
	Title   string   `xml:"title"`
	Updated string   `xml:"updated"`
	Authors []Person `xml:"author,omitempty"`

	// dc:language — ISO-код языка (ru/en/…). Для книг.
	Language string `xml:"dc:language,omitempty"`

	// dc:issued — год публикации (если есть). OPDS-клиенты не всегда
	// показывают, но KOReader использует для сортировки.
	Issued string `xml:"dc:issued,omitempty"`

	Categories []Category `xml:"category,omitempty"`

	// Summary — короткое описание (для acquisition: серия+автор).
	// Type обычно "text" — простой plain-text.
	Summary *Text `xml:"summary,omitempty"`
	// Content — длинное описание (для acquisition: аннотация).
	// Type "text" даёт plain-rendering у всех клиентов; "html"
	// поддерживают не все, поэтому не используем.
	Content *Text `xml:"content,omitempty"`

	Links []Link `xml:"link"`
}

// Link — атрибуты Atom-ссылки. Title/Length опциональны и обычно
// заполняются только для acquisition links (Length для прогресс-бара
// скачивания на e-reader'е).
type Link struct {
	XMLName xml.Name `xml:"link"`
	Rel     string   `xml:"rel,attr"`
	Href    string   `xml:"href,attr"`
	Type    string   `xml:"type,attr,omitempty"`
	Title   string   `xml:"title,attr,omitempty"`
	Length  int64    `xml:"length,attr,omitempty"`
}

// Person — author/contributor элемент.
type Person struct {
	Name string `xml:"name"`
	URI  string `xml:"uri,omitempty"`
}

// Category — жанр или тэг. term — машинный код (наш FB2-код),
// label — человекочитаемое имя.
type Category struct {
	XMLName xml.Name `xml:"category"`
	Term    string   `xml:"term,attr"`
	Label   string   `xml:"label,attr,omitempty"`
	Scheme  string   `xml:"scheme,attr,omitempty"`
}

// Text — обёртка вокруг summary/content с type-атрибутом.
type Text struct {
	Type string `xml:"type,attr,omitempty"`
	Body string `xml:",chardata"`
}

// Marshal — сериализует Feed в Atom XML с XML-prolog.
//
// Использует encoding/xml MarshalIndent для читабельного вывода
// (e-reader'ы парсят и сжатый, но при отладке курлом удобнее
// форматированный). Размер OPDS-страницы редко >100 KiB, лишний
// whitespace не критичен.
func Marshal(f *Feed) ([]byte, error) {
	if f.XMLNS == "" {
		f.XMLNS = "http://www.w3.org/2005/Atom"
	}
	if f.XMLNSOpenSearch == "" {
		f.XMLNSOpenSearch = "http://a9.com/-/spec/opensearch/1.1/"
	}
	if f.XMLNSDC == "" {
		f.XMLNSDC = "http://purl.org/dc/terms/"
	}
	body, err := xml.MarshalIndent(f, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal opds feed: %w", err)
	}
	var buf bytes.Buffer
	buf.WriteString(xml.Header) // <?xml version="1.0" encoding="UTF-8"?>\n
	buf.Write(body)
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}
