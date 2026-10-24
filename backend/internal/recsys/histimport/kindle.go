package histimport

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Kindle «My Clippings.txt»: записи через «==========», в записи — «Название
// (Автор)», мета-строка («- Your Highlight on page 12 | Location 123-125 |
// Added on …» или «– Ваш выделенный отрывок … | Место 123–125 | Добавлено: …»),
// пустая строка, текст. Берём подчёркивания и заметки; закладки (места Kindle —
// не наши позиции) и «прочитано» (подчёркивал ≠ дочитал) — нет.

const kindleSeparator = "=========="

type clipKind int

const (
	clipUnknown clipKind = iota
	clipHighlight
	clipNote
	clipBookmark
)

// clip — одна запись файла.
type clip struct {
	kind     clipKind
	from, to int // место Kindle (Location); 0 — неизвестно (страниц не берём: на странице много выделений)
	at       *time.Time
	text     string
}

func parseKindle(data string) *Result {
	res := &Result{Source: SourceKindle}
	data = strings.ReplaceAll(strings.ReplaceAll(data, "\r\n", "\n"), "\r", "\n")
	type book struct {
		item  Item
		clips []clip
	}
	var books []*book
	byKey := map[string]*book{}
	n := 0
	for _, block := range splitKindleBlocks(data) {
		n++
		lines := block
		for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
			lines = lines[1:]
		}
		if len(lines) == 0 {
			continue
		}
		if len(lines) < 2 {
			res.skip(n, "неполная запись")
			continue
		}
		c, ok := parseKindleMeta(lines[1])
		if !ok {
			res.skip(n, "не распознана строка записи: "+clipForMessage(lines[1]))
			continue
		}
		if c.kind == clipBookmark {
			continue
		}
		c.text = strings.TrimSpace(strings.Join(lines[2:], "\n"))
		if c.text == "" || isClippingLimit(c.text) {
			continue
		}
		title, author := splitKindleTitle(strings.TrimSpace(strings.TrimPrefix(lines[0], "\ufeff")))
		if title == "" {
			res.skip(n, "нет названия книги")
			continue
		}
		it := Item{Title: title, Authors: splitAuthors(author), Line: n}
		k := bookKey(it)
		b := byKey[k]
		if b == nil {
			b = &book{item: it}
			byKey[k] = b
			books = append(books, b)
		}
		b.clips = append(b.clips, c)
	}
	for _, b := range books {
		b.item.Highlights = kindleHighlights(b.clips)
		if len(b.item.Highlights) > 0 {
			res.Items = append(res.Items, b.item)
		}
	}
	return res
}

// splitKindleBlocks — записи между разделителями (BOM бывает и посреди
// склеенного файла).
func splitKindleBlocks(data string) [][]string {
	var (
		out [][]string
		cur []string
	)
	for _, l := range strings.Split(data, "\n") {
		if strings.TrimSpace(strings.TrimPrefix(l, "\ufeff")) == kindleSeparator {
			out = append(out, cur)
			cur = nil
			continue
		}
		cur = append(cur, l)
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// kindleHighlights — подчёркивания книги: правка выделения в Kindle дописывает
// новую запись, старая остаётся — из пересекающихся по месту берём последнюю;
// заметка садится к выделению, которое кончается на её месте, иначе — сама по
// себе.
func kindleHighlights(clips []clip) []Highlight {
	var hs []clip
	for _, c := range clips {
		if c.kind != clipHighlight {
			continue
		}
		hs = slices.DeleteFunc(hs, func(h clip) bool { return sameHighlight(h, c) })
		hs = append(hs, c)
	}
	out := make([]Highlight, len(hs))
	for i, h := range hs {
		out[i] = Highlight{Text: h.text, At: h.at}
	}
	for _, c := range clips {
		if c.kind != clipNote {
			continue
		}
		paired := false
		for i := len(hs) - 1; i >= 0; i-- {
			if c.from > 0 && hs[i].to == c.from && out[i].Note == "" {
				out[i].Note = c.text
				paired = true
				break
			}
		}
		if !paired {
			out = append(out, Highlight{Note: c.text, At: c.at})
		}
	}
	return out
}

// sameHighlight — b — правка выделения a: места пересекаются; без мест (у
// KOReader их нет) — тот же текст или b его расширяет (короткие не сравниваем:
// слово «да» есть во многих цитатах).
func sameHighlight(a, b clip) bool {
	if a.from > 0 && b.from > 0 {
		return a.from <= b.to && b.from <= a.to
	}
	return a.text == b.text || (len([]rune(a.text)) >= 20 && strings.Contains(b.text, a.text))
}

// isClippingLimit — заглушка Kindle вместо текста у книг с DRM, когда исчерпан
// лимит выделений: «<You have reached the clipping limit for this item>».
func isClippingLimit(s string) bool {
	return strings.HasPrefix(s, "<") && strings.HasSuffix(s, ">") && !strings.Contains(s[1:len(s)-1], "<")
}

// splitKindleTitle — «Название (Автор)»: автор — в последних скобках в конце
// строки (в названии бывают свои — «(Russian Edition)»).
func splitKindleTitle(line string) (title, author string) {
	if !strings.HasSuffix(line, ")") {
		return line, ""
	}
	depth := 0
	for i := len(line) - 1; i >= 0; i-- {
		switch line[i] {
		case ')':
			depth++
		case '(':
			depth--
			if depth == 0 {
				return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1 : len(line)-1])
			}
		}
	}
	return line, ""
}

