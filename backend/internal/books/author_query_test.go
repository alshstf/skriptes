package books

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNameCoversQuery(t *testing.T) {
	cases := []struct {
		last, first, middle, q string
		want                   bool
	}{
		{"Толстой", "Лев", "Николаевич", "толстой", true},
		{"Толстой", "Лев", "Николаевич", "Лев Толстой", true},
		{"Толстой", "Лев", "Николаевич", "толстой лев николаевич", true},
		{"Толстой", "Лев", "Николаевич", "лев", false},             // без фамилии
		{"Толстой", "Лев", "Николаевич", "толстой война", false},   // слово не из имени
		{"Толстой", "Алексей", "Николаевич", "лев толстой", false}, // другой Толстой
		{"Семёнов", "Юлиан", "", "семенов", true},                  // «ё» = «е»
		{"Ле Гуин", "Урсула", "", "ле гуин", true},
		{"Ле Гуин", "Урсула", "", "гуин", true}, // частица необязательна
		{"Салтыков-Щедрин", "Михаил", "", "салтыков-щедрин", true},
		{"Салтыков-Щедрин", "Михаил", "", "щедрин", false},
		{"Толстой", "Лев", "Николаевич", "толстой лев николаевич граф", false}, // длиннее ФИО
	}
	for _, c := range cases {
		require.Equal(t, c.want, nameCoversQuery(c.last, c.first, c.middle, queryWords(c.q)), "%s / %q", c.last, c.q)
	}
}

func TestAuthorQueryEligible(t *testing.T) {
	require.True(t, authorQueryEligible(ListParams{Query: "толстой"}, 0, 20))
	require.True(t, authorQueryEligible(ListParams{Query: "толстой"}, 40, 20))
	require.False(t, authorQueryEligible(ListParams{Query: "толстой"}, 30, 20), "не с границы страницы")
	require.False(t, authorQueryEligible(ListParams{Query: " "}, 0, 20))
	require.False(t, authorQueryEligible(ListParams{Query: "толстой", Sort: "year_desc"}, 0, 20))
	require.False(t, authorQueryEligible(ListParams{Query: "толстой", AuthorID: 7}, 0, 20))
	require.False(t, authorQueryEligible(ListParams{Query: "толстой", SeriesID: 7}, 0, 20))
}

func TestAndFilter(t *testing.T) {
	require.Equal(t, "(author_ids IN [1, 2])", andFilter("", authorIDsFilter([]MatchedAuthor{{ID: 1}, {ID: 2}})))
	require.Equal(t, `(lang = "ru") AND (NOT author_ids IN [3])`, andFilter(`lang = "ru"`, "NOT "+authorIDsFilter([]MatchedAuthor{{ID: 3}})))
}
