package importer

import "testing"

// Ключ гейта обязан следовать за версией схемы workDoc: бамп
// WorksIndexSchemaVersion = новый ключ app_settings = форс полного
// ResyncWorksIndex на ближайшем старте (см. main.go::runOnceWorksIndexSync).
// Меняешь схему — инкрементируй константу и обнови ожидание здесь.
func TestWorksIndexSyncedFlagKey(t *testing.T) {
	// v14 — авторы работы по правилу workauthors.Core (#464).
	if got, want := WorksIndexSyncedFlagKey(), "works_index_synced_v14"; got != want {
		t.Fatalf("WorksIndexSyncedFlagKey() = %q, want %q", got, want)
	}
}

func TestWorksSearchable(t *testing.T) {
	cases := map[int]string{
		0:  "title authors series",
		10: "title_s authors_s series_s",
		11: "title_s authors_s series_s alt_titles_s authors_latin",
	}
	for schema, want := range cases {
		got := ""
		for i, f := range worksSearchable(schema) {
			if i > 0 {
				got += " "
			}
			got += f
		}
		if got != want {
			t.Errorf("worksSearchable(%d) = %q, want %q", schema, got, want)
		}
	}
}

func TestAltTitlesForSearch(t *testing.T) {
	got := altTitlesForSearch("Хоббит", []string{"хоббит", "The Hobbit", "the hobbit", "  ", "Хоббит, или Туда и обратно", "Ёжик"})
	want := []string{"The Hobbit", "Хоббит, или Туда и обратно", "Ежик"}
	if len(got) != len(want) {
		t.Fatalf("altTitlesForSearch = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("altTitlesForSearch = %q, want %q", got, want)
		}
	}
	many := make([]string, 50)
	for i := range many {
		many[i] = string(rune('A'+i%26)) + string(rune('a'+i/26))
	}
	if n := len(altTitlesForSearch("x", many)); n != maxAltTitles {
		t.Fatalf("предел: %d, want %d", n, maxAltTitles)
	}
}