var kindleLocRe = regexp.MustCompile(`(?i)(?:location|loc\.|место|месте|позиция|позиции|position)\s*(\d+)(?:\s*[-–—]\s*(\d+))?`)

// parseKindleMeta — вторая строка записи: вид, место, дата. Виды — английская и
// русская прошивка Kindle (и KOReader с его «Your highlight»); другие языки —
// не распознаём (ok=false, строка уходит в пропущенные).
func parseKindleMeta(line string) (clip, bool) {
	l := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "-–—"))
	low := strings.ToLower(l)
	var c clip
	switch {
	case strings.Contains(low, "highlight") || strings.Contains(low, "выделенный отрывок") || strings.Contains(low, "выделение"):
		c.kind = clipHighlight
	case strings.Contains(low, "note") || strings.Contains(low, "заметка"):
		c.kind = clipNote
	case strings.Contains(low, "bookmark") || strings.Contains(low, "закладка"):
		c.kind = clipBookmark
	default:
		return c, false
	}
	if m := kindleLocRe.FindStringSubmatch(l); m != nil {
		c.from, _ = strconv.Atoi(m[1])
		c.to = c.from
		if m[2] != "" {
			c.to, _ = strconv.Atoi(m[2])
		}
	}
	if i := strings.LastIndex(l, "|"); i >= 0 {
		c.at = parseKindleDate(strings.TrimSpace(l[i+1:]))
	}
	return c, true
}

var (
	kindleEnLayouts = []string{
		"Monday, January 2, 2006 3:04:05 PM",
		"Monday, 2 January 2006 15:04:05",
		"Monday, January 2, 2006, 3:04:05 PM",
		"Monday, 2 January 2006 3:04:05 PM",
	}
	kindleRuDateRe = regexp.MustCompile(`(\d{1,2})\s+([а-яё]+)\s+(\d{4})\s*(?:г\.?)?\s*(?:в\s+)?(\d{1,2}):(\d{2})(?::(\d{2}))?`)
	ruMonthsGen    = map[string]time.Month{
		"января": 1, "февраля": 2, "марта": 3, "апреля": 4, "мая": 5, "июня": 6,
		"июля": 7, "августа": 8, "сентября": 9, "октября": 10, "ноября": 11, "декабря": 12,
	}
)

// parseKindleDate — «Added on Thursday, August 22, 2024 6:10:53 PM» (США),
// «… 22 August 2024 18:10:53» (Британия), «Добавлено: четверг, 22 августа
// 2024 г. в 18:10:53». Время Kindle — местное без зоны; храним как UTC.
func parseKindleDate(s string) *time.Time {
	low := strings.ToLower(s)
	for _, p := range []string{"added on ", "добавлено:"} {
		if i := strings.Index(low, p); i >= 0 {
			s = strings.TrimSpace(s[i+len(p):])
			low = strings.ToLower(s)
			break
		}
	}
	for _, layout := range kindleEnLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	if m := kindleRuDateRe.FindStringSubmatch(low); m != nil {
		mon, ok := ruMonthsGen[m[2]]
		if !ok {
			return nil
		}
		d, _ := strconv.Atoi(m[1])
		y, _ := strconv.Atoi(m[3])
		hh, _ := strconv.Atoi(m[4])
		mm, _ := strconv.Atoi(m[5])
		ss, _ := strconv.Atoi(m[6])
		t := time.Date(y, mon, d, hh, mm, ss, 0, time.UTC)
		return &t
	}
	return nil
}

// clipForMessage — начало строки для сообщения о пропуске.
func clipForMessage(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 60 {
		return string(r[:60]) + "…"
	}
	return s
}
