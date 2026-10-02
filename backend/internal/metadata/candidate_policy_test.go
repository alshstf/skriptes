package metadata

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// Политика приёма кандидата на примерах из разбора ошибок (case study #280).
func TestDecideCandidate(t *testing.T) {
	writer := CandidateFacts{Human: true, Occupations: []string{"писатель"}, Writer: true}
	cases := []struct {
		name  string
		q     AuthorQuery
		title string
		f     CandidateFacts
		match MatchKind
		want  bool
	}{
		{
			name:  "писатель — принять",
			q:     AuthorQuery{LastName: "Анджеевский", FirstName: "Ежи", MinBookYear: 1990, Genres: []string{"prose_classic"}},
			title: "Анджеевский, Ежи", f: CandidateFacts{Human: true, Occupations: []string{"писатель", "политик"}, Writer: true, Born: 1909, Died: 1983},
			want: true,
		},
		{
			name:  "строгий путь подтвердил — принять",
			q:     AuthorQuery{LastName: "Васильев", FirstName: "Борис", NetShare: 0.9},
			title: "Васильев, Борис Львович", f: CandidateFacts{Human: true, Occupations: []string{"сценарист"}}, match: MatchConfirmed,
			want: true,
		},
		{
			name: "книга автора в Wikidata — принять, даже без профессии",
			q:    AuthorQuery{LastName: "Карп", FirstName: "Харви", BookTitles: []string{"Самый счастливый малыш"}},
			f:    CandidateFacts{Human: true, Occupations: []string{"педиатр"}, Works: []string{"Самый счастливый малыш"}},
			want: true, title: "Карп, Харви",
		},
		{
			name: "космонавт у автора ЛитРПГ — сетевая литература без книги",
			q: AuthorQuery{LastName: "Губарев", FirstName: "Алексей", MiddleName: "Александрович",
				MinBookYear: 2019, NetShare: 1, Genres: []string{"network_literature", "sf_litrpg"}},
			title: "Губарев, Алексей Александрович",
			f:     CandidateFacts{Human: true, Occupations: []string{"космонавт", "писатель"}, Writer: true, Born: 1931, Died: 2015},
			want:  false,
		},
		{
			name:  "умер до 2000 — не автор сетевой литературы",
			q:     AuthorQuery{LastName: "Воробьев", FirstName: "Николай", NetShare: 0.4},
			title: "Воробьёв, Николай Иванович", f: CandidateFacts{Human: true, Occupations: []string{"мелиоратор"}, Died: 1993},
			want: false,
		},
		{
			name:  "родился позже книг",
			q:     AuthorQuery{LastName: "Никифоров", FirstName: "Сергей", MinBookYear: 1995},
			title: "Никифоров, Сергей Игоревич", f: CandidateFacts{Human: true, Occupations: []string{"видеоблогер"}, Born: 1993},
			want: false,
		},
		{
			name:  "другое отчество в названии статьи",
			q:     AuthorQuery{LastName: "Кузнецов", FirstName: "Виктор", MiddleName: "Иванович"},
			title: "Кузнецов, Виктор Васильевич (писатель)", f: writer,
			want: false,
		},
		{
			name:  "смежная профессия без подтверждения",
			q:     AuthorQuery{LastName: "Робинсон", FirstName: "Роберт", Genres: []string{"nonf_biography"}},
			title: "Робинсон, Роберт", f: CandidateFacts{Human: true, Occupations: []string{"химик"}, Adjacent: true},
			want: false,
		},
		{
			name:  "смежная профессия, тема книг совпала",
			q:     AuthorQuery{LastName: "Овсянников", FirstName: "Сергей", Genres: []string{"sci_medicine"}},
			title: "Овсянников, Сергей Алексеевич", f: CandidateFacts{Human: true, Occupations: []string{"психиатр", "учёный"}, Adjacent: true},
			want: true,
		},
		{
			name:  "смежная профессия, отчество совпало",
			q:     AuthorQuery{LastName: "Катков", FirstName: "Михаил", MiddleName: "Никифорович"},
			title: "Катков, Михаил Никифорович", f: CandidateFacts{Human: true, Occupations: []string{"историк"}, Adjacent: true},
			want: true,
		},
		{
			name:  "нет профессии и подтверждения",
			q:     AuthorQuery{LastName: "Ли", FirstName: "Сью", Genres: []string{"love_contemporary"}},
			title: "Ли, Сью", f: CandidateFacts{QID: "Q9", Human: true},
			want: false,
		},
		{
			name: "генерал с военными мемуарами",
			q: AuthorQuery{LastName: "Ротмистров", FirstName: "Павел", MiddleName: "Алексеевич",
				MinBookYear: 1960, Genres: []string{"nonf_military", "nonf_biography"}},
			title: "Ротмистров, Павел Алексеевич", f: CandidateFacts{Human: true, Occupations: []string{"офицер"}, Born: 1901, Died: 1982},
			want: true,
		},
		{
			name: "не писатель, тема не та",
			q: AuthorQuery{LastName: "Анисимов", FirstName: "Константин", MiddleName: "Александрович",
				Genres: []string{"sci_history"}},
			title: "Анисимов, Константин Александрович", f: CandidateFacts{Human: true, Occupations: []string{"актёр"}},
			want: false,
		},
		{
			name: "не писатель, умер до книг",
			q: AuthorQuery{LastName: "Миронов", FirstName: "Андрей", MiddleName: "Александрович",
				MinBookYear: 2007, Genres: []string{"sci_medicine", "home_health"}},
			title: "Миронов, Андрей Александрович", f: CandidateFacts{Human: true, Occupations: []string{"врач"}, Born: 1941, Died: 1987},
			want: false,
		},
		{
			name:  "иностранный спортсмен с автобиографией",
			q:     AuthorQuery{LastName: "Фуркад", FirstName: "Мартен", MinBookYear: 2016, Genres: []string{"home_sport", "nonf_biography"}},
			title: "Фуркад, Мартен", f: CandidateFacts{Human: true, Occupations: []string{"биатлонист"}, Born: 1988},
			want: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, why := decideCandidate(c.q, c.title, c.f, c.match)
			require.Equal(t, c.want, got, why)
			require.NotEmpty(t, why)
		})
	}
}

