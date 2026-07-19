package metadata

import (
	"strings"
	"testing"
)

type sparqlVal = struct {
	Value string `json:"value"`
}

func evRow(kv map[string]string) map[string]sparqlVal {
	out := map[string]sparqlVal{}
	for k, v := range kv {
		out[k] = sparqlVal{Value: v}
	}
	return out
}

// assembleWikidataEvents: типизация по prop, lifespan-фильтр смертей родных,
// кап наград, периоды из квалификаторов, дедуп, скрытие Q-id-«лейблов».
func TestAssembleWikidataEvents(t *testing.T) {
	rows := []map[string]sparqlVal{
		evRow(map[string]string{"prop": "P569", "date": "1828-09-09T00:00:00Z", "prec": "11", "placeLabel": "Ясная Поляна"}),
		evRow(map[string]string{"prop": "P570", "date": "1910-11-20T00:00:00Z", "prec": "11", "placeLabel": "Астапово"}),
		// Брак: дата из квалификатора (prec нет), сущность супруги.
		evRow(map[string]string{"prop": "P26", "date": "1862-09-23T00:00:00Z", "who": "http://www.wikidata.org/entity/Q463650", "whoLabel": "Софья Толстая"}),
		// Дубль того же брака (декартово произведение SPARQL) — дедуп.
		evRow(map[string]string{"prop": "P26", "date": "1862-09-23T00:00:00Z", "who": "http://www.wikidata.org/entity/Q463650", "whoLabel": "Софья Толстая"}),
		// Ребёнок.
		evRow(map[string]string{"prop": "P40", "date": "1863-06-28T00:00:00Z", "prec": "11", "who": "http://www.wikidata.org/entity/Q123456", "whoLabel": "Сергей Толстой"}),
		// Смерти родных: отец 1837 (внутри жизни — берём), сиблинг 1820 (до
		// рождения — мимо), родственник 1915 (после смерти автора — мимо).
		evRow(map[string]string{"prop": "P570rel", "date": "1837-06-21T00:00:00Z", "prec": "11", "who": "http://www.wikidata.org/entity/Q1", "whoLabel": "Николай Толстой"}),
		evRow(map[string]string{"prop": "P570rel", "date": "1820-01-01T00:00:00Z", "prec": "9", "who": "http://www.wikidata.org/entity/Q2", "whoLabel": "Ранний"}),
		evRow(map[string]string{"prop": "P570rel", "date": "1915-01-01T00:00:00Z", "prec": "9", "who": "http://www.wikidata.org/entity/Q3", "whoLabel": "Поздний"}),
		// Residence-период.
		evRow(map[string]string{"prop": "P551", "date": "1837-01-01T00:00:00Z", "end": "1841-01-01T00:00:00Z", "placeLabel": "Москва"}),
		// Война (P1344) и арест (P793) — по ключевым словам лейбла.
		evRow(map[string]string{"prop": "P1344", "date": "1854-01-01T00:00:00Z", "who": "http://www.wikidata.org/entity/Q4", "whoLabel": "Крымская война"}),
		evRow(map[string]string{"prop": "P793", "date": "1849-04-23T00:00:00Z", "who": "http://www.wikidata.org/entity/Q5", "whoLabel": "арест петрашевцев"}),
		// Сущность без человеческого лейбла (Wikidata отдаёт сам QID).
		evRow(map[string]string{"prop": "P26", "date": "1900-01-01T00:00:00Z", "who": "http://www.wikidata.org/entity/Q999", "whoLabel": "Q999"}),
	}
	// 5 наград — кап 3.
	for _, y := range []string{"1901", "1902", "1903", "1904", "1905"} {
		rows = append(rows, evRow(map[string]string{
			"prop": "P166", "date": y + "-01-01T00:00:00Z",
			"who": "http://www.wikidata.org/entity/Q7" + y, "whoLabel": "Премия " + y,
		}))
	}

	events := assembleWikidataEvents("Q7243", rows)

	byType := map[string]int{}
	byKey := map[string]AuthorEvent{}
	for _, ev := range events {
		byType[ev.Type]++
		byKey[ev.ExtKey] = ev
	}

	if byType[EventBirth] != 1 || byType[EventDeath] != 1 {
		t.Fatalf("рождение/смерть: %+v", byType)
	}
	if byType[EventLove] != 2 { // брак 1862 (дедуп дубля) + брак 1900
		t.Fatalf("браки: got %d, want 2 (дедуп дубля)", byType[EventLove])
	}
	if byType[EventLoss] != 1 {
		t.Fatalf("lifespan-фильтр смертей родных: got %d, want 1 (только 1837)", byType[EventLoss])
	}
	if byType[EventAward] != maxAwardEvents {
		t.Fatalf("кап наград: got %d, want %d", byType[EventAward], maxAwardEvents)
	}
	if byType[EventWar] != 1 {
		t.Fatalf("P1344 война: %+v", byType)
	}
	if byType[EventPersecution] != 1 {
		t.Fatalf("P793 арест → persecution: %+v", byType)
	}

	res, ok := byKey["P551:-:1837"]
	if !ok || res.YearTo == nil || *res.YearTo != 1841 {
		t.Fatalf("residence-период 1837–1841: %+v", res)
	}
	if res.Title != "Жил: Москва" {
		t.Fatalf("формулировка residence: %q", res.Title)
	}

	marriage := byKey["P26:Q463650:1862"]
	if marriage.Title != "Брак: Софья Толстая" {
		t.Fatalf("формулировка брака: %q", marriage.Title)
	}
	if marriage.Weight != 3 {
		t.Fatalf("вес брака: %d", marriage.Weight)
	}
	// Дата из квалификатора без precision, не 1 января → day-эвристика.
	if marriage.DatePrecision != "day" || marriage.DateFrom == nil {
		t.Fatalf("precision брака: %q %v", marriage.DatePrecision, marriage.DateFrom)
	}

	// «Лейбл» Q999 скрыт → generic формулировка.
	ghost := byKey["P26:Q999:1900"]
	if ghost.Title != "Брак" {
		t.Fatalf("Q-id-лейбл должен скрываться: %q", ghost.Title)
	}

	birth := byKey["P569:-:1828"]
	if !strings.Contains(birth.Title, "Ясная Поляна") || birth.Weight != 0 {
		t.Fatalf("рождение: %+v", birth)
	}
}
