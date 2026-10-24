package histimport

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Фикстуры вымышленные, по устройству реальных выгрузок (recsys/04, 4.1):
// BOM, CRLF, русская и английская прошивка Kindle, KOReader, calibre и
// Goodreads с их кавычками и форматами.

const bom = "\ufeff"

func kindleFile() string {
	blocks := []string{
		// Русская прошивка: EN DASH, «Место», дата с «г. в».
		"Пикник на обочине (Стругацкий, Аркадий)\n– Ваш выделенный отрывок на странице 12 | Место 180–181 | Добавлено: четверг, 22 августа 2024 г. в 18:10:53\n\nСчастье для всех, даром",
		// Правка того же выделения: место пересекается — остаётся последняя версия.
		"Пикник на обочине (Стругацкий, Аркадий)\n– Ваш выделенный отрывок на странице 12 | Место 180–183 | Добавлено: четверг, 22 августа 2024 г. в 18:11:02\n\nСчастье для всех, даром, и пусть никто не уйдёт обиженный!",
		// Заметка на конце выделения — садится к нему.
		"Пикник на обочине (Стругацкий, Аркадий)\n– Ваша заметка на странице 12 | Место 183 | Добавлено: четверг, 22 августа 2024 г. в 18:12:00\n\nглавная фраза книги",
		// Закладка — пропускаем молча.
		"Пикник на обочине (Стругацкий, Аркадий)\n– Ваша закладка на странице 40 | Место 610 | Добавлено: пятница, 23 августа 2024 г. в 9:00:00\n\n",
		// Английская прошивка, скобки в названии, многострочный текст.
		"The Long Walk (Russian Edition) (Stephen King)\n- Your Highlight on page 5 | Location 70-72 | Added on Monday, January 1, 2024 10:00:00 AM\n\nFirst line\nsecond line",
		// Заметка без выделения на её месте — сама по себе.
		"The Long Walk (Russian Edition) (Stephen King)\n- Your Note on Location 300 | Added on Monday, 1 January 2024 22:15:00\n\nвернуться к этому",
		// Лимит выделений DRM-книги — не цитата.
		"The Long Walk (Russian Edition) (Stephen King)\n- Your Highlight on Location 400-401 | Added on Monday, January 1, 2024 11:00:00 PM\n\n<You have reached the clipping limit for this item>",
		// Немецкая прошивка — не распознаём, в пропущенные.
		"Der Process (Franz Kafka)\n- Ihre Markierung bei Position 10-11 | Hinzugefügt am Montag, 1. Januar 2024 10:00:00\n\nJemand musste Josef K. verleumdet haben",
		// KOReader: строчная, без Location.
		"Мы (Евгений Замятин)\n- Your highlight on page 3 | Added on Tuesday, March 05, 2024 07:30:00 PM\n\nЯ просто списываю — слово в слово",
		"Мы (Евгений Замятин)\n- Your highlight on page 3 | Added on Tuesday, March 05, 2024 07:31:00 PM\n\nИ ещё одна, другая цитата на той же странице",
	}
	return bom + strings.ReplaceAll(strings.Join(blocks, "\n==========\n")+"\n==========\n", "\n", "\r\n") +
		// Склеенный файл: BOM посреди.
		bom + "Только закладки (Автор Неизвестный)\r\n- Your Bookmark on Location 5 | Added on Monday, January 1, 2024 10:00:00 AM\r\n\r\n\r\n==========\r\n"
}

