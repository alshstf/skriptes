package metadata

import "testing"

func TestTitlesMatch(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"Мастер и Маргарита", "Мастер и Маргарита. Роман", true},
		{"Dune: A Novel", "Dune", false}, // одно слово — не префикс-подзаголовок
		{"The Master and Margarita", "the master and margarita", true},
		{"Золотой телёнок", "Золотой теленок", true},
		{"Танька", "Антоновские яблоки", false},
		{"Свечка. Том 1", "Свечка. Том 2", false},
		{"", "Что-то", false},
	} {
		if got := titlesMatch(c.a, c.b); got != c.want {
			t.Errorf("titlesMatch(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestVolumeNumber(t *testing.T) {
	for in, want := range map[string]string{
		"Свечка. Том 2":                 "том 2",
		"Война и мир. Книга 1":          "книга 1",
		"Тихий Дон. Часть II":           "часть ii",
		"The Lord of the Rings, Vol. 3": "vol 3",
		"Томагавк":                      "",
		"Мастер и Маргарита":            "",
	} {
		if got := volumeNumber(in); got != want {
			t.Errorf("volumeNumber(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsStubSrcTitle(t *testing.T) {
	for in, want := range map[string]bool{
		"(no data for original title)": true,
		"(No Data For Original Title)": true,
		"???":                          true,
		"Мы":                           false,
		"It":                           false,
		"兄弟":                           false,
		"":                             false,
	} {
		if got := isStubSrcTitle(in); got != want {
			t.Errorf("isStubSrcTitle(%q) = %v, want %v", in, got, want)
		}
	}
}

// #279: бакет Tier-2 с разными названиями одного языка без src не склеивается,
// перевод + оригинал под одним ключом — склеивается; тома — конфликт.
func TestTier2BucketGates(t *testing.T) {
	stories := []groupBook{
		{normTitle: "танька", title: "Танька", lang: "ru"},
		{normTitle: "антоновские яблоки", title: "Антоновские яблоки", lang: "ru"},
	}
	if !sameLangTitleConflict(stories, []int{0, 1}) {
		t.Error("разные рассказы одного языка без src — конфликт")
	}
	translation := []groupBook{
		{normTitle: "мастер и маргарита", title: "Мастер и Маргарита", lang: "ru"},
		{normTitle: "the master and margarita", title: "The Master and Margarita", lang: "en", srcTitleNorm: "мастер и маргарита"},
	}
	if sameLangTitleConflict(translation, []int{0, 1}) {
		t.Error("перевод и оригинал на разных языках — не конфликт")
	}
	spellings := []groupBook{
		{normTitle: "двенадцать стульев", lang: "ru"},
		{normTitle: "двенадцать стульев", lang: "ru"},
	}
	if sameLangTitleConflict(spellings, []int{0, 1}) {
		t.Error("одинаковые названия — не конфликт")
	}
	volumes := []groupBook{
		{normTitle: "свечка. том 1", title: "Свечка. Том 1", lang: "ru"},
		{normTitle: "свечка. том 2", title: "Свечка. Том 2", lang: "ru", srcTitleNorm: "x"},
	}
	if !tier2BucketConflicts(volumes, []int{0, 1}) {
		t.Error("разные тома — конфликт")
	}
}
