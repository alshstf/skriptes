// Package booktext отдаёт текст книги по разделам — тем же, что видит ридер.
//
// Источник — epub, который fb2cng собрал для ридера (`/cache/converted`):
// порядок разделов (spine) там тот же, что у foliate, поэтому место читателя
// «раздел N, такой-то фрагмент» однозначно находится в тексте, и не нужно
// сопоставлять fb2 с epub. На этом строятся ИИ-функции читалки: модели уходит
// только прочитанная часть (план — ~/projects/plans/skriptes/ai-roadmap.md, 0.3).
package booktext

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Section — раздел книги (элемент spine).
type Section struct {
	// Index — позиция в spine, считая и нелинейные элементы (обложку): это тот
	// же номер раздела, что у ридера (foliate `sections[index]`, CFI `/6/2(i+1)`).
	Index int
	Href  string
	Title string // <title> раздела, иначе первый заголовок
	// Text — текст без разметки: абзацы и заголовки разделены переводом строки,
	// пробелы внутри схлопнуты.
	Text string
	// Notes — раздел сносок (fb2cng: footnote-body); в «прочитанное» не входит.
	Notes bool
	// Linear — false у обложки и прочих `linear="no"`.
	Linear bool
}

// Book — разобранная книга.
type Book struct {
	// Version — хеш файла epub: ключ кэшей по тексту (пересказы и т. п.).
	// Пересобрали epub (новый fb2cng, другой файл) — версия другая.
	Version  string
	Sections []Section
}

// ErrNotEPUB — файл не похож на epub (нет container.xml или OPF).
var ErrNotEPUB = errors.New("booktext: not an epub")

// Open читает epub по пути.
func Open(epubPath string) (*Book, error) {
	f, err := os.Open(epubPath) // #nosec G304 — путь из cacheRoot конвертера
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(f, st.Size())
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotEPUB, err)
	}
	b, err := read(zr)
	if err != nil {
		return nil, err
	}
	b.Version = hex.EncodeToString(h.Sum(nil))[:16]
	return b, nil
}

func read(zr *zip.Reader) (*Book, error) {
	files := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		files[f.Name] = f
	}
	opfPath, err := rootfile(files)
	if err != nil {
		return nil, err
	}
	var pkg struct {
		Manifest []struct {
			ID   string `xml:"id,attr"`
			Href string `xml:"href,attr"`
		} `xml:"manifest>item"`
		Spine []struct {
			IDRef  string `xml:"idref,attr"`
			Linear string `xml:"linear,attr"`
		} `xml:"spine>itemref"`
	}
	if err := decodeXML(files[opfPath], &pkg); err != nil {
		return nil, fmt.Errorf("%w: opf: %v", ErrNotEPUB, err)
	}
	hrefs := make(map[string]string, len(pkg.Manifest))
	for _, it := range pkg.Manifest {
		hrefs[it.ID] = it.Href
	}
	base := path.Dir(opfPath)
	b := &Book{}
	for i, ref := range pkg.Spine {
		s := Section{Index: i, Href: hrefs[ref.IDRef], Linear: ref.Linear != "no"}
		if f := files[path.Join(base, s.Href)]; f != nil && s.Href != "" {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("open %s: %w", s.Href, err)
			}
			s.Title, s.Text, s.Notes = extract(rc)
			_ = rc.Close()
		}
		b.Sections = append(b.Sections, s)
	}
	if len(b.Sections) == 0 {
		return nil, fmt.Errorf("%w: empty spine", ErrNotEPUB)
	}
	return b, nil
}

func rootfile(files map[string]*zip.File) (string, error) {
	var c struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := decodeXML(files["META-INF/container.xml"], &c); err != nil || len(c.Rootfiles) == 0 {
		return "", fmt.Errorf("%w: container.xml", ErrNotEPUB)
	}
	if files[c.Rootfiles[0].FullPath] == nil {
		return "", fmt.Errorf("%w: no %s", ErrNotEPUB, c.Rootfiles[0].FullPath)
	}
	return c.Rootfiles[0].FullPath, nil
}

