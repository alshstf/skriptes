package metadata

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
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
