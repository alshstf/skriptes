package gen

import (
	"strings"
	"testing"

	"github.com/skriptes/skriptes/backend/internal/bio"
)

const article = `Фёдор Михайлович Достоевский родился 30 октября (11 ноября) 1821 года в Москве.
== Европа ==
Первая дочь, Софья, родилась в Женеве 5 марта 1868 года. 24 мая 1868 года Соня умерла от воспаления лёгких.
В апреле 1871 года в Висбадене Достоевский проиграл последние деньги и дал жене слово больше не играть.
== Библиография ==
Бедные люди (1846).`

func sources() *Sources {
	return &Sources{
		QID: "Q991", Label: "Фёдор Достоевский", Born: "1821-11-11", Died: "1881-02-09",
		Facts: []Fact{
			{Prop: "P40", Label: "Софья Достоевская", Born: "1868-03-05", Died: "1868-05-24"},
			{Prop: "P26", Label: "Анна Достоевская", Start: "1867-02-15"},
		},
		Articles: []Article{{Lang: "ru", Title: "Достоевский", Text: article}},
	}
}

func ev(key, from string, typ bio.EventType, short, quote string) bio.Event {
	e := bio.Event{Key: key, Type: typ, Tier: bio.TierMajor, Short: short}
	e.Date.From = from
	e.Date.Precision = bio.PrecisionDay
	if len(from) == 4 {
		e.Date.Precision = bio.PrecisionYear
	} else if len(from) == 7 {
		e.Date.Precision = bio.PrecisionMonth
	}
	if quote != "" {
		e.Evidence = &bio.Evidence{Source: "ruwiki", Quote: quote}
	}
	return e
}

func author(events ...bio.Event) *bio.Author {
	return &bio.Author{QID: "Q991", Name: "Фёдор Достоевский", Born: "1821-11-11", Died: "1881-02-09",
		Sources: []bio.Source{{ID: "ruwiki", URL: "u"}}, Events: events}
}

func flagsFor(flags []Flag, key string) []Flag {
	var out []Flag
	for _, f := range flags {
		if f.Key == key {
			out = append(out, f)
		}
	}
	return out
}

func TestVerifyKeepsGrounded(t *testing.T) {
	a := author(
		ev("child-1868", "1868-03-05", bio.EventChild, "Рождение дочери Сони", "Первая дочь, Софья, родилась в Женеве 5 марта 1868 года"),
		ev("loss-1868", "1868-05-24", bio.EventLoss, "Смерть трёхмесячной дочери Сони", "24 мая 1868 года Соня умерла от воспаления лёгких"),
	)
	out, flags := Verify(a, sources())
	if len(out.Events) != 2 {
		t.Fatalf("ожидалось 2 вехи, получено %d: %v", len(out.Events), flags)
	}
	for _, f := range flags {
		if f.Dropped {
			t.Fatalf("неожиданный drop: %v", f)
		}
	}
}

func TestVerifyDropsUngrounded(t *testing.T) {
	a := author(
		ev("loss-1868", "1868-05-24", bio.EventLoss, "Смерть дочери", ""),                                                // нет основания
		ev("other-1871", "1871-04", bio.EventOther, "Последняя игра в рулетку", "в Баден-Бадене проиграл всё"),           // цитаты нет в тексте
		ev("relocation-1870", "1870", bio.EventRelocation, "Переезд в Дрезден", "Первая дочь, Софья, родилась в Женеве"), // года нет ни в цитате, ни в фактах
	)
	out, flags := Verify(a, sources())
	if len(out.Events) != 0 {
		t.Fatalf("ожидалось 0 вех, получено %d", len(out.Events))
	}
	for _, k := range []string{"loss-1868", "other-1871", "relocation-1870"} {
		fs := flagsFor(flags, k)
		if len(fs) == 0 || !fs[0].Dropped {
			t.Errorf("%s: ожидался drop, получено %v", k, fs)
		}
	}
}

