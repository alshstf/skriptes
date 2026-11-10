package booktext

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const xhtmlHead = `<?xml version="1.0" encoding="UTF-8"?><!DOCTYPE html><html xmlns="http://www.w3.org/1999/xhtml"><head><title>`

// writeEPUB собирает epub в том виде, как его делает fb2cng: OEBPS/content.opf,
// обложка `linear="no"` первой в spine, разделы, сноски последним разделом.
func writeEPUB(t *testing.T, sections map[string]string, spine []string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "book.epub")
	f, err := os.Create(p)
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	add := func(name, body string) {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(body))
		require.NoError(t, err)
	}
	add("mimetype", "application/epub+zip")
	add("META-INF/container.xml", `<?xml version="1.0"?><container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container"><rootfiles><rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/></rootfiles></container>`)
	var manifest, refs strings.Builder
	for _, id := range spine {
		manifest.WriteString(`<item id="` + id + `" href="` + id + `.xhtml" media-type="application/xhtml+xml"/>`)
		linear := ""
		if id == "cover" {
			linear = ` linear="no"`
		}
		refs.WriteString(`<itemref idref="` + id + `"` + linear + `/>`)
		add("OEBPS/"+id+".xhtml", sections[id])
	}
	add("OEBPS/content.opf", `<?xml version="1.0"?><package xmlns="http://www.idpf.org/2007/opf" version="3.0"><manifest>`+manifest.String()+`</manifest><spine>`+refs.String()+`</spine></package>`)
	require.NoError(t, zw.Close())
	require.NoError(t, f.Close())
	return p
}

func testBook(t *testing.T) *Book {
	t.Helper()
	b, err := Open(writeEPUB(t, map[string]string{
		"cover": xhtmlHead + `Обложка</title></head><body><svg><text>не текст</text></svg></body></html>`,
		"ch1": xhtmlHead + `Глава первая</title><style>p{color:red}</style></head><body><div class="section"><h2><span>Глава первая</span></h2>` +
			`<p>Гарри&nbsp;нёс   дочь на&#173;плечах.</p><p>— Папа, он <em>опять</em> это говорит<a href="#n1"><sup>1</sup></a>…</p></div></body></html>`,
		"ch2":   xhtmlHead + `</title></head><body><h3>Глава вторая</h3><p>Поезд ушёл.</p><p>Поезд ушёл. И снова пришёл.</p><br/><p>Конец.</p></body></html>`,
		"notes": xhtmlHead + `Примечания</title></head><body><div class="footnote-body"><p>1 Сноска.</p></div></body></html>`,
	}, []string{"cover", "ch1", "ch2", "notes"}))
	require.NoError(t, err)
	return b
}

func TestOpen_Sections(t *testing.T) {
	b := testBook(t)
	require.Len(t, b.Version, 16)
	require.Len(t, b.Sections, 4)

	cover, ch1, ch2, notes := b.Sections[0], b.Sections[1], b.Sections[2], b.Sections[3]
	require.False(t, cover.Linear)
	require.Empty(t, cover.Text, "svg обложки — не текст")

	// Номер раздела — позиция в spine вместе с обложкой: так считает и ридер.
	require.Equal(t, 1, ch1.Index)
	require.Equal(t, "Глава первая", ch1.Title)
	require.Equal(t, "Глава первая\nГарри нёс дочь наплечах.\n— Папа, он опять это говорит1…", ch1.Text,
		"абзацы — строками, пробелы схлопнуты, &nbsp; — пробел, мягкий перенос выброшен, стили не текст")

	require.Equal(t, "Глава вторая", ch2.Title, "без <title> — первый заголовок")
	require.Equal(t, "Глава вторая\nПоезд ушёл.\nПоезд ушёл. И снова пришёл.\nКонец.", ch2.Text)

	require.True(t, notes.Notes)
	require.Equal(t, len([]rune(ch1.Text))+len([]rune(ch2.Text)), b.Runes(), "сноски и обложка в длину не входят")
}