func TestPatronymic(t *testing.T) {
	require.True(t, patronymicConflict("Кузнецов, Виктор Васильевич (писатель)", "Иванович"))
	require.False(t, patronymicConflict("Кузнецов, Виктор Иванович", "Иванович"))
	require.False(t, patronymicConflict("Кузнецов, Виктор", "Иванович"), "в названии нет отчества — нечему противоречить")
	require.False(t, patronymicConflict("Капаев, Суюн Имамалиевич", ""), "у автора отчества нет")
	require.True(t, patronymicMatches("Ротмистров, Павел Алексеевич", "Алексеевич"))
	require.True(t, patronymicMatches("Берёзко, Георгий Сергеевич", "Сергеевич"))
	require.False(t, patronymicMatches("Губарев, Алексей Юрьевич", "Александрович"))
}

func TestWorksAnchor(t *testing.T) {
	require.True(t, worksAnchor([]string{"Левая рука тьмы"}, []string{"Левая рука тьмы", "Гробницы Атуана"}))
	require.True(t, worksAnchor([]string{"The Left Hand of Darkness"}, []string{"Левая рука тьмы", "The Left Hand of Darkness"}))
	require.True(t, worksAnchor([]string{"Гробницы Атуана (роман)"}, []string{"Гробницы Атуана"}), "одно содержит другое")
	require.False(t, worksAnchor([]string{"Он"}, []string{"Она"}), "короткие не сравниваем")
	require.False(t, worksAnchor(nil, []string{"Левая рука тьмы"}))
}

// Факты по одному QID в пределах TTL запрашиваются один раз (био и фото).
func TestCandidateCheck_FactsCached(t *testing.T) {
	calls := 0
	check := NewCandidateCheck(func(_ context.Context, qid string) (CandidateFacts, error) {
		calls++
		return CandidateFacts{QID: qid, Human: true, Occupations: []string{"писатель"}, Writer: true}, nil
	})
	q := AuthorQuery{LastName: "Пелевин", FirstName: "Виктор"}
	for i := 0; i < 3; i++ {
		ok, err := check(context.Background(), q, "wikipedia", "ru", "Пелевин, Виктор Олегович", "Q1", MatchName)
		require.NoError(t, err)
		require.True(t, ok)
	}
	require.Equal(t, 1, calls)
	_, _ = check(context.Background(), q, "wikipedia", "ru", "Пелевин, Виктор Олегович", "Q2", MatchName)
	require.Equal(t, 2, calls, "другой QID — новый запрос")
}

// Нестрогое имя — только с книгой автора в Wikidata; не человек — никогда (#280).
func TestDecideCandidate_LooseAndNotHuman(t *testing.T) {
	q := AuthorQuery{LastName: "Джексон", FirstName: "Ширли", BookTitles: []string{"Призрак дома на холме"}}
	writer := CandidateFacts{QID: "Q1", Human: true, Occupations: []string{"писательница"}, Writer: true}
	ok, why := decideCandidate(q, "Джексон, Шерли", writer, MatchLoose)
	require.False(t, ok, why)
	withBook := writer
	withBook.Works = []string{"Призрак дома на холме"}
	ok, why = decideCandidate(q, "Джексон, Шерли", withBook, MatchLoose)
	require.True(t, ok, why)

	series := CandidateFacts{QID: "Q2", Works: []string{"Скверное начало"}}
	ok, why = decideCandidate(AuthorQuery{LastName: "Сникет", FirstName: "Лемони", BookTitles: []string{"Скверное начало"}},
		"Лемони Сникет: 33 несчастья", series, MatchConfirmed)
	require.False(t, ok, why)
	require.Equal(t, "not a person", why)

	ok, why = decideCandidate(AuthorQuery{LastName: "Тукарам"}, "Тукарам",
		CandidateFacts{QID: "Q3", Human: true, Occupations: []string{"поэт"}, Writer: true}, MatchName)
	require.True(t, ok, why)
	ok, why = decideCandidate(AuthorQuery{LastName: "София"}, "София", CandidateFacts{QID: "Q4"}, MatchName)
	require.False(t, ok, why)
}

func TestLooseNameMatches(t *testing.T) {
	for _, c := range []struct {
		last, first, cand string
		want              bool
	}{
		{"Джексон", "Ширли", "Джексон, Шерли", true},
		{"Гоццано", "Гуидо", "Гоццано, Гвидо", true},
		{"Килуорт", "Гарри", "Килворт, Гарри", true},
		{"Глик", "Джеймс", "Глейк, Джеймс", true},
		{"Гомикава", "Дзюнпэй", "Гомикава, Дзюмпэй", true},
		{"Муравейка", "Иван", "Муравейко, Иван Андреевич", true},
		{"Иванова", "Мария", "Петрова, Мария", false},
		{"Гарднер", "Лиза", "Иван Гарднер", false},
	} {
		require.Equal(t, c.want, looseNameMatches(AuthorQuery{LastName: c.last, FirstName: c.first}, c.cand), "%s %s ~ %s", c.last, c.first, c.cand)
	}
}
