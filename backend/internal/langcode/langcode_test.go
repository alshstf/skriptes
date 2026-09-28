package langcode

import "testing"

func TestCanonical(t *testing.T) {
	for in, want := range map[string]string{
		// Прод-кейсы #287.
		"en": "en", "EN": "en", " ru-RU ": "ru", "zh_Hans": "zh",
		"spa": "es", "ger": "de", "deu": "de", "eng": "en", "fra": "fr", "ukr": "uk", "jpn": "ja", "tib": "bo",
		"jp": "ja", "gr": "el", "cz": "cs", "dk": "da", "by": "be", "ua": "uk", "tm": "tk",
		"iw": "he", "in": "id", "ji": "yi", "jw": "jv", "mo": "ro",
		"английски": "en", "Руски": "ru", "russian": "ru", "японский": "ja", "turkmen": "tk", "balkar": "krc",
		"grc": "grc", "sah": "sah", "udm": "udm", "sh": "sh", "cu": "cu",
		// Мусор.
		"?": "", "u": "", "ud": "", "gm": "", "bl": "", "dr": "", "po": "", "ан": "", "кг": "",
		"en,tl": "", "старокитайски": "", "": "",
	} {
		if got := Canonical(in); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", in, got, want)
		}
	}
}
