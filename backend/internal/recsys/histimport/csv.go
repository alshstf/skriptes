package histimport

import (
	"encoding/csv"
	"errors"
	"io"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// CSV-форматы: calibre (каталог), Goodreads (Export Library) и наш шаблон.
// Колонки ищем по имени без регистра, а не по позиции: у Goodreads их было 31,
// стало 24, бывают в нижнем регистре; calibre сортирует их по алфавиту.

// Колонки универсального шаблона и их русские синонимы.
var csvAliases = map[string]string{
	"title": "title", "название": "title", "книга": "title",
	"author": "author", "authors": "author", "автор": "author", "авторы": "author",
	"rating": "rating", "оценка": "rating",
	"read_at": "read_at", "date_read": "read_at", "прочитано": "read_at", "дата прочтения": "read_at",
	"shelf": "shelf", "полка": "shelf", "статус": "shelf",
	"highlight": "highlight", "quote": "highlight", "цитата": "highlight", "подчёркивание": "highlight", "подчеркивание": "highlight",
	"note": "note", "заметка": "note",
	"isbn": "isbn",
}

// CSVTemplate — шапка и пример универсального шаблона (скачивается из UI).
const CSVTemplate = "title,author,rating,read_at,shelf,highlight,note,isbn\n" +
	"Пикник на обочине,Аркадий Стругацкий; Борис Стругацкий,5,2024-03-15,read,,,\n" +
	"Пикник на обочине,Аркадий Стругацкий; Борис Стругацкий,,,,\"Счастье для всех, даром, и пусть никто не уйдёт обиженный!\",,\n" +
	"Мастер и Маргарита,Михаил Булгаков,,,to_read,,,\n"

// detectCSV — формат по шапке (первой строке); "" — не CSV известного вида.
func detectCSV(header string) Source {
	cols, err := csvFields(header)
	if err != nil || len(cols) < 2 {
		return ""
	}
	has := map[string]bool{}
	for _, c := range cols {
		has[strings.ToLower(strings.TrimSpace(c))] = true
	}
	switch {
	case has["title"] && (has["my rating"] || has["exclusive shelf"]):
		return SourceGoodreads
	case has["title"] && has["authors"] && (has["author_sort"] || has["title_sort"] || has["uuid"] || has["#read"]):
		return SourceCalibre
	}
	for c := range has {
		if csvAliases[c] == "title" {
			return SourceCSV
		}
	}
	return ""
}

// csvDelimiter — разделитель по шапке: запятая, точка с запятой (Excel в
// русской локали) или табуляция.
func csvDelimiter(header string) rune {
	best, n := ',', strings.Count(header, ",")
	for _, d := range []rune{';', '\t'} {
		if c := strings.Count(header, string(d)); c > n {
			best, n = d, c
		}
	}
	return best
}

func csvFields(line string) ([]string, error) {
	r := csv.NewReader(strings.NewReader(line))
	r.Comma = csvDelimiter(line)
	r.LazyQuotes = true
	return r.Read()
}

func parseCSV(data string, src Source) (*Result, error) {
	header, _, _ := strings.Cut(data, "\n")
	r := csv.NewReader(strings.NewReader(data))
	r.Comma = csvDelimiter(header)
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	r.ReuseRecord = false
	head, err := r.Read()
	if err != nil {
		return nil, ErrUnknownFormat
	}
	col := map[string]int{}
	for i, h := range head {
		name := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(h, "\ufeff")))
		if _, dup := col[name]; !dup {
			col[name] = i
		}
	}
	res := &Result{Source: src}
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			line := 0
			var pe *csv.ParseError
			if errors.As(err, &pe) {
				line = pe.Line
			}
			res.skip(line, "не разобрать строку CSV")
			continue
		}
		line, _ := r.FieldPos(0)
		get := func(name string) string {
			if i, ok := col[name]; ok && i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		if allEmpty(rec) {
			continue
		}
		var it Item
		var reason string
		switch src {
		case SourceGoodreads:
			it, reason = goodreadsRow(get)
		case SourceCalibre:
			it, reason = calibreRow(get)
		default:
			it, reason = templateRow(get, col)
		}
		if reason != "" {
			res.skip(line, reason)
			continue
		}
		it.Line = line
		res.Items = append(res.Items, it)
	}
	return res, nil
}

func allEmpty(rec []string) bool {
	for _, v := range rec {
		if strings.TrimSpace(v) != "" {
			return false
		}
	}
	return true
}

