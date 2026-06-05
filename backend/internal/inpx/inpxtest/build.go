// Package inpxtest строит синтетические INPX-фикстуры для интеграционных тестов.
//
// Формат — как у librusec/Flibusta (см. inpx.DefaultSchema): zip с version.info,
// collection.info и одним .inp; запись = 14 полей через 0x04, авторы/жанры — через
// ':', части ФИО — через ','. Описываешь книги читаемыми структурами Book, а не
// бинарём — легко добавлять кейсы (языки в разном регистре, дубль серий на разных
// языках, скрытые жанры, удалённые книги и т.п.).
package inpxtest

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	fieldSep = "\x04" // разделитель полей в .inp
	multiSep = ":"    // разделитель авторов/жанров внутри поля
)

// Book — читаемое описание одной книги. Пустые поля допустимы (Series/Lang/…).
// Authors — каждый в форме "Last,First,Middle" (First/Middle опциональны).
type Book struct {
	Authors  []string
	Genres   []string // fb2-коды (sf, det_police, erotica, …)
	Title    string
	Series   string
	SerNo    int
	LibID    string // уникальный; идёт и в FILE, и в LIBID
	Deleted  bool
	Lang     string // как в источнике — можно "RU"/" ru "/"" для проверки нормализации
	Rating   int
	Keywords string
}

// record собирает одну .inp-строку (14 полей DefaultSchema через 0x04).
func record(b Book) string {
	del := "0"
	if b.Deleted {
		del = "1"
	}
	serNo := ""
	if b.SerNo > 0 {
		serNo = strconv.Itoa(b.SerNo)
	}
	rating := ""
	if b.Rating > 0 {
		rating = strconv.Itoa(b.Rating)
	}
	fields := []string{
		strings.Join(b.Authors, multiSep), // AUTHOR
		strings.Join(b.Genres, multiSep),  // GENRE
		b.Title,                           // TITLE
		b.Series,                          // SERIES
		serNo,                             // SERNO
		b.LibID,                           // FILE (имя fb2 внутри zip)
		"1024",                            // SIZE
		b.LibID,                           // LIBID
		del,                               // DEL
		"fb2",                             // EXT
		"2020-01-01",                      // DATE (date_added; не год написания)
		b.Lang,                            // LANG
		rating,                            // LIBRATE
		b.Keywords,                        // KEYWORDS
	}
	return strings.Join(fields, fieldSep)
}

// WriteINPX строит .inpx в каталоге dir под именем name и возвращает полный путь.
// Все книги кладутся в один синтетический архив (fb2-900001-900099). Имя файла
// архива не важно для этих тестов — обложки/чтение тут не задействованы.
func WriteINPX(dir, name string, books []Book) (string, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(fname, content string) error {
		w, err := zw.Create(fname)
		if err != nil {
			return fmt.Errorf("zip create %s: %w", fname, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			return fmt.Errorf("zip write %s: %w", fname, err)
		}
		return nil
	}
	if err := add("version.info", "20260601\n"); err != nil {
		return "", err
	}
	// collection.info — 5 строк (Name, Prefix, Version, Description, URL).
	if err := add("collection.info",
		"Synthetic Test [FB2]\nsynthetic_test_fb2\n65536\nДиверс-фикстура для тестов\nhttp://example.test/\n"); err != nil {
		return "", err
	}
	lines := make([]string, 0, len(books))
	for _, b := range books {
		lines = append(lines, record(b))
	}
	if err := add("fb2-900001-900099.inp", strings.Join(lines, "\r\n")+"\r\n"); err != nil {
		return "", err
	}
	if err := zw.Close(); err != nil {
		return "", fmt.Errorf("zip close: %w", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		return "", fmt.Errorf("write inpx: %w", err)
	}
	return path, nil
}
