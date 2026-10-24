// Package histimport — импорт чужой истории чтения из файлов, которые
// пользователь выгружает сам (план recsys/04): Kindle «My Clippings.txt»,
// CSV-каталог Calibre, Goodreads «Export Library» и наш универсальный
// CSV-шаблон. Parse разбирает файл (формат — по содержимому), Matcher
// сопоставляет книги из файла с работами каталога (точность важнее полноты —
// грабля 13). Скрейпинга и входа под чужой учёткой нет — только файл. Форматы
// и что из них берём — ~/projects/plans/skriptes/recsys/import-formats.md.
package histimport

import (
	"bytes"
	"errors"
	"io"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/skriptes/skriptes/backend/internal/textnorm"
)

// Source — откуда файл.
type Source string

const (
	SourceKindle    Source = "kindle"    // My Clippings.txt
	SourceCalibre   Source = "calibre"   // CSV-каталог calibre
	SourceGoodreads Source = "goodreads" // Export Library (и LiveLib Backup в режиме Goodreads)
	SourceCSV       Source = "csv"       // универсальный шаблон
)

// Shelf — статус книги в файле.
type Shelf string

const (
	ShelfNone    Shelf = ""
	ShelfRead    Shelf = "read"
	ShelfToRead  Shelf = "to_read"
	ShelfReading Shelf = "reading" // читаю сейчас: позиции нет — применять нечего
)

// Highlight — подчёркивание (цитата) и/или заметка к нему.
type Highlight struct {
	Text string     `json:"text"`
	Note string     `json:"note,omitempty"`
	At   *time.Time `json:"at,omitempty"`
}

// Item — книга из файла: всё, что о ней сказано (строки одной книги слиты).
type Item struct {
	Title      string      `json:"title"`
	Authors    []string    `json:"authors"`
	Rating     int         `json:"rating,omitempty"` // 1–5; 0 — без оценки
	ReadAt     *time.Time  `json:"read_at,omitempty"`
	Shelf      Shelf       `json:"shelf,omitempty"`
	Highlights []Highlight `json:"highlights,omitempty"`
	ISBNs      []string    `json:"isbns,omitempty"`
	// Line — строка CSV или номер записи Kindle, где книга встретилась впервые.
	Line int `json:"line"`
}

// Read — книга прочитана по файлу: полка «прочитано» или дата прочтения.
func (it Item) Read() bool { return it.Shelf == ShelfRead || it.ReadAt != nil }

// Skip — пропущенная строка файла и почему.
type Skip struct {
	Line   int    `json:"line"`
	Reason string `json:"reason"`
}

// Result — разобранный файл.
type Result struct {
	Source  Source `json:"source"`
	Items   []Item `json:"items"`
	Skipped []Skip `json:"skipped"` // первые maxSkips
	// SkippedTotal — сколько строк пропущено всего (Skipped — только начало).
	SkippedTotal int `json:"skipped_total"`
}

const maxSkips = 50

func (r *Result) skip(line int, reason string) {
	r.SkippedTotal++
	if len(r.Skipped) < maxSkips {
		r.Skipped = append(r.Skipped, Skip{Line: line, Reason: reason})
	}
}

var (
	// ErrUnknownFormat — файл не похож ни на один поддерживаемый формат.
	ErrUnknownFormat = errors.New("формат файла не распознан: нужен My Clippings.txt с Kindle, CSV-каталог calibre, экспорт Goodreads или CSV по шаблону")
	// ErrNoBooks — формат распознан, но книг в файле нет.
	ErrNoBooks = errors.New("в файле не нашлось ни одной книги")
)

// Parse разбирает файл. hint — формат, если пользователь его выбрал (пусто —
// определить по содержимому). Размер файла ограничивает вызывающий.
func Parse(r io.Reader, hint Source) (*Result, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	raw = bytes.TrimPrefix(raw, []byte("\ufeff"))
	data := strings.ToValidUTF8(string(raw), "�")
	src := hint
	if src == "" {
		src = detect(data)
	}
	var res *Result
	switch src {
	case SourceKindle:
		res = parseKindle(data)
	case SourceCalibre, SourceGoodreads, SourceCSV:
		res, err = parseCSV(data, src)
		if err != nil {
			return nil, err
		}
	default:
		return nil, ErrUnknownFormat
	}
	res.Items = merge(res.Items)
	if len(res.Items) == 0 {
		return res, ErrNoBooks
	}
	return res, nil
}

// detect — формат по содержимому: CSV — по шапке, Kindle — по разделителю
// записей «==========».
func detect(data string) Source {
	first, _, _ := strings.Cut(data, "\n")
	if src := detectCSV(strings.TrimSpace(first)); src != "" {
		return src
	}
	for _, l := range strings.SplitN(data, "\n", 200) {
		if strings.TrimSpace(strings.TrimPrefix(l, "\ufeff")) == kindleSeparator {
			return SourceKindle
		}
	}
	return ""
}

// bookKey — одна ли это книга внутри файла: название и первый автор без
// регистра, ё, пунктуации.
func bookKey(it Item) string {
	a := ""
	if len(it.Authors) > 0 {
		a = foldKey(it.Authors[0])
	}
	return foldKey(it.Title) + "\x00" + a
}

func foldKey(s string) string {
	s = strings.ToLower(textnorm.FoldYo(s))
	return strings.Join(strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }), " ")
}

// merge сливает записи одной книги (Kindle — блок на каждое подчёркивание,
// CSV-шаблон — строка на каждую цитату): оценка — первая непустая, «прочитано»
// важнее «читаю» и «хочу прочитать», дата — поздняя, цитаты и ISBN — все.
func merge(items []Item) []Item {
	idx := make(map[string]int, len(items))
	out := make([]Item, 0, len(items))
	for _, it := range items {
		k := bookKey(it)
		i, ok := idx[k]
		if !ok {
			idx[k] = len(out)
			out = append(out, it)
			continue
		}
		m := &out[i]
		if m.Rating == 0 {
			m.Rating = it.Rating
		}
		if shelfRank[it.Shelf] > shelfRank[m.Shelf] {
			m.Shelf = it.Shelf
		}
		if it.ReadAt != nil && (m.ReadAt == nil || it.ReadAt.After(*m.ReadAt)) {
			m.ReadAt = it.ReadAt
		}
		m.Highlights = append(m.Highlights, it.Highlights...)
		for _, isbn := range it.ISBNs {
			if !slices.Contains(m.ISBNs, isbn) {
				m.ISBNs = append(m.ISBNs, isbn)
			}
		}
	}
	return out
}

var shelfRank = map[Shelf]int{ShelfNone: 0, ShelfToRead: 1, ShelfReading: 2, ShelfRead: 3}

// splitAuthors — несколько авторов в одной строке: «;» и « & » (calibre,
// Kindle). Запятая — нет: «Стругацкий, Аркадий» — один человек.
func splitAuthors(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ";") {
		for _, a := range strings.Split(part, " & ") {
			a = strings.TrimSpace(strings.ReplaceAll(a, "&&", "&"))
			if a != "" && !slices.Contains(out, a) {
				out = append(out, a)
			}
		}
	}
	return out
}

// normISBN — только цифры и X; 10 или 13 знаков (как books.isbn), иначе "".
func normISBN(s string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(s) {
		if (r >= '0' && r <= '9') || r == 'X' {
			b.WriteRune(r)
		}
	}
	v := b.String()
	if len(v) != 10 && len(v) != 13 {
		return ""
	}
	return v
}
