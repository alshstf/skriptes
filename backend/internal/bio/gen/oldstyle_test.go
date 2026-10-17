package gen

import (
	"strings"
	"testing"

	"github.com/skriptes/skriptes/backend/internal/bio"
)

func TestJulianToGregorian(t *testing.T) {
	for in, want := range map[string]string{
		"1825-12-14": "1825-12-26", // восстание декабристов
		"1799-05-26": "1799-06-06", // Пушкин
		"1891-05-03": "1891-05-15", // Булгаков, XIX век: +12
		"1900-03-01": "1900-03-14", // после юлианского 29 февраля 1900: +13
		"1833-12-31": "1834-01-12", // сдвиг года
		"1700-02-18": "1700-02-28", // до 29 февраля 1700: +10
		"1700-03-01": "1700-03-12", // XVIII век: +11
		"1918-01-31": "1918-02-13", // последний день старого стиля
	} {
		if got, ok := julianToGregorian(in); !ok || got != want {
			t.Errorf("%s → %s, want %s", in, got, want)
		}
	}
	if _, ok := julianToGregorian("1825-12"); ok {
		t.Error("месяц не пересчитывается")
	}
}

func TestConvertOldStyle(t *testing.T) {
	if f, to := convertOldStyle("1850-01-03", "1854-02-15", bio.PrecisionDay); f != "1850-01-15" || to != "1854-02-27" {
		t.Fatalf("период: %s–%s", f, to)
	}
	if f, _ := convertOldStyle("1825-12", "", bio.PrecisionMonth); f != "1825-12" {
		t.Fatalf("месяц тронут: %s", f)
	}
	if f, _ := convertOldStyle("1930-04-18", "", bio.PrecisionDay); f != "1930-04-18" {
		t.Fatalf("после 1918 тронут: %s", f)
	}
}

func TestFromDraftConvertsOldStyle(t *testing.T) {
	d := &draft{Events: []draftEvent{
		{Type: "career", Tier: "minor", Short: "Звание камер-юнкера", Evidence: "31 декабря 1833 года"},
		{Type: "career", Tier: "minor", Short: "Избрание в академию", Evidence: "7 января 1833 года"},
	}}
	d.Events[0].Date.From, d.Events[0].Date.Precision, d.Events[0].Date.OldStyle = "1833-12-31", "day", true
	d.Events[1].Date.From, d.Events[1].Date.Precision = "1833-01-19", "day"
	a := fromDraft(&Sources{QID: "Q7200"}, d, nil)
	if a.Events[0].Date.From != "1833-01-19" || a.Events[1].Date.From != "1834-01-12" {
		t.Fatalf("даты: %s, %s", a.Events[0].Date.From, a.Events[1].Date.From)
	}
	if a.Events[1].Key != "career-1834" {
		t.Fatalf("ключ по году нового стиля: %s", a.Events[1].Key)
	}
}

const pushkinLead = `Александр Сергеевич Пушкин (26 мая [6 июня] 1799, Москва — 29 января [10 февраля] 1837, Санкт-Петербург) — русский поэт.
Известие о восстании декабристов 14 декабря 1825 года сильно взволновало Пушкина, после него он сжёг свои записки.
Николай I присваивает своему историографу 31 декабря 1833 года младшее придворное звание камер-юнкера.
18 февраля (2 марта) 1831 Александр Пушкин венчался с Натальей Гончаровой.
24 декабря 1836 года вышел четвёртый номер журнала.`

func pushkin() *Sources {
	return &Sources{QID: "Q7200", Born: "1799-06-06", Died: "1837-02-10",
		Facts:    []Fact{{Prop: "P40", Label: "Мария", Born: "1832-05-31"}},
		Articles: []Article{{Lang: "ru", Text: pushkinLead}}}
}

func pushkinAuthor(events ...bio.Event) *bio.Author {
	return &bio.Author{QID: "Q7200", Name: "Пушкин", Born: "1799-06-06", Died: "1837-02-10",
		Sources: []bio.Source{{ID: "ruwiki", URL: "u"}}, Events: events}
}

func TestOldStyleArticle(t *testing.T) {
	if !oldStyleArticle(pushkin()) {
		t.Error("Пушкин: двойная дата в преамбуле")
	}
	for _, lead := range []string{"Михаил Булгаков (3 [15] мая 1891, Киев — 10 марта 1940)", "Грин (11 (23) августа 1880 — 8 июля 1932)"} {
		if !oldStyleArticle(&Sources{Articles: []Article{{Lang: "ru", Text: lead}}}) {
			t.Errorf("не распознана: %s", lead)
		}
	}
	if oldStyleArticle(&Sources{Articles: []Article{{Lang: "ru", Text: "Эрих Мария Ремарк (22 июня 1898, Оснабрюк — 25 сентября 1970, Локарно)"}}}) {
		t.Error("Ремарк: новый стиль")
	}
}

