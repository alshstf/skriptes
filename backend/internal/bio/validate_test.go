package bio

import (
	"strconv"
	"strings"
	"testing"
)

func base() *Author {
	return &Author{
		QID: "Q991", Name: "Фёдор Достоевский", Born: "1821-11-11", Died: "1881-02-09",
		Sources:  []Source{{ID: "ruwiki", URL: "https://ru.wikipedia.org/wiki/X"}},
		Chapters: []Chapter{{Key: "youth", Title: "Детство", From: 1821, To: 1843}, {Key: "late", Title: "Поздние годы", From: 1844, To: 1881}},
		Works:    []Work{{Ref: "idiot", Title: "Идиот", Written: &Written{From: 1867, To: 1869}}},
		Events: []Event{
			{Key: "loss-1868", Date: Date{From: "1868-05-24", Precision: PrecisionDay}, Type: EventLoss, Tier: TierMajor,
				Short: "Смерть трёхмесячной дочери Сони", Long: "Умерла в Женеве.", Chapter: "late",
				Works: []WorkLink{{Ref: "idiot", Note: "работа шла в эти недели"}}, Evidence: &Evidence{Source: "ruwiki", Quote: "24 мая 1868 года"}},
		},
	}
}

func hasErr(ps Problems, sub string) bool {
	for _, p := range ps.Errors() {
		if strings.Contains(p.Path+": "+p.Msg, sub) {
			return true
		}
	}
	return false
}

func TestValidateClean(t *testing.T) {
	if ps := Validate(base()).Errors(); len(ps) > 0 {
		t.Fatalf("неожиданные ошибки: %s", ps.Error())
	}
}

func TestValidateRules(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(a *Author)
		want   string
	}{
		{"год в short", func(a *Author) { a.Events[0].Short = "Смерть дочери в 1868" }, "год в формулировке"},
		{"название работы в short", func(a *Author) { a.Events[0].Short = "Работа над «Идиотом» в Женеве" }, "название работы"},
		{"short длиннее 60", func(a *Author) { a.Events[0].Short = strings.Repeat("а", 61) }, "> 60"},
		{"ключ не совпадает с типом", func(a *Author) { a.Events[0].Key = "love-1868" }, "тип в ключе"},
		{"ключ не совпадает с годом", func(a *Author) { a.Events[0].Key = "loss-1869" }, "год в ключе"},
		{"точность не совпадает", func(a *Author) { a.Events[0].Date.Precision = PrecisionMonth }, "не соответствует точности"},
		{"дата вне жизни", func(a *Author) {
			a.Events[0].Date = Date{From: "1900", Precision: PrecisionYear}
			a.Events[0].Key = "loss-1900"
		}, "позже смерти"},
		{"нет главы", func(a *Author) { a.Events[0].Chapter = "nope" }, "нет главы"},
		{"нет работы", func(a *Author) { a.Events[0].Works[0].Ref = "besy" }, "нет работы"},
		{"to раньше from", func(a *Author) { a.Events[0].Date.To = "1868-01-01" }, "раньше from"},
		{"неизвестный тип", func(a *Author) { a.Events[0].Type = "party"; a.Events[0].Key = "party-1868" }, "неизвестный тип"},
		{"главы пересекаются", func(a *Author) { a.Chapters[1].From = 1840 }, "пересекаются"},
		{"период написания после смерти", func(a *Author) { a.Works[0].Written.To = 1890 }, "после смерти"},
		{"плохой qid", func(a *Author) { a.QID = "991" }, "qid"},
		{"дубль ключа", func(a *Author) { a.Events = append(a.Events, a.Events[0]) }, "дубль ключа"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := base()
			c.mutate(a)
			ps := Validate(a)
			if !hasErr(ps, c.want) {
				t.Fatalf("ожидалась ошибка %q, получено: %v", c.want, ps)
			}
		})
	}
}

func TestValidateCaps(t *testing.T) {
	a := base()
	a.Chapters = nil
	a.Events = nil
	for y := 1830; y < 1830+MaxMajorEvents+1; y++ {
		a.Events = append(a.Events, Event{Key: "other-" + itoa(y), Date: Date{From: itoa(y), Precision: PrecisionYear}, Type: EventOther, Tier: TierMajor, Short: "Веха"})
	}
	if !hasErr(Validate(a), "major-вех") {
		t.Fatal("кап major не сработал")
	}
	// Якоря рождения/смерти в кап не входят.
	a.Events = a.Events[:MaxMajorEvents]
	a.Events = append(a.Events,
		Event{Key: "birth-1821", Date: Date{From: "1821", Precision: PrecisionYear}, Type: EventBirth, Tier: TierMajor, Short: "Рождение"},
		Event{Key: "death-1881", Date: Date{From: "1881", Precision: PrecisionYear}, Type: EventDeath, Tier: TierMajor, Short: "Смерть"})
	if hasErr(Validate(a), "major-вех") {
		t.Fatal("якоря посчитаны в кап major")
	}
}

func itoa(i int) string { return strconv.Itoa(i) }
