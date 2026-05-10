// Package inpx разбирает INP/INPX-файлы (формат MyHomeLib/Flibusta/Lib.rus.ec).
//
// Формат:
//   - INPX = zip-архив, содержащий version.info, collection.info,
//     опционально structure.info и набор *.inp.
//   - .inp — текст в UTF-8: записи разделены \r\n, поля — байтом 0x04.
//   - В пределах поля AUTHOR/GENRE множественные значения разделены ':',
//     внутри AUTHOR части ФИО — ','.
//   - Имя .inp без расширения соответствует имени .zip с книгами:
//     "fb2-749080-749080.inp" ↔ "fb2-749080-749080.zip".
//   - В реально встречающихся коллекциях (librusec_local_fb2 и т.п.)
//     имена .inp иногда несут суффикс "_lost" (напр. "fb2-...-..._lost.inp").
//     В каноничных генераторах (InpxCreator, inpx-web) этот суффикс
//     не задокументирован и не производится; физический архив всё равно
//     называется без него. Поэтому при выводе Archive из Name суффикс
//     просто отбрасывается. Существует ли физический архив — отдельный
//     вопрос, который решает импортёр (попыткой открыть zip).
package inpx

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Имена полей, как они приходят в structure.info.
const (
	FieldAuthor   = "AUTHOR"
	FieldGenre    = "GENRE"
	FieldTitle    = "TITLE"
	FieldSeries   = "SERIES"
	FieldSerNo    = "SERNO"
	FieldFile     = "FILE"
	FieldSize     = "SIZE"
	FieldLibID    = "LIBID"
	FieldDel      = "DEL"
	FieldExt      = "EXT"
	FieldDate     = "DATE"
	FieldLang     = "LANG"
	FieldLibRate  = "LIBRATE"
	FieldKeywords = "KEYWORDS"
)

// Schema задаёт порядок полей в .inp-файле.
// Получается из structure.info (если он есть в INPX) или DefaultSchema.
type Schema []string

// DefaultSchema — расширенная схема librusec/Flibusta (14 полей).
// Используется когда structure.info отсутствует.
var DefaultSchema = Schema{
	FieldAuthor, FieldGenre, FieldTitle, FieldSeries, FieldSerNo,
	FieldFile, FieldSize, FieldLibID, FieldDel, FieldExt, FieldDate,
	FieldLang, FieldLibRate, FieldKeywords,
}

// Author — автор книги; части ФИО приходят в AUTHOR через ','.
type Author struct {
	LastName, FirstName, MiddleName string
}

// Record — одна нормализованная запись из .inp.
// Числовые поля = 0 / nil-указатель если поле было пустым.
type Record struct {
	Authors  []Author
	Genres   []string // FB2-коды (sf_action, popadanec, ...)
	Title    string
	Series   string
	SerNo    int
	File     string // имя файла без расширения внутри zip
	Size     int64  // байты
	LibID    string
	Deleted  bool
	Ext      string
	Date     *time.Time // YYYY-MM-DD; nil если пусто или не парсится
	Lang     string
	Rating   int
	Keywords string

	// Extra собирает значения из полей, не входящих в DefaultSchema.
	// Заполняется только если в schema есть незнакомые имена.
	Extra map[string]string
}

const (
	fieldSep  byte = 0x04
	multiSep  byte = ':'
	personSep byte = ','
	recordSep      = "\r\n"
)

// ParseSchema читает structure.info: одно имя поля на строку (TRIM, к UPPER).
// Пустые строки игнорируются. Возвращает DefaultSchema если поток пуст.
func ParseSchema(r io.Reader) (Schema, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1024), 64*1024)
	var out Schema
	for sc.Scan() {
		name := strings.TrimSpace(sc.Text())
		if name == "" {
			continue
		}
		out = append(out, strings.ToUpper(name))
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read structure.info: %w", err)
	}
	if len(out) == 0 {
		return DefaultSchema, nil
	}
	return out, nil
}

