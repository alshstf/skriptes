package metadata

import (
	"strings"
	"testing"
)

// splitSentences: инициалы «А. С.» и сокращения не рвут предложение.
func TestSplitSentences(t *testing.T) {
	text := "В 1849 году А. С. Пушкин не жил. Достоевский был арестован по делу петрашевцев. " +
		"Жил на ул. Малой Морской, д. 4. В 1854 г. вышел на поселение!"
	got := splitSentences(text)
	want := []string{
		"В 1849 году А. С. Пушкин не жил.",
		"Достоевский был арестован по делу петрашевцев.",
		"Жил на ул. Малой Морской, д. 4.",
		"В 1854 г. вышел на поселение!",
	}
	if len(got) != len(want) {
		t.Fatalf("предложений %d, ждали %d: %q", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("предложение %d: %q != %q", i, got[i], want[i])
		}
	}
}

// bioZone: секции 2-го уровня с био-заголовками; «Библиография»/«Память» — мимо.
func TestBioZone(t *testing.T) {
	text := "Преамбула статьи.\n" +
		"== Биография ==\n" +
		"Родился в Москве.\n" +
		"=== Детство ===\n" +
		"Играл в саду.\n" +
		"== Творчество ==\n" +
		"Написал роман.\n" +
		"== Библиография ==\n" +
		"М.: Наука, 1978.\n" +
		"== Память ==\n" +
		"Памятник в Москве.\n"
	zone := bioZone(text)
	for _, wantIn := range []string{"Родился в Москве", "Играл в саду", "Написал роман"} {
		if !strings.Contains(zone, wantIn) {
			t.Fatalf("биозона потеряла %q: %q", wantIn, zone)
		}
	}
	for _, wantOut := range []string{"Наука", "Памятник"} {
		if strings.Contains(zone, wantOut) {
			t.Fatalf("биозона захватила мусор %q: %q", wantOut, zone)
		}
	}

	// Без био-заголовков — первые 60%.
	flat := strings.Repeat("Абзац текста без заголовков.\n", 10)
	if got := bioZone(flat); len([]rune(got)) >= len([]rune(flat)) {
		t.Fatalf("fallback 60%% не сработал: %d из %d", len([]rune(got)), len([]rune(flat)))
	}
}

// milestoneYear: lifespan-фильтр [рожд..смерть+2] — годы сюжета отбрасываются.
func TestMilestoneYear(t *testing.T) {
	cases := []struct {
		sent         string
		birth, death int
		want         int
	}{
		// Год сюжета «Войны и мира» — до рождения Толстого (замер: главный шум).
		{"Роман описывает события 1812 года.", 1828, 1910, 0},
		{"В 1869 году завершил «Войну и мир».", 1828, 1910, 1869},
		// Первый год вне жизни, второй — в жизни.
		{"Описал войну 1812 года по рассказам, а в 1854 сам попал в Севастополь.", 1828, 1910, 1854},
		// Посмертная публикация: смерть+2 — ок, дальше — нет.
		{"Роман опубликован в 1911 году.", 1828, 1910, 1911},
		{"Собрание сочинений вышло в 1928 году.", 1828, 1910, 0},
		// Живой автор (death=0) — верхней границы нет.
		{"В 2019 году вышел новый роман.", 1948, 0, 2019},
		// Декада — не год (живой прогон: «в конце 1840-х сочинил вальс»).
		{"В конце 1840-х годов сочинил вальс.", 1828, 1910, 0},
		// Даты жизни родни в скобках — не год события.
		{"Николай Ильич Толстой (1794—1837) был отцом писателя.", 1828, 1910, 0},
		{"В 1837 году (осенью) семья переехала в Москву.", 1828, 1910, 1837},
	}
	for _, c := range cases {
		if got := milestoneYear(c.sent, c.birth, c.death); got != c.want {
			t.Fatalf("%q → %d, ждали %d", c.sent, got, c.want)
		}
	}
}

// Классификатор: приоритет по убыванию веса, ru+en ключи.
func TestClassifyMilestone(t *testing.T) {
	cases := map[string]string{
		"Был арестован по делу петрашевцев.":              EventPersecution,
		"Отправлен на каторгу в Омск.":                    EventPersecution,
		"Участвовал в обороне Севастополя.":               EventWar,
		"Умерла его дочь Соня.":                           EventLoss,
		"Женился на Софье Берс.":                          EventLove,
		"Родился сын Сергей.":                             EventChild,
		"Переехал в Петербург.":                           EventRelocation,
		"Окончил Казанский университет.":                  EventEducation,
		"Опубликован роман «Бедные люди».":                EventCareer,
		"Начал работу над «Идиотом».":                     EventCreationMode,
		"Получил Нобелевскую премию.":                     EventAward,
		"Заболел туберкулёзом.":                           EventIllness,
		"Пережил духовный кризис.":                        EventSpiritual,
		"Остался без средств, долги росли.":               EventPoverty,
		"He was arrested and sent to a labor camp.":       EventPersecution,
		"Случилось нечто без опознавательных знаков тут.": EventOther,
		// Арест во время войны → persecution (первый по приоритету).
		"Во время войны был арестован.": EventPersecution,
	}
	for sent, want := range cases {
		if got := classifyMilestone(sent); got != want {
			t.Fatalf("%q → %s, ждали %s", sent, got, want)
		}
	}

	// Название произведения в кавычках не должно задавать тип: «Война и мир»
	// в творческой вехе — НЕ war (живой прогон: 6 ложных war у Толстого).
	sent := `Первые четыре тома «Войны и мира» быстро разошлись, и понадобилось второе издание.`
	if got := classifyMilestone(quotedRe.ReplaceAllString(sent, "«…»")); got != EventCareer {
		t.Fatalf("кавычки должны выпадать из классификации: got %s", got)
	}
}

