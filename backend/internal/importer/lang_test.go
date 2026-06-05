package importer

import "testing"

func TestNormalizeLang(t *testing.T) {
	cases := map[string]string{
		"ru":      "ru",
		"RU":      "ru",
		"Ru":      "ru",
		" ru ":    "ru",
		"EN":      "en",
		"":        "",
		"   ":     "",
		"BG\t":    "bg",
		"ru-RU":   "ru", // региональный субтег срезается
		"ru_RU":   "ru", // тот же случай через '_'
		"EN-US":   "en",
		"zh-Hans": "zh", // скриптовый субтег тоже срезается
		"ru-":     "ru",
		"-ru":     "", // субтег с самого начала → пусто (не язык)
	}
	for in, want := range cases {
		if got := normalizeLang(in); got != want {
			t.Errorf("normalizeLang(%q) = %q, want %q", in, got, want)
		}
	}
}
