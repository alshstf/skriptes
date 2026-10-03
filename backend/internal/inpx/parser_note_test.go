package inpx

import "testing"

func TestParseAuthors_Note(t *testing.T) {
	got := parseAuthors("Антоний [Блум],,:Гибсон [фантаст],Уильям,:Васильев [#27941],Сергей,Викторович:Антонов [художник， писатель],Сергей,Александрович:Иванов,Иван,:Брак [],Ян,:")
	want := []Author{
		{LastName: "Антоний", Note: "Блум"},
		{LastName: "Гибсон", FirstName: "Уильям", Note: "фантаст"},
		{LastName: "Васильев", FirstName: "Сергей", MiddleName: "Викторович", Note: "#27941"},
		{LastName: "Антонов", FirstName: "Сергей", MiddleName: "Александрович", Note: "художник， писатель"},
		{LastName: "Иванов", FirstName: "Иван"},
		{LastName: "Брак", FirstName: "Ян"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d authors, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("author %d: got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestSplitNameNote(t *testing.T) {
	cases := []struct{ in, name, note string }{
		{"Гибсон [фантаст]", "Гибсон", "фантаст"},
		{"  Де [x] Ла  ", "Де Ла", "x"},
		{"Без скобок", "Без скобок", ""},
		{"Незакрытая [скобка", "Незакрытая [скобка", ""},
	}
	for _, c := range cases {
		name, note := splitNameNote(c.in)
		if name != c.name || note != c.note {
			t.Errorf("splitNameNote(%q) = (%q, %q), want (%q, %q)", c.in, name, note, c.name, c.note)
		}
	}
}