func TestVerifyFixesOldStyleCopies(t *testing.T) {
	a := pushkinAuthor(
		ev("persecution-1825", "1825-12-14", bio.EventPersecution, "Восстание декабристов, сожжённые записки",
			"Известие о восстании декабристов 14 декабря 1825 года сильно взволновало Пушкина"),
		ev("career-1833", "1833-12-31", bio.EventCareer, "Звание камер-юнкера",
			"Николай I присваивает своему историографу 31 декабря 1833 года младшее придворное звание камер-юнкера"),
		ev("love-1831", "1831-03-02", bio.EventLove, "Венчание с Натальей Гончаровой",
			"18 февраля (2 марта) 1831 Александр Пушкин венчался с Натальей Гончаровой"),
		ev("career-1836", "1836-12-04", bio.EventCareer, "Четвёртый номер журнала",
			"24 декабря 1836 года вышел четвёртый номер журнала"),
	)
	out, flags := Verify(a, pushkin())
	got := map[string]bio.Event{}
	for _, e := range out.Events {
		got[e.Key] = e
	}
	if e := got["persecution-1825"]; e.Date.From != "1825-12" || e.Date.Precision != bio.PrecisionMonth {
		t.Errorf("декабристы: %+v (флаги %v)", e.Date, flags)
	}
	if e, ok := got["career-1834"]; !ok || e.Date.From != "1834-01" {
		t.Errorf("камер-юнкер: ключ и год нового стиля, вехи %v, флаги %v", out.Events, flags)
	}
	if e := got["love-1831"]; e.Date.From != "1831-03-02" || e.Date.Precision != bio.PrecisionDay {
		t.Errorf("двойная дата тронута: %+v", e.Date)
	}
	if e := got["career-1836"]; e.Date.From != "1836-12-04" {
		t.Errorf("«24 декабря» — не «4 декабря»: %+v", e.Date)
	}
	if len(flagsFor(flags, "persecution-1825")) != 1 || !strings.Contains(flagsFor(flags, "persecution-1825")[0].Msg, "1825-12-26") {
		t.Errorf("флаг с датой нового стиля: %v", flags)
	}
	if !sortedByDate(out.Events) {
		t.Error("вехи не отсортированы после пересчёта")
	}
}

func TestVerifyOldStyleSkipsWikidataAndNewStyleArticles(t *testing.T) {
	src := pushkin()
	src.Articles[0].Text += "\nДочь Мария родилась 31 мая 1832 года."
	a := pushkinAuthor(ev("child-1832", "1832-05-31", bio.EventChild, "Рождение дочери Марии", "Дочь Мария родилась 31 мая 1832 года"))
	if out, _ := Verify(a, src); out.Events[0].Date.From != "1832-05-31" {
		t.Errorf("дата из Wikidata тронута: %+v", out.Events[0].Date)
	}
	src = pushkin()
	src.Articles[0].Text = strings.Replace(src.Articles[0].Text, "26 мая [6 июня] 1799", "6 июня 1799", 1)
	src.Articles[0].Text = strings.Replace(src.Articles[0].Text, "29 января [10 февраля] 1837", "10 февраля 1837", 1)
	a = pushkinAuthor(ev("persecution-1825", "1825-12-14", bio.EventPersecution, "Восстание декабристов",
		"Известие о восстании декабристов 14 декабря 1825 года сильно взволновало Пушкина"))
	if out, _ := Verify(a, src); out.Events[0].Date.From != "1825-12-14" {
		t.Errorf("статья нового стиля — дата тронута: %+v", out.Events[0].Date)
	}
}

func TestVerifyAcceptsOldStyleYearShift(t *testing.T) {
	// Модель пометила old_style, программа пересчитала 31.12.1833 → 12.01.1834:
	// в цитате прошлый год, веха не отбраковывается.
	a := pushkinAuthor(ev("career-1834", "1834-01-12", bio.EventCareer, "Звание камер-юнкера",
		"Николай I присваивает своему историографу 31 декабря 1833 года младшее придворное звание камер-юнкера"))
	out, flags := Verify(a, pushkin())
	if len(out.Events) != 1 {
		t.Fatalf("веха отбракована: %v", flags)
	}
}

func sortedByDate(es []bio.Event) bool {
	for i := 1; i < len(es); i++ {
		if es[i].Date.From < es[i-1].Date.From {
			return false
		}
	}
	return true
}