// ParseRecord разбирает одну строку записи (без разделителя \r\n).
// Если в записи больше полей чем в schema — лишние сохраняются под именем "_extraN".
// Если меньше — недостающие считаются пустыми.
func ParseRecord(line []byte, schema Schema) (Record, error) {
	if len(line) == 0 {
		return Record{}, errors.New("empty record")
	}
	fields := bytes.Split(line, []byte{fieldSep})
	rec := Record{}
	for i, f := range fields {
		var name string
		switch {
		case i < len(schema):
			name = schema[i]
		default:
			name = fmt.Sprintf("_extra%d", i-len(schema))
		}
		if err := assignField(&rec, name, string(f)); err != nil {
			return Record{}, fmt.Errorf("field %s: %w", name, err)
		}
	}
	return rec, nil
}

// ParseInp читает поток .inp и для каждой непустой записи вызывает fn.
// Стримово: запись держится в памяти только пока работает fn.
// Если fn возвращает ошибку — итерация прерывается и ошибка пробрасывается наверх.
func ParseInp(r io.Reader, schema Schema, fn func(Record) error) error {
	br := bufio.NewReader(r)
	var line []byte
	for {
		chunk, err := br.ReadBytes('\n')
		if len(chunk) > 0 {
			line = append(line[:0], chunk...)
			line = bytes.TrimRight(line, "\r\n")
			if len(line) > 0 {
				rec, perr := ParseRecord(line, schema)
				if perr != nil {
					return perr
				}
				if ferr := fn(rec); ferr != nil {
					return ferr
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read inp: %w", err)
		}
	}
}

// ── вспомогательные ────────────────────────────────────────────

func assignField(rec *Record, name, raw string) error {
	switch name {
	case FieldAuthor:
		rec.Authors = parseAuthors(raw)
	case FieldGenre:
		rec.Genres = splitMulti(raw)
	case FieldTitle:
		rec.Title = raw
	case FieldSeries:
		rec.Series = raw
	case FieldSerNo:
		n, err := parseIntOrZero(raw)
		if err != nil {
			return err
		}
		rec.SerNo = n
	case FieldFile:
		rec.File = raw
	case FieldSize:
		n, err := parseInt64OrZero(raw)
		if err != nil {
			return err
		}
		rec.Size = n
	case FieldLibID:
		rec.LibID = raw
	case FieldDel:
		rec.Deleted = strings.TrimSpace(raw) == "1"
	case FieldExt:
		rec.Ext = raw
	case FieldDate:
		t := parseDateOrNil(raw)
		rec.Date = t
	case FieldLang:
		rec.Lang = raw
	case FieldLibRate:
		n, err := parseIntOrZero(raw)
		if err != nil {
			return err
		}
		rec.Rating = n
	case FieldKeywords:
		rec.Keywords = raw
	default:
		if rec.Extra == nil {
			rec.Extra = map[string]string{}
		}
		rec.Extra[name] = raw
	}
	return nil
}

// parseAuthors режет AUTHOR-поле по ':' (трейлинговый ':' игнорируется).
// Каждый автор — Lastname,Firstname,Middlename, недостающие части — пустые строки.
func parseAuthors(s string) []Author {
	parts := splitMulti(s)
	if len(parts) == 0 {
		return nil
	}
	out := make([]Author, 0, len(parts))
	for _, p := range parts {
		a := Author{}
		segs := strings.SplitN(p, string(personSep), 3)
		if len(segs) > 0 {
			a.LastName = strings.TrimSpace(segs[0])
		}
		if len(segs) > 1 {
			a.FirstName = strings.TrimSpace(segs[1])
		}
		if len(segs) > 2 {
			a.MiddleName = strings.TrimSpace(segs[2])
		}
		// Полностью пустых авторов отбрасываем.
		if a.LastName == "" && a.FirstName == "" && a.MiddleName == "" {
			continue
		}
		out = append(out, a)
	}
	return out
}

// splitMulti режет multi-value поле по ':' и отбрасывает пустые элементы
// (типичный трейлинговый ':' в конце поля).
func splitMulti(s string) []string {
	if s == "" {
		return nil
	}
	raw := strings.Split(s, string(multiSep))
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		r = strings.TrimSpace(r)
		if r != "" {
			out = append(out, r)
		}
	}
	return out
}

func parseIntOrZero(s string) (int, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("parse int %q: %w", s, err)
	}
	return n, nil
}

func parseInt64OrZero(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse int64 %q: %w", s, err)
	}
	return n, nil
}

func parseDateOrNil(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return nil
	}
	return &t
}
