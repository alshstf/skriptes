package importer

import "testing"

func TestNormalizeLang(t *testing.T) {
	cases := map[string]string{
		"ru":   "ru",
		"RU":   "ru",
		"Ru":   "ru",
		" ru ": "ru",
		"EN":   "en",
		"":     "",
		"   ":  "",
		"BG\t": "bg",
	}
	for in, want := range cases {
		if got := normalizeLang(in); got != want {
			t.Errorf("normalizeLang(%q) = %q, want %q", in, got, want)
		}
	}
}