func TestParseKindle(t *testing.T) {
	res, err := Parse(strings.NewReader(kindleFile()), "")
	require.NoError(t, err)
	require.Equal(t, SourceKindle, res.Source)
	require.Len(t, res.Items, 3, "книга только с закладками не попадает")

	picnic := res.Items[0]
	require.Equal(t, "Пикник на обочине", picnic.Title)
	require.Equal(t, []string{"Стругацкий, Аркадий"}, picnic.Authors)
	require.Equal(t, ShelfNone, picnic.Shelf, "подчёркивания ≠ прочитано")
	require.Len(t, picnic.Highlights, 1)
	h := picnic.Highlights[0]
	require.Equal(t, "Счастье для всех, даром, и пусть никто не уйдёт обиженный!", h.Text)
	require.Equal(t, "главная фраза книги", h.Note)
	require.Equal(t, time.Date(2024, 8, 22, 18, 11, 2, 0, time.UTC), *h.At)

	walk := res.Items[1]
	require.Equal(t, "The Long Walk (Russian Edition)", walk.Title)
	require.Equal(t, []string{"Stephen King"}, walk.Authors)
	require.Equal(t, []Highlight{
		{Text: "First line\nsecond line", At: tp(time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC))},
		{Note: "вернуться к этому", At: tp(time.Date(2024, 1, 1, 22, 15, 0, 0, time.UTC))},
	}, walk.Highlights)

	we := res.Items[2]
	require.Equal(t, "Мы", we.Title)
	require.Len(t, we.Highlights, 2, "без мест: разные цитаты одной страницы — обе")
	require.Equal(t, time.Date(2024, 3, 5, 19, 30, 0, 0, time.UTC), *we.Highlights[0].At)

	require.Equal(t, 1, res.SkippedTotal)
	require.Contains(t, res.Skipped[0].Reason, "Ihre Markierung")
}

func TestParseCalibre(t *testing.T) {
	file := bom + "author_sort,authors,id,identifiers,isbn,rating,tags,title,uuid,#read\r\n" +
		`"Булгаков, Михаил","Михаил Булгаков","1","isbn:9785170000001,goodreads:1","","4.5","Классика","Мастер и Маргарита","u1","True"` + "\r\n" +
		`"Ильф, Илья & Петров, Евгений","Илья Ильф & Евгений Петров","2","","5170000002","","","Двенадцать стульев","u2","False"` + "\r\n" +
		`"Smith && Wesson","Smith && Wesson","3","","","2","","Revolvers ""in"" history","u3",""` + "\r\n" +
		`"X","Кто-то","4","","","7","","Шкала не та","u4",""` + "\r\n"
	res, err := Parse(strings.NewReader(file), "")
	require.NoError(t, err)
	require.Equal(t, SourceCalibre, res.Source)
	require.Len(t, res.Items, 3)

	m := res.Items[0]
	require.Equal(t, "Мастер и Маргарита", m.Title)
	require.Equal(t, 5, m.Rating, "половинка — вверх")
	require.Equal(t, ShelfRead, m.Shelf, "#read = True")
	require.Equal(t, []string{"9785170000001"}, m.ISBNs)

	chairs := res.Items[1]
	require.Equal(t, []string{"Илья Ильф", "Евгений Петров"}, chairs.Authors)
	require.Zero(t, chairs.Rating)
	require.Equal(t, ShelfNone, chairs.Shelf, "книга в calibre ≠ прочитана")
	require.Equal(t, []string{"5170000002"}, chairs.ISBNs)

	rev := res.Items[2]
	require.Equal(t, `Revolvers "in" history`, rev.Title)
	require.Equal(t, []string{"Smith & Wesson"}, rev.Authors)
	require.Equal(t, 2, rev.Rating)

	require.Equal(t, 1, res.SkippedTotal)
	require.Equal(t, 5, res.Skipped[0].Line)
	require.Contains(t, res.Skipped[0].Reason, "оценка")
}

