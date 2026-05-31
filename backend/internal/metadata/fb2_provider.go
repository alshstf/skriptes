package metadata

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html/charset"
)

// Fb2Provider извлекает обложку из самого fb2-файла внутри zip-архива.
//
// Формат fb2:
//
//	<FictionBook>
//	  <description>
//	    <title-info>
//	      <coverpage>
//	        <image l:href="#cover.jpg"/>
//	      </coverpage>
//	    </title-info>
//	  </description>
//	  ...
//	  <binary id="cover.jpg" content-type="image/jpeg">BASE64...</binary>
//	</FictionBook>
//
// Логика:
//  1. находим coverpage → запоминаем id (без '#');
//  2. дочитываем XML до соответствующего <binary> → декодируем base64.
//
// Если coverpage отсутствует, но в файле есть какие-нибудь <binary>
// image/*, в качестве запасного варианта берём первую — некоторые
// авторы не разметили обложку формально, хотя файл с картинкой там есть.
//
// Hit rate на нашей коллекции ожидается ~95%+: fb2 без coverpage
// встречаются, но почти у всех есть хоть какая-то картинка.
type Fb2Provider struct{}

func NewFb2Provider() *Fb2Provider { return &Fb2Provider{} }

func (p *Fb2Provider) Name() string { return "fb2" }

// Local помечает провайдер как не ходящий в сеть (читает обложку и
// аннотацию из нашего же zip-архива). Фоновый прогрев использует только
// такие провайдеры — без rate-limit'ов внешних API. См. localProvider.
func (p *Fb2Provider) Local() bool { return true }

// FetchYears — локально, без сети: год написания произведения
// (<title-info><date>) и год бумажного издания (<publish-info><year>).
// Возвращает 0 для отсутствующего/непарсимого значения.
//
// written БЕЗ fallback на edition: это РАЗНЫЕ сущности. written питает
// статистику по годам написания (и будущую корреляцию с биографией), а
// edition — справочное поле «это издание». Смешивать их нельзя.
//
// document-info/date (когда СДЕЛАН fb2) и src-title-info (инфо об
// оригинале перевода) сознательно игнорируем — это не год произведения.
func (p *Fb2Provider) FetchYears(_ context.Context, q BookQuery) (written int, edition int, err error) {
	if q.ArchivePath == "" || q.FB2Name == "" {
		return 0, 0, ErrNotFound
	}
	rc, err := openFB2(q.ArchivePath, q.FB2Name)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = rc.Close() }()
	w, ed := scanFb2Years(rc)
	return w, ed, nil
}

// FetchAnnotation — текстовое описание книги из тега
// <description><title-info><annotation> внутри fb2.
//
// Извлекаем только текстовое содержимое (включая текст внутри
// inline-тегов вроде <emphasis>, <strong>), параграфы <p> склеиваются
// через "\n\n" — фронт рендерит как whitespace-pre-wrap. Никакого HTML
// в результате, безопасно для рендера.
func (p *Fb2Provider) FetchAnnotation(_ context.Context, q BookQuery) (string, error) {
	if q.ArchivePath == "" || q.FB2Name == "" {
		return "", ErrNotFound
	}
	rc, err := openFB2(q.ArchivePath, q.FB2Name)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()

	text, err := extractFb2Annotation(rc)
	if err != nil {
		return "", fmt.Errorf("scan fb2 annotation: %w", err)
	}
	if text == "" {
		return "", ErrNotFound
	}
	return text, nil
}

func (p *Fb2Provider) FetchCover(_ context.Context, q BookQuery) (*CoverImage, error) {
	if q.ArchivePath == "" || q.FB2Name == "" {
		return nil, ErrNotFound
	}

	rc, err := openFB2(q.ArchivePath, q.FB2Name)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()

	coverID, binaries, err := scanFb2(rc)
	if err != nil {
		return nil, fmt.Errorf("scan fb2: %w", err)
	}

	// Сначала пробуем именно coverpage-binary.
	if coverID != "" {
		if b, ok := binaries[coverID]; ok {
			return decodeBinary(b)
		}
	}
	// Fallback — первый попавшийся image/* binary.
	for _, b := range binaries {
		if strings.HasPrefix(b.contentType, "image/") {
			return decodeBinary(b)
		}
	}
	return nil, ErrNotFound
}