func TestVerifyTypographyTolerant(t *testing.T) {
	// Кавычки, «ё», неразрывные пробелы и многоточие в цитате модели.
	a := author(ev("other-1871", "1871-04", bio.EventOther, "Последняя игра в рулетку",
		"В апреле 1871 года в Висбадене Достоевский проиграл последние деньги и дал жене слово больше не играть"))
	a.Events[0].Evidence.Quote = "В апреле 1871 года в Висбадене Достоевский проиграл последние деньги… и дал жене слово больше не играть"
	out, flags := Verify(a, sources())
	if len(out.Events) != 1 {
		t.Fatalf("цитата с типографикой отброшена: %v", flags)
	}
}

func TestVerifyWikidataOnlyEvidence(t *testing.T) {
	a := author(
		ev("love-1867", "1867-02-15", bio.EventLove, "Венчание с Анной Сниткиной", "wikidata P26: Анна Достоевская 1867-02-15"),
		ev("love-1875", "1875", bio.EventLove, "Второй брак", "wikidata P26: кто-то 1875"),
	)
	out, flags := Verify(a, sources())
	if len(out.Events) != 1 || out.Events[0].Key != "love-1867" {
		t.Fatalf("ожидалась только love-1867, получено %+v / %v", out.Events, flags)
	}
}

func TestVerifyCrossChecksRelatives(t *testing.T) {
	// Утрата в год, когда по Wikidata никто из родных не умирал — флаг, но не drop.
	a := author(ev("loss-1871", "1871-04", bio.EventLoss, "Смерть друга", "В апреле 1871 года в Висбадене Достоевский проиграл последние деньги"))
	out, flags := Verify(a, sources())
	if len(out.Events) != 1 {
		t.Fatalf("drop вместо флага: %v", flags)
	}
	fs := flagsFor(flags, "loss-1871")
	if len(fs) == 0 || fs[0].Dropped || !strings.Contains(fs[0].Msg, "Wikidata") {
		t.Fatalf("ожидался флаг сверки, получено %v", fs)
	}
}

func TestVerifyDropsByDatasetRules(t *testing.T) {
	// Год в short — правило датасета → веха выброшена, остальные остаются.
	a := author(
		ev("child-1868", "1868-03-05", bio.EventChild, "Рождение дочери Сони в 1868", "Первая дочь, Софья, родилась в Женеве 5 марта 1868 года"),
		ev("loss-1868", "1868-05-24", bio.EventLoss, "Смерть трёхмесячной дочери Сони", "24 мая 1868 года Соня умерла от воспаления лёгких"),
	)
	out, flags := Verify(a, sources())
	if len(out.Events) != 1 || out.Events[0].Key != "loss-1868" {
		t.Fatalf("ожидалась только loss-1868: %+v / %v", out.Events, flags)
	}
}

func TestNormalizeAndQuoteFound(t *testing.T) {
	if normalize("Ёжик — «в тумане»!") != "ежик в тумане" {
		t.Fatalf("normalize: %q", normalize("Ёжик — «в тумане»!"))
	}
	if quoteFound("коротко", []string{normalize("слишком коротко")}) {
		t.Fatal("цитата короче 12 символов не должна приниматься")
	}
	text := normalize("Первая фраза про Болдино осенью. Другая фраза. 5 декабря 1830 года вернулся в Москву.")
	if !quoteFound("Первая фраза про Болдино осенью … 5 декабря 1830 года вернулся в Москву", []string{text}) {
		t.Fatal("цитата из двух фрагментов через «…» должна приниматься")
	}
	if quoteFound("Первая фраза про Болдино осенью … в Петербург уехал навсегда", []string{text}) {
		t.Fatal("фрагмент, которого нет в тексте, должен отклоняться")
	}
}

func TestNormalizeDropsCombiningMarks(t *testing.T) {
	if normalize("Михаи́л Афана́сьевич") != "михаил афанасьевич" {
		t.Fatalf("normalize: %q", normalize("Михаи́л Афана́сьевич"))
	}
}
