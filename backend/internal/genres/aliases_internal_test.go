package genres

import "testing"

// Каждый алиас ведёт в код словаря и сам кодом словаря не является (иначе
// MergeAliases удалил бы жанр словаря).
func TestAliases_AllPointIntoDictionary(t *testing.T) {
	entries, err := Dictionary()
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, e := range entries {
		known[e.Code] = true
	}
	for alias, canon := range aliases {
		if !known[canon] {
			t.Errorf("%q → %q: нет в словаре", alias, canon)
		}
		if known[alias] {
			t.Errorf("%q — сам код словаря", alias)
		}
	}
}
