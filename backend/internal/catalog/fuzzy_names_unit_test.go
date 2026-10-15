package catalog

import "testing"

func TestOSADistance(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"азимов", "азимов", 0},
		{"азимав", "азимов", 1},     // замена
		{"булгков", "булгаков", 1},  // пропуск
		{"булгкаов", "булгаков", 1}, // перестановка соседних — одна правка
		{"пратчет", "пратчетт", 1},
		{"", "кинг", 4},
		{"кинг", "", 4},
	} {
		if got := osaDistance([]rune(c.a), []rune(c.b)); got != c.want {
			t.Errorf("osaDistance(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestNameDistance(t *testing.T) {
	for _, c := range []struct {
		query, name string
		want        int
	}{
		{"бредбери", "бредбери реи", 0},     // вхождение
		{"рей брадбери", "бредбери реи", 1}, // «рей» — вхождение, «брадбери» — 1 правка
		{"брудбе", "бредбери реи", 1},       // недописанное слово с опечаткой
		{"толкиен", "толкин джон рональд руел", 1},
		{"достаевскии", "достоевскии федор михаилович", 1},
		{"дастаевскии", "достоевскии федор михаилович", 2}, // 11 букв — бюджет 2
		{"азимав", "азимов аизек", 1},
		{"азимав", "азамат", -1},             // 6 букв — бюджет 1, а правок 3
		{"кот", "кит джон", -1},              // 3 буквы — только вхождение
		{"стивен кинк", "кинг стивен", 1},    // каждое слово — своё слово имени
		{"стивен пушкин", "кинг стивен", -1}, // второе слово не похоже
		{"le guin", "le guin ursula k", 0},   // латиница
		{"ле гуин", "ле гуин урсула к", 0},
		{"", "кинг", -1},
	} {
		if got := nameDistance(fuzzyWords(c.query), c.name); got != c.want {
			t.Errorf("nameDistance(%q, %q) = %d, want %d", c.query, c.name, got, c.want)
		}
	}
}

func TestFuzzyProbe(t *testing.T) {
	for q, want := range map[string]string{
		"рэй бредбери": "бредбери",
		"Хайнлайн":     "хаинлаин",
		"кот":          "",
		"лев кот":      "",
		"":             "",
	} {
		if got := fuzzyProbe(fuzzyWords(q)); got != want {
			t.Errorf("fuzzyProbe(%q) = %q, want %q", q, got, want)
		}
	}
}
