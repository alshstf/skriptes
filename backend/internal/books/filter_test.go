package books

import "testing"

// Юниты на сборку meili filter-выражения. Без docker: проверяем строку,
// которую отдаём в Meili (в т.ч. новые exclude-клаузы для скрытого контента).

func TestBuildFilter_FiltersAndExclusions(t *testing.T) {
	got := buildFilter(ListParams{
		Genres:        []string{"sf_action", ""}, // пустые отбрасываются
		Lang:          "ru",
		YearFrom:      1990,
		YearTo:        2000,
		SeriesID:      7,
		AuthorID:      42,
		ExcludeGenres: []string{"erotica", "porno"},
		ExcludeLangs:  []string{"bg"},
	})
	want := `genres IN ["sf_action"] AND lang = "ru" AND year >= 1990 AND year <= 2000 ` +
		`AND series_id = 7 AND author_ids = 42 AND genres NOT IN ["erotica","porno"] AND lang NOT IN ["bg"]`
	if got != want {
		t.Fatalf("buildFilter mismatch:\n got: %s\nwant: %s", got, want)
	}
}

func TestBuildFilter_OnlyExclusions(t *testing.T) {
	got := buildFilter(ListParams{
		ExcludeGenres: []string{"erotica"},
		ExcludeLangs:  []string{"bg", "uk"},
	})
	want := `genres NOT IN ["erotica"] AND lang NOT IN ["bg","uk"]`
	if got != want {
		t.Fatalf("buildFilter mismatch:\n got: %s\nwant: %s", got, want)
	}
}

func TestBuildFilter_Empty(t *testing.T) {
	if got := buildFilter(ListParams{}); got != "" {
		t.Fatalf("empty params → empty filter, got %q", got)
	}
	// Срезы из одних пустых строк — тоже пустой фильтр.
	if got := buildFilter(ListParams{ExcludeGenres: []string{"", ""}}); got != "" {
		t.Fatalf("blank-only exclude → empty filter, got %q", got)
	}
}

// buildWorksFilter: язык оригинала фильтруется по orig_lang (эффективный
// оригинал = src_lang ?? язык издания) — ТОЛЬКО works-индекса; buildFilter
// (books/OPDS) его игнорирует (атрибут не filterable). URL-параметр значения
// остаётся SrcLang.
func TestBuildWorksFilter_SrcLang(t *testing.T) {
	got := buildWorksFilter(ListParams{Lang: "ru", SrcLang: "en"}, nil)
	want := `lang = "ru" AND orig_lang = "en"`
	if got != want {
		t.Fatalf("buildWorksFilter mismatch:\n got: %s\nwant: %s", got, want)
	}
	// books-фильтр src_lang НЕ знает (не filterable в books-индексе).
	if got := buildFilter(ListParams{SrcLang: "en"}); got != "" {
		t.Fatalf("buildFilter must ignore SrcLang, got %q", got)
	}
}

func TestExclusionFilter(t *testing.T) {
	if got := exclusionFilter(nil, nil); got != "" {
		t.Fatalf("nil exclusions → empty, got %q", got)
	}
	if got := exclusionFilter([]string{"a"}, nil); got != `genres NOT IN ["a"]` {
		t.Fatalf("genres-only mismatch: %q", got)
	}
	if got := exclusionFilter(nil, []string{"bg"}); got != `lang NOT IN ["bg"]` {
		t.Fatalf("lang-only mismatch: %q", got)
	}
}