// openFB2 ищет fb2 внутри zip по точному имени и по basename
// (на случай если файл лежит в поддиректории). Дублирует логику
// converter.ExtractFB2, но не зависит от того пакета — здесь нужен
// io.ReadCloser, а не сам результат скачивания.
func openFB2(archivePath, fb2Name string) (io.ReadCloser, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	// Точное имя.
	for _, f := range zr.File {
		if f.Name == fb2Name {
			r, err := f.Open()
			if err != nil {
				_ = zr.Close()
				return nil, fmt.Errorf("open inner: %w", err)
			}
			return &zipCloser{Reader: r, parent: zr}, nil
		}
	}
	// Basename fallback.
	for _, f := range zr.File {
		base := f.Name
		if i := strings.LastIndex(base, "/"); i >= 0 {
			base = base[i+1:]
		}
		if base == fb2Name {
			r, err := f.Open()
			if err != nil {
				_ = zr.Close()
				return nil, fmt.Errorf("open inner: %w", err)
			}
			return &zipCloser{Reader: r, parent: zr}, nil
		}
	}
	_ = zr.Close()
	return nil, fmt.Errorf("%w: fb2 not found in archive", ErrNotFound)
}

// zipCloser держит zip.ReadCloser живым пока caller читает inner-файл.
type zipCloser struct {
	io.Reader
	parent *zip.ReadCloser
}

func (z *zipCloser) Close() error {
	// Reader от zip.File обычно не реализует Close, но на всякий случай.
	if closer, ok := z.Reader.(io.Closer); ok {
		_ = closer.Close()
	}
	return z.parent.Close()
}

type fb2Binary struct {
	id          string
	contentType string
	data        []byte // raw base64 bytes (как лежало в XML, без пробельных символов)
}

// scanFb2 — единственный проход по XML: вытаскивает coverpage href
// и все binary-блоки (мы не знаем заранее, какой нам понадобится).
//
// Для маленьких fb2 (<5 MB) — мгновенно. Для очень больших (десятки
// MB с картинками) загружает только нужные base64-блоки, без копии
// XML целиком.
func scanFb2(r io.Reader) (coverID string, binaries map[string]fb2Binary, err error) {
	dec := xml.NewDecoder(r)
	// fb2 в нашем каталоге часто объявляет encoding="windows-1251"
	// (Lib.rus.ec-наследие). Без CharsetReader stdlib-парсер падает с
	// "declared but Decoder.CharsetReader is nil". charset.NewReaderLabel
	// покрывает все распространённые legacy-кодировки.
	dec.CharsetReader = charset.NewReaderLabel
	binaries = map[string]fb2Binary{}
	var inCoverpage bool

	for {
		tok, terr := dec.Token()
		if terr == io.EOF {
			break
		}
		if terr != nil {
			return "", nil, terr
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := t.Name.Local
			switch name {
			case "coverpage":
				inCoverpage = true
			case "image":
				if inCoverpage {
					for _, a := range t.Attr {
						if a.Name.Local == "href" {
							coverID = strings.TrimPrefix(a.Value, "#")
						}
					}
				}
			case "binary":
				var id, ct string
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "id":
						id = a.Value
					case "content-type":
						ct = a.Value
					}
				}
				if id != "" {
					// CharData в base64; читаем до закрывающего тега.
					data, derr := readCharData(dec)
					if derr != nil {
						return "", nil, derr
					}
					binaries[id] = fb2Binary{id: id, contentType: ct, data: data}
				}
			}
		case xml.EndElement:
			if t.Name.Local == "coverpage" {
				inCoverpage = false
			}
		}
	}
	return coverID, binaries, nil
}

// readCharData читает токены до закрывающего EndElement текущего
// контейнера и собирает CharData. Используется внутри <binary> — там
// между Start и End может быть только base64-текст.
func readCharData(dec *xml.Decoder) ([]byte, error) {
	var out []byte
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.CharData:
			out = append(out, t...)
		case xml.EndElement:
			return out, nil
		}
	}
}

// fb2YearRe выдёргивает 4-значный год (1000–2029) из значения date/year.
// Значения в fb2 бывают разные: ISO ("1869-01-01"), просто год ("1869"),
// свободный текст ("XIX век", "1980-е") — берём первый разумный год либо 0.
var fb2YearRe = regexp.MustCompile(`\b(1[0-9]{3}|20[0-2][0-9])\b`)

func parseFb2Year(s string) int {
	m := fb2YearRe.FindString(s)
	if m == "" {
		return 0
	}
	y, _ := strconv.Atoi(m)
	return y
}

// attrValue — значение атрибута по local-name (без неймспейса).
func attrValue(s xml.StartElement, name string) string {
	for _, a := range s.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// elemText читает текстовое содержимое уже открытого элемента до его
// закрывающего тега (StartElement элемента уже потреблён вызывающим).
// Толерантен к вложенным тегам.
func elemText(dec *xml.Decoder) string {
	var b strings.Builder
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.CharData:
			b.Write(t)
		case xml.StartElement:
			depth++
		case xml.EndElement:
			depth--
		}
	}
	return strings.TrimSpace(b.String())
}