func TestFlat(t *testing.T) {
	b := testBook(t)
	f := b.Flat()
	ch1, ch2 := b.Sections[1].Text, b.Sections[2].Text
	require.Equal(t, ch1+"\n\n"+ch2, f.Text, "обложка без текста и сноски не входят")

	at := strings.Index(ch2, "И снова")
	off := f.Offset(2, at)
	require.Equal(t, ch1+"\n\nГлава вторая\nПоезд ушёл.\nПоезд ушёл. ", f.Text[:off], "прочитанное — всё до места чтения")
	require.Equal(t, off, f.Offset(2, at+1), "смещение внутри руны — назад к её границе")

	require.Equal(t, len(ch1), f.Offset(1, -1), "без смещения — конец раздела")
	require.Equal(t, len(ch1), f.Offset(1, 1<<20), "за пределами — конец раздела, не начало следующего")
	require.Equal(t, 0, f.Offset(0, 5), "обложка — начало первого читаемого раздела")
	require.Equal(t, len(f.Text), f.Offset(3, 0), "сноски — конец книги")

	require.Equal(t, 1, f.Section(0))
	require.Equal(t, 1, f.Section(len(ch1)))
	require.Equal(t, 2, f.Section(off))
}

func TestFlat_Window(t *testing.T) {
	f := &Flat{Text: "Первый абзац.\nВторой абзац подлиннее.\nТретий."}
	upto := strings.Index(f.Text, "Третий")

	w, start := f.Window(upto, 1000)
	require.Equal(t, "Первый абзац.\nВторой абзац подлиннее.\n", w, "окно больше текста — всё до места")
	require.Zero(t, start)

	w, start = f.Window(upto, 30)
	require.Equal(t, "Второй абзац подлиннее.\n", w, "с начала абзаца, а не с середины предыдущего")
	require.Equal(t, strings.Index(f.Text, "Второй"), start)

	w, _ = f.Window(upto, 12)
	require.Equal(t, "подлиннее.\n", w, "абзац длиннее окна — с границы слова")

	w, _ = f.Window(len(f.Text)+10, 7)
	require.Equal(t, "Третий.", w)
}

func TestFlat_Chunks(t *testing.T) {
	para := strings.Repeat("слово ", 20) + "конец.\n" // 127 знаков
	f := &Flat{Text: strings.Repeat(para, 10)}
	chunks := f.Chunks(300)
	require.Len(t, chunks, 4, "по три абзаца в куске, последний — хвост")
	require.Zero(t, chunks[0].Start)
	for i, c := range chunks {
		require.True(t, strings.HasSuffix(f.Text[c.Start:c.End], "конец.\n"), "кусок %d кончается на границе абзаца", i)
		if i > 0 {
			require.Equal(t, chunks[i-1].End, c.Start, "без дыр и нахлёстов")
		}
	}
	require.Equal(t, len(f.Text), chunks[3].End)
	require.Equal(t, chunks, f.Chunks(300), "нарезка детерминирована")
	require.Empty(t, (&Flat{}).Chunks(300))
}

func TestLocate(t *testing.T) {
	text := "Глава вторая\nПоезд ушёл.\nПоезд ушёл. И снова пришёл.\nКонец."

	// Браузер отдаёт выделение со своими пробелами и переводами строк.
	s, e, ok := Locate(text, "ушёл.\n\n  И снова пришёл", 0.9)
	require.True(t, ok)
	require.Equal(t, "ушёл. И снова пришёл", text[s:e])

	// Два вхождения — берётся ближайшее к подсказке.
	s, _, ok = Locate(text, "Поезд ушёл.", 0)
	require.True(t, ok)
	first := strings.Index(text, "Поезд")
	require.Equal(t, first, s)
	s, _, ok = Locate(text, "Поезд ушёл.", 0.6)
	require.True(t, ok)
	require.Equal(t, strings.LastIndex(text, "Поезд"), s)

	// Выделение через перевод строки между абзацами.
	s, e, ok = Locate(text, "вторая Поезд", 0)
	require.True(t, ok)
	require.Equal(t, "вторая\nПоезд", text[s:e])

	// Выделение длиннее раздела (зацепило следующий) — находим по началу.
	long := "И снова пришёл. Конец. " + strings.Repeat("а что было в следующем разделе, тут уже нет ", 5)
	s, _, ok = Locate(text, long, 0.5)
	require.True(t, ok)
	require.Equal(t, strings.Index(text, "И снова"), s)

	_, _, ok = Locate(text, "этого в разделе нет", 0.5)
	require.False(t, ok)
	_, _, ok = Locate(text, "   ", 0.5)
	require.False(t, ok)
}

func TestOpen_NotEPUB(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.epub")
	require.NoError(t, os.WriteFile(p, []byte("не zip"), 0o600))
	_, err := Open(p)
	require.ErrorIs(t, err, ErrNotEPUB)
}
