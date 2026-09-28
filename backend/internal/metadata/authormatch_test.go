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

		// #280: в одном алфавите — только точное совпадение (опечатка ≠ другой человек).
		{"reject cyrillic near-miss surname", "Фирсов", "Андрей", "Фурсов, Андрей Ильич", false},
		{"reject cyrillic near-miss surname 2", "Каменова", "Анна", "Каменкова, Анна Владимировна", false},
		{"reject cyrillic near-miss given", "Иванова", "Мария", "Иванова, Марина Сергеевна", false},
		{"reject latin near-miss", "Gardner", "Lisa", "Lisa Garner", false},
		{"accept cross-script near form", "Гарднер", "Лиза", "Lisa Gardner", true},
		// Выборка с прода: удвоенные буквы в фамилии, вставка/пропуск буквы в имени.
		{"accept doubled letters in surname", "Флевеллинг", "Линн", "Флевелинг, Линн", true},
		{"accept one-gap given name", "Блей", "Фритц", "Блей, Фриц", true},
		{"reject one-gap surname", "Юрмин", "Георгий", "Юрин, Георгий Васильевич", false},
		{"reject substituted given name", "Иванова", "Мария", "Иванова, Марина", false},
		// Перепроверка 1.16.0: разные передачи иностранной фамилии в одном алфавите.
		{"accept Г/Х in surname", "Херберт", "Фрэнк", "Герберт, Фрэнк", true},
		{"accept З/С in surname", "Зузак", "Маркус", "Зусак, Маркус", true},
		{"reject Ж/Ш in surname", "Жуков", "Георгий", "Шуков, Георгий", false},
		{"reject vowel with Г/Х", "Херберт", "Фрэнк", "Гарберт, Фрэнк", false},
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

// #280: имя из одного слова («София», «2B») — только с подтверждением книгой.
func TestAuthorQuery_StrictOneWord(t *testing.T) {
	if !(AuthorQuery{LastName: "София"}).Strict() {
		t.Error("фамилия без имени — строгий режим")
	}
	if (AuthorQuery{LastName: "Гарднер", FirstName: "Лиза"}).Strict() {
		t.Error("фамилия и имя — обычный поиск")
	}
	if (AuthorQuery{FullName: "Достоевский Фёдор"}).Strict() {
		t.Error("нет разбора на фамилию/имя — гейтить нечем, обычный поиск")
	}
}

// Статья под псевдонимом годится, если называет автора (#280).
func TestMentionsAuthor(t *testing.T) {
	q := AuthorQuery{LastName: "Чхартишвили", FirstName: "Григорий"}
	if !mentionsAuthor("Бори\u0301с Аку\u0301нин (настоящее имя — Григо\u0301рий Ша\u0301лвович Чхартишви\u0301ли) — писатель", q) {
		t.Error("ударения в тексте не мешают найти настоящее имя")
	}
	if mentionsAuthor("Александр Флит — русский поэт", AuthorQuery{LastName: "Флинт", FirstName: "Александра"}) {
		t.Error("другой человек — не упоминание")
	}
}

func TestArticleIsAuthor(t *testing.T) {
	matheson := AuthorQuery{LastName: "Матесон", FirstName: "Ричард"}
	if !articleIsAuthor(matheson, "Мэтисон, Ричард",
		"Ри́чард Мэ́тисон (англ. Richard Burton Matheson; 20 февраля 1926, Аллендейл) — американский писатель") {
		t.Error("начало статьи называет автора латиницей — тот же человек")
	}
	if articleIsAuthor(AuthorQuery{LastName: "Флинт", FirstName: "Александра"}, "Флит, Александр",
		"Алекса́ндр Ива́нович Флит (род. 1950) — российский поэт") {
		t.Error("редирект на другого человека — не автор")
	}
	if articleIsAuthor(matheson, "Мэтисон, Ричард",
		"Ри́чард Мэ́тисон (англ. Richard Mathison) — американский художник") {
		t.Error("другая фамилия и латиницей — не автор")
	}
}
