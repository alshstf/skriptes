package books

import "testing"

func TestDisplayNote(t *testing.T) {
	for in, want := range map[string]string{
		"Блум": "Блум", " фантаст ": "фантаст", "#17465": "", "#": "#", "#12a": "#12a", "": "", "1840-1913": "1840-1913",
	} {
		if got := DisplayNote(in); got != want {
			t.Errorf("DisplayNote(%q) = %q, want %q", in, got, want)
		}
	}
}
