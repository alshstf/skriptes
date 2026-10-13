package gen

import (
	"strconv"
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

// Механические огрехи модели правятся, а не выбрасывают веху/файл: snake_case в
// ref и ключах глав (вместе со ссылками из вех), длинный long, лишние major.
func TestVerifyNormalizesRefsAndTrims(t *testing.T) {
	long := strings.Repeat("Слово ", 40) + "Конец первого предложения. " + strings.Repeat("хвост ", 20) // 387 > 300, граница предложения на 267
	e := ev("loss-1868", "1868-05-24", bio.EventLoss, "Смерть трёхмесячной дочери Сони", "24 мая 1868 года Соня умерла от воспаления лёгких")
	e.Long = long
	e.Chapter = "Europe_Years"
	e.Works = []bio.WorkLink{{Ref: "the_idiot", Note: strings.Repeat("n", bio.MaxNoteRunes+1)}}
	a := author(e)
	a.Chapters = []bio.Chapter{{Key: "Europe_Years", Title: "Европа", From: 1821, To: 1881}}
	a.Works = []bio.Work{{Ref: "the_idiot", Title: "Идиот"}, {Ref: "crime-and-punishment", Title: "Преступление и наказание"}}
	out, flags := Verify(a, sources())
	if len(out.Events) != 1 {
		t.Fatalf("веха должна остаться: %+v / %v", out.Events, flags)
	}
	got := out.Events[0]
	if out.Works[0].Ref != "the-idiot" || got.Works[0].Ref != "the-idiot" {
		t.Errorf("ref не нормализован: %q / %q", out.Works[0].Ref, got.Works[0].Ref)
	}
	if out.Chapters[0].Key != "europe-years" || got.Chapter != "europe-years" {
		t.Errorf("ключ главы не нормализован: %q / %q", out.Chapters[0].Key, got.Chapter)
	}
	if n := len([]rune(got.Long)); n > bio.MaxLongRunes || !strings.HasSuffix(got.Long, "предложения.") {
		t.Errorf("long не усечён по предложению: %d %q", n, got.Long)
	}
	if got.Works[0].Note != "" {
		t.Errorf("длинная note должна быть снята")
	}
	if ps := bio.Validate(out).Errors(); len(ps) > 0 {
		t.Errorf("после правок файл должен быть валиден: %v", ps)
	}
	if len(flagsFor(flags, "works")) != 1 || len(flagsFor(flags, "chapters")) != 1 || len(flagsFor(flags, "loss-1868")) < 2 {
		t.Errorf("ожидались флаги о правках: %v", flags)
	}
}

func TestDemoteExtraMajors(t *testing.T) {
	var events []bio.Event
	for i := 0; i <= bio.MaxMajorEvents+1; i++ { // 22 major
		y := 1850 + i
		events = append(events, ev("other-"+strconv.Itoa(y), strconv.Itoa(y), bio.EventOther, "Веха", ""))
	}
	events[len(events)-1].Works = []bio.WorkLink{{Ref: "w"}} // поздняя, но с линком — понижается последней
	a := author(events...)
	flags := demoteExtraMajors(a)
	major := 0
	for _, e := range a.Events {
		if e.Tier == bio.TierMajor {
			major++
		}
	}
	if major != bio.MaxMajorEvents {
		t.Fatalf("major после понижения %d, ожидалось %d: %v", major, bio.MaxMajorEvents, flags)
	}
	if a.Events[len(a.Events)-1].Tier != bio.TierMajor {
		t.Errorf("веха с линком должна остаться major")
	}
	for _, k := range []string{"other-1870", "other-1869"} {
		if len(flagsFor(flags, k)) != 1 {
			t.Errorf("ожидалось понижение %s: %v", k, flags)
		}
	}
	// Сверх MaxEvents — поздние minor без линков выбрасываются.
	extra := bio.MaxEvents - len(a.Events) + 3
	for i := 0; i < extra; i++ {
		e := ev("other-"+strconv.Itoa(1900+i), strconv.Itoa(1900+i), bio.EventOther, "Деталь", "")
		e.Tier = bio.TierMinor
		a.Events = append(a.Events, e)
	}
	flags = capEvents(a)
	if len(a.Events) != bio.MaxEvents || len(flags) != 3 {
		t.Errorf("вех %d, выброшено %d; ожидалось %d и 3", len(a.Events), len(flags), bio.MaxEvents)
	}
}

func TestSlug(t *testing.T) {
	for in, want := range map[string]string{"time_machine": "time-machine", "Discworld_birth": "discworld-birth",
		"the--idiot": "the-idiot", "ok-ref": "ok-ref", "Война и мир": ""} {
		if got := slug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFixWritten(t *testing.T) {
	a := author()
	a.Works = []bio.Work{
		{Ref: "posthumous", Title: "П", Written: &bio.Written{From: 1884, To: 1884}}, // после смерти 1881 — снять
		{Ref: "late", Title: "Л", Written: &bio.Written{From: 1879, To: 1883}},       // конец после смерти — обрезать
		{Ref: "ok", Title: "О", Written: &bio.Written{From: 1866, To: 1866}},
	}
	flags := fixWritten(a)
	if a.Works[0].Written != nil {
		t.Errorf("посмертный период должен быть снят: %+v", a.Works[0].Written)
	}
	if a.Works[1].Written == nil || a.Works[1].Written.To != 1881 {
		t.Errorf("конец должен быть обрезан годом смерти: %+v", a.Works[1].Written)
	}
	if a.Works[2].Written == nil || *a.Works[2].Written != (bio.Written{From: 1866, To: 1866}) {
		t.Errorf("корректный период тронут: %+v", a.Works[2].Written)
	}
	if len(flags) != 2 || len(bio.Validate(a).Errors()) != 0 {
		t.Errorf("ожидалось 2 флага и валидный файл: %v / %v", flags, bio.Validate(a).Errors())
	}
}
