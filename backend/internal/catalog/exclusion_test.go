package catalog

import (
	"strings"
	"testing"
)

func TestBookExclusionClause(t *testing.T) {
	// Пусто → no-op (ни фрагмента, ни аргументов): хелпер безопасно звать всегда.
	if clause, args := bookExclusionClause(2, nil, nil); clause != "" || len(args) != 0 {
		t.Fatalf("empty: clause=%q args=%v, want empty", clause, args)
	}

	// Только языки: фрагмент по b.lang с плейсхолдером startArg, один арг.
	clause, args := bookExclusionClause(2, nil, []string{"bg", "uk"})
	if !strings.Contains(clause, "b.lang") || !strings.Contains(clause, "$2::text[]") {
		t.Fatalf("langs: clause=%q", clause)
	}
	if len(args) != 1 {
		t.Fatalf("langs: want 1 arg, got %d", len(args))
	}

	// Только жанры: NOT EXISTS по book_genres с плейсхолдером startArg.
	clause, args = bookExclusionClause(3, []string{"sf"}, nil)
	if !strings.Contains(clause, "NOT EXISTS") || !strings.Contains(clause, "$3::text[]") {
		t.Fatalf("genres: clause=%q", clause)
	}
	if len(args) != 1 {
		t.Fatalf("genres: want 1 arg, got %d", len(args))
	}

	// Оба: язык занимает startArg, жанр — startArg+1; два аргумента в порядке lang, genre.
	clause, args = bookExclusionClause(2, []string{"sf"}, []string{"bg"})
	if !strings.Contains(clause, "$2::text[]") || !strings.Contains(clause, "$3::text[]") {
		t.Fatalf("both: clause=%q", clause)
	}
	if len(args) != 2 {
		t.Fatalf("both: want 2 args, got %d", len(args))
	}
	if langs, ok := args[0].([]string); !ok || len(langs) != 1 || langs[0] != "bg" {
		t.Fatalf("both: arg0 should be langs slice, got %#v", args[0])
	}
	if genres, ok := args[1].([]string); !ok || len(genres) != 1 || genres[0] != "sf" {
		t.Fatalf("both: arg1 should be genres slice, got %#v", args[1])
	}
}
