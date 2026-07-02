package metadata

import "testing"

// normalizeLangCode — канонизация кодов языка перед записью в books.lang /
// books.src_lang (зеркало importer.normalizeLang, грабля №14): fb2 шлёт и
// 'EN', и 'ru-RU', и 'zh-Hans' — в колонке обязан лежать первичный субтег.
func TestNormalizeLangCode(t *testing.T) {
	cases := map[string]string{
		"en":       "en",
		"EN":       "en",
		" ru ":     "ru",
		"ru-RU":    "ru",
		"en_US":    "en",
		"zh-Hans":  "zh",
		"":         "",
		"  ":       "",
		"FR-ca ":   "fr",
		"sr_Latn ": "sr",
	}
	for in, want := range cases {
		if got := normalizeLangCode(in); got != want {
			t.Errorf("normalizeLangCode(%q) = %q, want %q", in, got, want)
		}
	}
}
