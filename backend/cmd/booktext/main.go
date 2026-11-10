// booktext — отладка разбора текста книги из epub ридера (internal/booktext).
//
//	booktext stats book.epub…        — по строке на книгу: разделы, знаки, самый длинный раздел
//	booktext dump book.epub [раздел] — список разделов или текст одного
//
// Нужен для замеров длины книг под ИИ-функции читалки и для разбора жалоб
// «не нашлось место в тексте» (план — ~/projects/plans/skriptes/ai-roadmap.md).
package main

import (
	"fmt"
	"os"
	"strconv"
	"unicode/utf8"

	"github.com/skriptes/skriptes/backend/internal/booktext"
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: booktext stats book.epub… | booktext dump book.epub [section]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "stats":
		fmt.Println("version\tsections\ttext_sections\trunes\tmax_section_runes\tfile")
		for _, p := range os.Args[2:] {
			b, err := booktext.Open(p)
			if err != nil {
				fmt.Fprintf(os.Stderr, "%s: %v\n", p, err)
				continue
			}
			text, longest := 0, 0
			for _, s := range b.Sections {
				if !s.Linear || s.Notes || s.Text == "" {
					continue
				}
				text++
				longest = max(longest, utf8.RuneCountInString(s.Text))
			}
			fmt.Printf("%s\t%d\t%d\t%d\t%d\t%s\n", b.Version, len(b.Sections), text, b.Runes(), longest, p)
		}
	case "dump":
		b, err := booktext.Open(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if len(os.Args) > 3 {
			n, err := strconv.Atoi(os.Args[3])
			if err != nil || n < 0 || n >= len(b.Sections) {
				fmt.Fprintln(os.Stderr, "no such section")
				os.Exit(1)
			}
			fmt.Println(b.Sections[n].Text)
			return
		}
		for _, s := range b.Sections {
			fmt.Printf("%d\t%d\tlinear=%t notes=%t\t%s\t%s\n", s.Index, utf8.RuneCountInString(s.Text), s.Linear, s.Notes, s.Href, s.Title)
		}
	default:
		fmt.Fprintln(os.Stderr, "unknown command", os.Args[1])
		os.Exit(2)
	}
}