func decodeXML(f *zip.File, v any) error {
	if f == nil {
		return errors.New("missing file")
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	return xml.NewDecoder(rc).Decode(v)
}

// blockTags — элементы, после которых в тексте идёт перевод строки.
var blockTags = map[string]bool{
	"p": true, "div": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"li": true, "br": true, "tr": true, "blockquote": true, "section": true, "pre": true, "dt": true, "dd": true,
	"figcaption": true, "hr": true,
}

// extract — заголовок и текст раздела из XHTML. Разбор нестрогий (сущности
// HTML, незакрытые теги): битая разметка в одной книге не должна ронять ответ.
func extract(r io.Reader) (title, text string, notes bool) {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	dec.AutoClose = xml.HTMLAutoClose
	dec.Entity = xml.HTMLEntity

	var out strings.Builder
	var heading strings.Builder
	var docTitle strings.Builder
	inBody, skip, inTitle, inHeading, headingDone := false, 0, false, false, false
	space := false // перед следующим словом нужен пробел
	newline := func() {
		if s := out.String(); s != "" && !strings.HasSuffix(s, "\n") {
			out.WriteByte('\n')
		}
		space = false
	}
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := strings.ToLower(t.Name.Local)
			switch {
			case name == "body":
				inBody = true
			case name == "title" && !inBody:
				inTitle = true
			case name == "script" || name == "style" || name == "svg":
				skip++
			case inBody && !headingDone && len(name) == 2 && name[0] == 'h' && name[1] >= '1' && name[1] <= '6':
				inHeading = true
			}
			if inBody && name == "div" && !notes {
				for _, a := range t.Attr {
					if a.Name.Local == "class" && strings.Contains(a.Value, "footnote-body") {
						notes = true
					}
				}
			}
			if inBody && blockTags[name] {
				newline()
			}
		case xml.EndElement:
			name := strings.ToLower(t.Name.Local)
			switch {
			case name == "title":
				inTitle = false
			case name == "script" || name == "style" || name == "svg":
				if skip > 0 {
					skip--
				}
			case inHeading && len(name) == 2 && name[0] == 'h':
				inHeading, headingDone = false, true
			}
			if inBody && blockTags[name] {
				newline()
			}
		case xml.CharData:
			if skip > 0 {
				continue
			}
			if inTitle {
				docTitle.Write(t)
				continue
			}
			if !inBody {
				continue
			}
			if inHeading {
				heading.Write(t)
			}
			writeCollapsed(&out, string(t), &space)
		}
	}
	text = strings.TrimSpace(out.String())
	title = strings.Join(strings.Fields(docTitle.String()), " ")
	if title == "" {
		title = strings.Join(strings.Fields(heading.String()), " ")
	}
	return title, text, notes
}

// writeCollapsed дописывает текст, схлопывая пробельные символы (включая
// неразрывный пробел) в один пробел и выбрасывая мягкие переносы.
func writeCollapsed(out *strings.Builder, s string, space *bool) {
	for _, r := range s {
		switch {
		case r == '­':
		case isSpace(r):
			*space = true
		default:
			if *space && out.Len() > 0 && !strings.HasSuffix(out.String(), "\n") {
				out.WriteByte(' ')
			}
			*space = false
			out.WriteRune(r)
		}
	}
}

func isSpace(r rune) bool { return unicode.IsSpace(r) || r == ' ' || r == ' ' || r == ' ' }

// Runes — длина читаемого текста книги в знаках (без сносок и нелинейных разделов).
func (b *Book) Runes() int {
	n := 0
	for _, s := range b.Sections {
		if s.Linear && !s.Notes {
			n += utf8.RuneCountInString(s.Text)
		}
	}
	return n
}

// Flat — читаемый текст книги одной строкой: линейные разделы без сносок
// подряд, между разделами пустая строка. Место в книге — одно число (смещение
// в байтах Text), а не пара «раздел, смещение»: так режутся окна и куски,
// не зависящие от того, как fb2cng нарезал разделы (бывает раздел на 400 тыс.
// знаков, бывает на 12).
type Flat struct {
	Text   string
	starts map[int]int // Section.Index → смещение начала раздела в Text
	order  []int       // индексы вошедших разделов по порядку
}

// Flat собирает читаемый текст книги.
func (b *Book) Flat() *Flat {
	f := &Flat{starts: map[int]int{}}
	var sb strings.Builder
	for _, s := range b.Sections {
		if !s.Linear || s.Notes || s.Text == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		f.starts[s.Index] = sb.Len()
		f.order = append(f.order, s.Index)
		sb.WriteString(s.Text)
	}
	f.Text = sb.String()
	return f
}

// Offset переводит место «раздел, смещение в его тексте» в смещение в Text.
// Раздел без текста (обложка, сноски) — начало следующего читаемого раздела;
// смещение за пределами раздела — его конец; offset < 0 — конец раздела.
func (f *Flat) Offset(section, offset int) int {
	start, ok := f.starts[section]
	if !ok {
		for _, idx := range f.order {
			if idx > section {
				return f.starts[idx]
			}
		}
		return len(f.Text)
	}
	end := len(f.Text)
	for i, idx := range f.order {
		if idx == section && i+1 < len(f.order) {
			end = f.starts[f.order[i+1]] - 2 // без пустой строки между разделами
		}
	}
	if offset < 0 || start+offset > end {
		return end
	}
	return alignRune(f.Text, start+offset)
}

// Section — индекс раздела, в который попадает смещение.
func (f *Flat) Section(off int) int {
	sec := -1
	for _, idx := range f.order {
		if f.starts[idx] > off {
			break
		}
		sec = idx
	}
	return sec
}