// goodreadsRow — строка Goodreads «Export Library» (и LiveLib Backup в режиме
// «для Goodreads»: Shelves вместо Exclusive Shelf, «unread» — не дочитал).
func goodreadsRow(get func(string) string) (Item, string) {
	it := Item{Title: get("title")}
	if it.Title == "" {
		return it, "нет названия"
	}
	if a := get("author"); a != "" {
		it.Authors = append(it.Authors, a)
	}
	for _, a := range strings.Split(get("additional authors"), ",") {
		if a = strings.TrimSpace(a); a != "" {
			it.Authors = append(it.Authors, a)
		}
	}
	r, ok := stars(get("my rating"))
	if !ok {
		return it, "оценка не 0–5: " + get("my rating")
	}
	it.Rating = r
	it.ReadAt = parseDate(get("date read"))
	shelf := get("exclusive shelf")
	if shelf == "" {
		shelf = get("shelves")
	}
	switch strings.ToLower(shelf) {
	case "read":
		it.Shelf = ShelfRead
	case "to-read":
		it.Shelf = ShelfToRead
	case "currently-reading":
		it.Shelf = ShelfReading
	}
	for _, c := range []string{"isbn13", "isbn"} {
		if v := normISBN(get(c)); v != "" {
			it.ISBNs = append(it.ISBNs, v)
		}
	}
	return it, ""
}

// calibreRow — строка CSV-каталога calibre: авторы через « & », оценка — в
// звёздах (внутренняя 0–10, делённая на 2), «прочитано» — только
// пользовательская колонка #read (имени других не угадываем).
func calibreRow(get func(string) string) (Item, string) {
	it := Item{Title: get("title"), Authors: splitAuthors(get("authors"))}
	if it.Title == "" {
		return it, "нет названия"
	}
	r, ok := stars(get("rating"))
	if !ok {
		return it, "оценка не 0–5: " + get("rating")
	}
	it.Rating = r
	if yes(get("#read")) {
		it.Shelf = ShelfRead
	}
	if v := normISBN(get("isbn")); v != "" {
		it.ISBNs = append(it.ISBNs, v)
	}
	for _, id := range strings.Split(get("identifiers"), ",") {
		if k, v, ok := strings.Cut(strings.TrimSpace(id), ":"); ok && strings.EqualFold(k, "isbn") {
			if v = normISBN(v); v != "" && !slices.Contains(it.ISBNs, v) {
				it.ISBNs = append(it.ISBNs, v)
			}
		}
	}
	return it, ""
}

// templateRow — строка универсального шаблона (CSVTemplate).
func templateRow(get func(string) string, col map[string]int) (Item, string) {
	alias := func(field string) string {
		for name := range col {
			if csvAliases[name] == field {
				if v := get(name); v != "" {
					return v
				}
			}
		}
		return ""
	}
	it := Item{Title: alias("title"), Authors: splitAuthors(alias("author"))}
	if it.Title == "" {
		return it, "нет названия"
	}
	r, ok := stars(alias("rating"))
	if !ok {
		return it, "оценка не 1–5: " + alias("rating")
	}
	it.Rating = r
	if v := alias("read_at"); v != "" {
		if yes(v) {
			it.Shelf = ShelfRead
		} else if d := parseDate(v); d != nil {
			it.ReadAt = d
			it.Shelf = ShelfRead
		} else {
			return it, "дата прочтения не разобрана: " + v
		}
	}
	switch s := strings.ToLower(alias("shelf")); s {
	case "":
	case "read", "прочитано", "прочитана", "прочитал", "прочитала":
		it.Shelf = ShelfRead
	case "to_read", "to-read", "want", "хочу прочитать", "хочу":
		if it.Shelf == ShelfNone {
			it.Shelf = ShelfToRead
		}
	case "reading", "currently-reading", "читаю":
		if it.Shelf == ShelfNone {
			it.Shelf = ShelfReading
		}
	default:
		return it, "полка не из шаблона: " + s
	}
	if h, n := alias("highlight"), alias("note"); h != "" || n != "" {
		it.Highlights = append(it.Highlights, Highlight{Text: h, Note: n})
	}
	if v := normISBN(alias("isbn")); v != "" {
		it.ISBNs = append(it.ISBNs, v)
	}
	return it, ""
}

// stars — оценка в звёздах: пусто и 0 — без оценки; половинки — вверх (4.5 →
// 5, как округляют импортёры Goodreads); вне 0–5 — ok=false (шкалу 1–10 не
// угадываем).
func stars(s string) (int, bool) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ",", "."))
	if s == "" {
		return 0, true
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f < 0 || f > 5 {
		return 0, false
	}
	return int(math.Floor(f + 0.5)), true
}

func yes(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "y", "1", "+", "да", "x", "✓":
		return true
	}
	return false
}

var yearOnlyRe = regexp.MustCompile(`^\d{4}$`)

// parseDate — даты из файлов: ISO, «2024/05/01» (Goodreads), «01.05.2024»,
// месяц и год, один год (→ 1 января: «прочитано в 2019»).
func parseDate(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if len(s) >= 10 {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			t = t.UTC()
			return &t
		}
	}
	for _, layout := range []string{"2006-01-02", "2006/01/02", "02.01.2006", "2006-01", "2006/01", "01.2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return &t
		}
	}
	if yearOnlyRe.MatchString(s) {
		y, _ := strconv.Atoi(s)
		t := time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC)
		return &t
	}
	return nil
}