// scanFb2Years — один проход по XML, различает секции <description>:
//
//	title-info/date   → written (value-атрибут ISO либо текст)
//	publish-info/year → edition
//
// Останавливаемся на <body>: вся метаинформация — выше, дальше тело книги.
func scanFb2Years(r io.Reader) (written int, edition int) {
	dec := xml.NewDecoder(r)
	dec.CharsetReader = charset.NewReaderLabel
	dec.Strict = false
	var stack []string
	for {
		tok, terr := dec.Token()
		if terr == io.EOF {
			break
		}
		if terr != nil {
			// Толерантно: битый хвост XML не должен ронять извлечение года —
			// отдаём, что успели собрать выше по файлу.
			return written, edition
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := t.Name.Local
			parent := ""
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			switch {
			case name == "date" && parent == "title-info":
				if written == 0 {
					written = parseFb2Year(attrValue(t, "value"))
					txt := elemText(dec)
					if written == 0 {
						written = parseFb2Year(txt)
					}
				} else {
					_ = elemText(dec)
				}
				continue // элемент потреблён, в stack не кладём
			case name == "year" && parent == "publish-info":
				if edition == 0 {
					edition = parseFb2Year(elemText(dec))
				} else {
					_ = elemText(dec)
				}
				continue
			}
			stack = append(stack, name)
			if name == "body" {
				return written, edition
			}
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	return written, edition
}

func decodeBinary(b fb2Binary) (*CoverImage, error) {
	clean := stripWhitespace(b.data)
	decoded, err := base64.StdEncoding.DecodeString(string(clean))
	if err != nil {
		return nil, fmt.Errorf("decode base64: %w", err)
	}
	mime := b.contentType
	if mime == "" {
		mime = "image/jpeg"
	}
	return &CoverImage{
		Reader:   io.NopCloser(bytes.NewReader(decoded)),
		Mime:     mime,
		SourceID: "fb2:" + b.id,
	}, nil
}

func stripWhitespace(b []byte) []byte {
	out := b[:0]
	for _, c := range b {
		if c == '\n' || c == '\r' || c == '\t' || c == ' ' {
			continue
		}
		out = append(out, c)
	}
	return out
}

// extractFb2Annotation — стримом обходит XML и возвращает plain-text
// аннотации. Структура fb2:
//
//	<description>
//	  <title-info>
//	    <annotation>
//	      <p>Параграф 1...</p>
//	      <p>Параграф <emphasis>с выделением</emphasis>.</p>
//	    </annotation>
//	    ...
//
// Алгоритм: ловим вход в <annotation>, далее каждый <p>-блок собираем
// в одну строку (всё CharData внутри, без тегов), параграфы склеиваем
// через "\n\n". Выход из <annotation> завершает сбор.
//
// Не-fb2 теги внутри annotation (например, у некоторых книг
// сразу текст без <p>) обрабатываем как fallback: накапливаем весь
// CharData до закрытия <annotation>.
func extractFb2Annotation(r io.Reader) (string, error) {
	dec := xml.NewDecoder(r)
	dec.CharsetReader = charset.NewReaderLabel

	var (
		inAnnotation bool
		paragraphs   []string
		curPara      strings.Builder
		inParagraph  bool
		// fallback-буфер для текста-без-<p>
		fallback strings.Builder
	)

	finalize := func() {
		// если был открыт текущий параграф — закрыть его.
		if inParagraph {
			s := strings.TrimSpace(curPara.String())
			if s != "" {
				paragraphs = append(paragraphs, s)
			}
			curPara.Reset()
			inParagraph = false
		}
	}

	for {
		tok, terr := dec.Token()
		if terr == io.EOF {
			break
		}
		if terr != nil {
			return "", terr
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "annotation" {
				inAnnotation = true
				continue
			}
			if !inAnnotation {
				continue
			}
			if t.Name.Local == "p" {
				finalize()
				inParagraph = true
			}
			// Любые другие inline-теги (<emphasis>, <strong>) — игнорим
			// сам тег, но CharData внутри попадёт в curPara через
			// следующий CharData-токен.
		case xml.CharData:
			if !inAnnotation {
				continue
			}
			if inParagraph {
				curPara.Write(t)
			} else {
				fallback.Write(t)
			}
		case xml.EndElement:
			if !inAnnotation {
				continue
			}
			if t.Name.Local == "annotation" {
				finalize()
				// если ни одного <p> не нашли, отдадим fallback-текст
				if len(paragraphs) == 0 {
					if s := strings.TrimSpace(fallback.String()); s != "" {
						paragraphs = append(paragraphs, s)
					}
				}
				// дальше XML может содержать что угодно — нам неинтересно.
				return strings.Join(paragraphs, "\n\n"), nil
			}
			if t.Name.Local == "p" {
				finalize()
			}
		}
	}
	// Никогда не встретили закрытия — отдаём что насобирали.
	finalize()
	if len(paragraphs) == 0 {
		return strings.TrimSpace(fallback.String()), nil
	}
	return strings.Join(paragraphs, "\n\n"), nil
}