// Window — текст перед местом upto: не больше maxRunes знаков, с начала абзаца
// (если абзац сам длиннее окна — с границы слова). Возвращает и смещение начала.
func (f *Flat) Window(upto, maxRunes int) (string, int) {
	upto = alignRune(f.Text, min(max(upto, 0), len(f.Text)))
	start := upto
	for n := 0; start > 0 && n < maxRunes; n++ {
		_, size := utf8.DecodeLastRuneInString(f.Text[:start])
		start -= size
	}
	if start > 0 {
		// Вперёд до начала абзаца; нет его в окне — до начала слова.
		if nl := strings.IndexByte(f.Text[start:upto], '\n'); nl >= 0 && start+nl+1 < upto {
			start += nl + 1
		} else if sp := strings.IndexByte(f.Text[start:upto], ' '); sp >= 0 {
			start += sp + 1
		}
	}
	return f.Text[start:upto], start
}

// Span — кусок Text: [Start, End).
type Span struct{ Start, End int }

// Chunks режет текст на куски примерно по maxRunes знаков по границам абзацев
// (абзац длиннее куска не рвётся — кусок выйдет больше). Нарезка зависит только
// от текста и maxRunes, поэтому номер куска — устойчивый ключ кэша пересказов.
func (f *Flat) Chunks(maxRunes int) []Span {
	var out []Span
	start, runes := 0, 0
	for i, r := range f.Text {
		runes++
		if r != '\n' || runes < maxRunes {
			continue
		}
		out = append(out, Span{start, i + 1})
		start, runes = i+1, 0
	}
	if start < len(f.Text) {
		out = append(out, Span{start, len(f.Text)})
	}
	return out
}

// alignRune сдвигает смещение назад к границе руны.
func alignRune(s string, off int) int {
	if off >= len(s) {
		return len(s)
	}
	for off > 0 && !utf8.RuneStart(s[off]) {
		off--
	}
	return off
}

// Locate ищет в тексте раздела фрагмент, который читатель выделил в ридере.
// Браузер отдаёт выделение со своими пробелами и переводами строк, поэтому
// сравнение идёт по тексту со схлопнутыми пробелами. hint (0..1) — примерная
// доля раздела, где фрагмент ждут: из нескольких вхождений берётся ближайшее.
// Возвращает границы в байтах text; ok=false — фрагмента в разделе нет.
func Locate(text, excerpt string, hint float64) (start, end int, ok bool) {
	nt, idx := normalize(text)
	ne, _ := normalize(excerpt)
	if ne == "" || nt == "" {
		return 0, 0, false
	}
	probe := ne
	if !strings.Contains(nt, probe) {
		// Выделение захватило больше, чем есть в разделе (обрезка до 2000 знаков,
		// переход через границу раздела) — берём самое длинное начало фрагмента,
		// которое в разделе есть. «Начало длины n нашлось» монотонно по n, поэтому
		// двоичный поиск; короче minProbe знаков — случайное совпадение, не берём.
		runes := []rune(ne)
		lo, hi := 0, len(runes)-1
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if strings.Contains(nt, strings.TrimSpace(string(runes[:mid]))) {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		if lo < minProbe {
			return 0, 0, false
		}
		probe = strings.TrimSpace(string(runes[:lo]))
	}
	s, e, found := nearest(nt, probe, hint)
	if !found {
		return 0, 0, false
	}
	// Конец — сразу за последней руной совпадения, без пробелов после неё.
	last := idx[e-1]
	_, size := utf8.DecodeRuneInString(text[last:])
	return idx[s], last + size, true
}

// minProbe — наименьшая длина начала фрагмента (в знаках), по которой ещё можно
// судить о месте.
const minProbe = 12

// nearest — вхождение probe в nt, ближайшее к hint; границы в байтах nt.
func nearest(nt, probe string, hint float64) (int, int, bool) {
	want := int(hint * float64(len(nt)))
	best, bestDist := -1, 0
	for from := 0; from <= len(nt)-len(probe); {
		i := strings.Index(nt[from:], probe)
		if i < 0 {
			break
		}
		at := from + i
		if d := abs(at - want); best < 0 || d < bestDist {
			best, bestDist = at, d
		}
		from = at + 1
	}
	if best < 0 {
		return 0, 0, false
	}
	return best, best + len(probe), true
}

// normalize — текст со схлопнутыми пробелами (переводы строк тоже пробел) и
// карта: байт нормализованного текста → начало его руны в исходном.
func normalize(s string) (string, []int) {
	var b strings.Builder
	idx := make([]int, 0, len(s))
	space := false
	for i, r := range s {
		if r == '­' {
			continue
		}
		if isSpace(r) {
			space = b.Len() > 0
			continue
		}
		if space {
			idx = append(idx, i)
			b.WriteByte(' ')
			space = false
		}
		for k := 0; k < utf8.RuneLen(r); k++ {
			idx = append(idx, i)
		}
		b.WriteRune(r)
	}
	return b.String(), idx
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
