package metadata

import "testing"

func TestAuthorNameMatches(t *testing.T) {
	cases := []struct {
		name      string
		last      string
		first     string
		candidate string
		want      bool
	}{
		// Главный кейс: однофамилец отвергается, настоящий — принимается.
		{"reject same surname different given", "Гарднер", "Лиза", "Иван Гарднер", false},
		{"reject same surname patronymic", "Гарднер", "Лиза", "Гарднер, Иван Алексеевич", false},
		{"accept latin form (Лиза≈Lisa)", "Гарднер", "Лиза", "Lisa Gardner", true},
		{"accept cyrillic comma form", "Гарднер", "Лиза", "Гарднер, Лиза", true},

		// Классики — не должны ломаться.
		{"accept Dostoevsky full", "Достоевский", "Фёдор", "Достоевский, Фёдор Михайлович", true},
		{"accept Tolstoy latin (Лев≈Leo)", "Толстой", "Лев", "Leo Tolstoy", true},
		{"reject Tolstoy wrong given", "Толстой", "Лев", "Алексей Толстой", false},

		// Инициал.
		{"accept initial form Л.", "Гарднер", "Лиза", "Л. Гарднер", true},

		// Нет имени — гейтим только по фамилии (status quo).
		{"surname only — accept any same surname", "Гарднер", "", "Иван Гарднер", true},
		{"surname only — reject other surname", "Гарднер", "", "Иван Петров", false},

		// Совсем другая фамилия — мимо.
		{"reject different surname", "Гарднер", "Лиза", "Лиза Симпсон", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := authorNameMatches(AuthorQuery{LastName: c.last, FirstName: c.first}, c.candidate)
			if got != c.want {
				t.Fatalf("authorNameMatches(last=%q first=%q, %q) = %v, want %v",
					c.last, c.first, c.candidate, got, c.want)
			}
		})
	}
}

func TestTranslitName(t *testing.T) {
	cases := map[string]string{
		"Гарднер": "gardner",
		"Лиза":    "liza",
		"Lisa":    "lisa",
		"Лев":     "lev",
		"Толстой": "tolstoi", // й→i; «tolstoi»≈«tolstoy» (dist 1) на этапе матча
		"Фёдор":   "fedor",
	}
	for in, want := range cases {
		if got := translitName(in); got != want {
			t.Errorf("translitName(%q) = %q, want %q", in, got, want)
		}
	}
}