func TestParseGoodreads(t *testing.T) {
	file := "Book Id,Title,Author,Author l-f,Additional Authors,ISBN,ISBN13,My Rating,Average Rating,Publisher,Binding,Number of Pages,Year Published,Original Publication Year,Date Read,Date Added,Bookshelves,Bookshelves with positions,Exclusive Shelf,My Review,Spoiler,Private Notes,Read Count,Owned Copies\n" +
		`1,"Roadside Picnic","Arkady Strugatsky","Strugatsky, Arkady","Boris Strugatsky","=""0575079789""","=""9780575079786""",4,4.05,Gollancz,Paperback,224,2007,1972,2024/03/15,2024/01/02,,,read,"Great<br/>book",,,1,0` + "\n" +
		`2,"The Name of the Wind (The Kingkiller Chronicle, #1)","Patrick Rothfuss","Rothfuss, Patrick","","=""""","=""""",0,4.52,DAW,Hardcover,662,2007,2007,,2024/02/01,to-read,to-read (#3),to-read,,,,0,0` + "\n" +
		`3,"Dune","Frank Herbert","Herbert, Frank","","=""""","=""""",0,4.27,Ace,Paperback,658,1990,1965,,2024/02/01,currently-reading,,currently-reading,,,,0,0` + "\n"
	res, err := Parse(strings.NewReader(file), "")
	require.NoError(t, err)
	require.Equal(t, SourceGoodreads, res.Source)
	require.Len(t, res.Items, 3)

	p := res.Items[0]
	require.Equal(t, []string{"Arkady Strugatsky", "Boris Strugatsky"}, p.Authors)
	require.Equal(t, 4, p.Rating)
	require.Equal(t, ShelfRead, p.Shelf)
	require.Equal(t, time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC), *p.ReadAt)
	require.Equal(t, []string{"9780575079786", "0575079789"}, p.ISBNs)
	require.True(t, p.Read())

	w := res.Items[1]
	require.Zero(t, w.Rating, "0 = без оценки")
	require.Equal(t, ShelfToRead, w.Shelf)
	require.Nil(t, w.ISBNs)
	require.False(t, w.Read())

	require.Equal(t, ShelfReading, res.Items[2].Shelf)
}

func TestParseTemplate(t *testing.T) {
	t.Run("шаблон из UI — разбирается и сливает строки цитат", func(t *testing.T) {
		res, err := Parse(strings.NewReader(CSVTemplate), "")
		require.NoError(t, err)
		require.Equal(t, SourceCSV, res.Source)
		require.Len(t, res.Items, 2)
		p := res.Items[0]
		require.Equal(t, []string{"Аркадий Стругацкий", "Борис Стругацкий"}, p.Authors)
		require.Equal(t, 5, p.Rating)
		require.Equal(t, ShelfRead, p.Shelf)
		require.Len(t, p.Highlights, 1)
		require.Equal(t, ShelfToRead, res.Items[1].Shelf)
	})
	t.Run("русская шапка, точка с запятой (Excel), даты и «да»", func(t *testing.T) {
		file := "Название;Автор;Оценка;Прочитано;Полка;Цитата\n" +
			"Мы;Евгений Замятин;4;05.03.2023;;\n" +
			"Чевенгур;Андрей Платонов;;да;;\n" +
			"Котлован;Андрей Платонов;;2019;;\n" +
			"Собачье сердце;Михаил Булгаков;;;хочу прочитать;\n" +
			";Без названия;5;;;\n" +
			"Улитка на склоне;Стругацкие;11;;;\n" +
			"Град обреченный;Стругацкие;;когда-то;;\n"
		res, err := Parse(strings.NewReader(file), "")
		require.NoError(t, err)
		require.Equal(t, SourceCSV, res.Source)
		require.Len(t, res.Items, 4)
		require.Equal(t, time.Date(2023, 3, 5, 0, 0, 0, 0, time.UTC), *res.Items[0].ReadAt)
		require.Equal(t, 4, res.Items[0].Rating)
		require.True(t, res.Items[1].Read())
		require.Nil(t, res.Items[1].ReadAt)
		require.Equal(t, time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC), *res.Items[2].ReadAt)
		require.Equal(t, ShelfToRead, res.Items[3].Shelf)
		require.Equal(t, 3, res.SkippedTotal)
		require.Equal(t, []int{6, 7, 8}, []int{res.Skipped[0].Line, res.Skipped[1].Line, res.Skipped[2].Line})
	})
}

func TestParseUnknown(t *testing.T) {
	_, err := Parse(strings.NewReader("просто текст\nбез формата\n"), "")
	require.ErrorIs(t, err, ErrUnknownFormat)
	_, err = Parse(strings.NewReader("title,author\n"), "")
	require.ErrorIs(t, err, ErrNoBooks)
}

func tp(t time.Time) *time.Time { return &t }
