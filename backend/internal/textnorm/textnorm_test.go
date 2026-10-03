package textnorm

import "testing"

func TestFoldYo(t *testing.T) {
	for in, want := range map[string]string{
		"Три мушкетёра": "Три мушкетера",
		"ЁЖИК":          "ЕЖИК",
		"лёд и мёд":     "лед и мед",
		"без изменений": "без изменений",
		"":              "",
	} {
		if got := FoldYo(in); got != want {
			t.Errorf("FoldYo(%q) = %q, want %q", in, got, want)
		}
	}
}