// extractWikiMilestones: полный конвейер — фильтры, дедуп (год,тип), якоря.
func TestExtractWikiMilestones(t *testing.T) {
	text := "Преамбула без годов.\n" +
		"== Биография ==\n" +
		"Родился в 1821 году в Москве.\n" + // дубль якоря рождения → отсев
		"В 1849 году арестован по делу петрашевцев.\n" +
		"В 1849 году приговорён к расстрелу, заменённому каторгой.\n" + // дубль (1849, persecution)
		"Роман описывает 1812 год.\n" + // вне жизни
		"В 1866 году женился на Анне Сниткиной.\n" +
		"Короток 1866.\n" + // < 20 рун
		"Издание: М.: Наука, 1866. — 320 с.\n" + // библиография
		"В 1875 году открыт музей его имени.\n" + // посмертное/память-шум
		"В 1881 году скончался в Петербурге.\n" + // дубль якоря смерти → отсев
		"== Память ==\n" +
		"В 1956 году выпущена марка.\n" // вне биозоны И вне жизни
	events := extractWikiMilestones("ru", "https://ru.wikipedia.org/wiki/X", text, 1821, 1881)

	if len(events) != 2 {
		t.Fatalf("ждали 2 вехи (арест-1849, брак-1866), got %d: %+v", len(events), events)
	}
	arrest := events[0]
	if arrest.Type != EventPersecution || arrest.YearFrom != 1849 || arrest.Weight != 5 {
		t.Fatalf("арест: %+v", arrest)
	}
	if !strings.HasPrefix(arrest.ExtKey, "ru:") {
		t.Fatalf("ext_key без языка: %q", arrest.ExtKey)
	}
	if arrest.Quote == "" || arrest.Source != "wikipedia" || arrest.URL == "" {
		t.Fatalf("quote/source/url обязательны: %+v", arrest)
	}
	if events[1].Type != EventLove || events[1].YearFrom != 1866 {
		t.Fatalf("брак: %+v", events[1])
	}
}

// mergeAuthorEvents: wd-приоритет при дубле (год,тип), перенос цитаты, кап.
func TestMergeAuthorEvents(t *testing.T) {
	wd := []AuthorEvent{
		{Source: "wikidata", ExtKey: "P569:-:1821", Type: EventBirth, YearFrom: 1821},
		{Source: "wikidata", ExtKey: "P26:Q1:1866", Type: EventLove, YearFrom: 1866, Title: "Брак: Анна Сниткина"},
	}
	wiki := []AuthorEvent{
		{Source: "wikipedia", ExtKey: "ru:aaaa", Type: EventLove, YearFrom: 1866, Quote: "В 1866 году женился.", Weight: 3},
		{Source: "wikipedia", ExtKey: "ru:bbbb", Type: EventPersecution, YearFrom: 1849, Quote: "Арестован.", Weight: 5},
	}
	merged := mergeAuthorEvents(wd, wiki)
	if len(merged) != 3 {
		t.Fatalf("ждали 3 (wd-брак поглотил wiki-дубль): %+v", merged)
	}
	var wdLove *AuthorEvent
	for i := range merged {
		if merged[i].ExtKey == "P26:Q1:1866" {
			wdLove = &merged[i]
		}
		if merged[i].ExtKey == "ru:aaaa" {
			t.Fatalf("wiki-дубль брака должен быть поглощён")
		}
	}
	if wdLove == nil || wdLove.Quote != "В 1866 году женился." {
		t.Fatalf("цитата wiki-дубля должна перейти в wd-событие: %+v", wdLove)
	}

	// Кап: 70 событий веса 1 + якоря → ≤ maxTimelineEvents (+ якоря сверх капа).
	var many []AuthorEvent
	many = append(many, AuthorEvent{ExtKey: "P569:-:1800", Type: EventBirth, YearFrom: 1800})
	many = append(many, AuthorEvent{ExtKey: "P570:-:1880", Type: EventDeath, YearFrom: 1880})
	for i := 0; i < 70; i++ {
		many = append(many, AuthorEvent{ExtKey: strings.Repeat("x", i%7+1), Type: EventResidence, YearFrom: 1801 + i, Weight: 1})
	}
	capped := mergeAuthorEvents(many, nil)
	if len(capped) != maxTimelineEvents+2 {
		t.Fatalf("кап: got %d, want %d (+2 якоря)", len(capped), maxTimelineEvents+2)
	}
	hasBirth, hasDeath := false, false
	for _, ev := range capped {
		hasBirth = hasBirth || ev.Type == EventBirth
		hasDeath = hasDeath || ev.Type == EventDeath
	}
	if !hasBirth || !hasDeath {
		t.Fatalf("якоря не должны отсеиваться капом")
	}
}

// clipSentence: длинное предложение режется по границе слова.
func TestClipSentence(t *testing.T) {
	if got := clipSentence("Короткое предложение.", 90); got != "Короткое предложение" {
		t.Fatalf("короткое: %q", got)
	}
	long := strings.Repeat("слово ", 30) + "конец."
	got := clipSentence(long, 90)
	if len([]rune(got)) > 91 || !strings.HasSuffix(got, "…") {
		t.Fatalf("длинное: %d рун, %q", len([]rune(got)), got)
	}
}
