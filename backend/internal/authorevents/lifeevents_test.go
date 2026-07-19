package authorevents

import "testing"

func ev(year, weight int, title string) Event {
	return Event{ID: int64(year), Type: "loss", YearFrom: year, Weight: weight, Title: title}
}

func period(from, to, weight int, title string) Event {
	e := ev(from, weight, title)
	e.YearTo = &to
	e.Type = "persecution" // период, длительность которого сама по себе значима
	return e
}

func typedPeriod(from, to, weight int, title, typ string) Event {
	e := period(from, to, weight, title)
	e.Type = typ
	return e
}

func relations(items []LifeEvent) map[string]string {
	out := map[string]string{}
	for _, it := range items {
		out[it.Title] = it.Relation
	}
	return out
}

// Золотые кейсы плана: прямая, контрастная и ОТЛОЖЕННАЯ связь.
func TestSelectLifeEvents(t *testing.T) {
	t.Run("событие того же года — same_year (Достоевский: «Идиот» 1868 и смерть дочери Сони)", func(t *testing.T) {
		got := selectLifeEvents([]Event{
			ev(1868, 5, "Умерла дочь Соня"),
			ev(1867, 3, "Брак: Анна Сниткина"),
		}, 1868)
		rel := relations(got)
		if rel["Умерла дочь Соня"] != RelationSameYear {
			t.Fatalf("same_year: %+v", got)
		}
		// Событие годом раньше — тоже в окне, но как years_after.
		if rel["Брак: Анна Сниткина"] != RelationBefore {
			t.Fatalf("years_after: %+v", got)
		}
		if got[0].Relation != RelationSameYear {
			t.Fatalf("same_year должен идти первым: %+v", got)
		}
	})

	t.Run("период накрывает год написания — during, и он важнее прочих", func(t *testing.T) {
		got := selectLifeEvents([]Event{
			ev(1852, 3, "Женился"),
			period(1850, 1854, 5, "Каторга в Омске"),
		}, 1852)
		if got[0].Relation != RelationDuring || got[0].Title != "Каторга в Омске" {
			t.Fatalf("during первым: %+v", got)
		}
	})

	t.Run("отложенная связь: Дрезден-1945 → «Бойня №5»-1969", func(t *testing.T) {
		got := selectLifeEvents([]Event{
			ev(1945, 5, "Бомбардировка Дрездена"),
		}, 1969)
		if len(got) != 1 {
			t.Fatalf("формирующее событие должно добираться при пустом окне: %+v", got)
		}
		if got[0].Relation != RelationDelayed || got[0].YearsGap != 24 {
			t.Fatalf("delayed 24 года: %+v", got[0])
		}
	})

	t.Run("формирующее НЕ добирается, когда окно и так наполнено", func(t *testing.T) {
		got := selectLifeEvents([]Event{
			ev(1945, 5, "Война"),
			ev(1968, 5, "Умер отец"),
			ev(1969, 3, "Переехал"),
		}, 1969)
		for _, it := range got {
			if it.Relation == RelationDelayed {
				t.Fatalf("delayed лишний при полном окне: %+v", got)
			}
		}
	})

	t.Run("слишком давнее формирующее — совпадение, не связь", func(t *testing.T) {
		got := selectLifeEvents([]Event{ev(1900, 5, "Война")}, 1955) // 55 лет
		if len(got) != 0 {
			t.Fatalf("gap > maxDelayedGap не берём: %+v", got)
		}
	})

	t.Run("события ПОСЛЕ написания не влияли на книгу", func(t *testing.T) {
		got := selectLifeEvents([]Event{ev(1875, 5, "Утрата")}, 1869)
		if len(got) != 0 {
			t.Fatalf("будущее в блок не идёт: %+v", got)
		}
	})

	t.Run("тривиальные события (weight < 2) не показываем", func(t *testing.T) {
		got := selectLifeEvents([]Event{
			ev(1869, 0, "Родился"),
			ev(1869, 1, "Жил в Москве"),
		}, 1869)
		if len(got) != 0 {
			t.Fatalf("тривиальные отсеиваются: %+v", got)
		}
	})

	t.Run("скрытые курированием не показываем", func(t *testing.T) {
		hidden := ev(1869, 5, "Скрытое")
		yes := true
		hidden.Hidden = &yes
		if got := selectLifeEvents([]Event{hidden}, 1869); len(got) != 0 {
			t.Fatalf("hidden отсеивается везде: %+v", got)
		}
	})

	t.Run("брак-период не даёт during: он накрыл бы все книги после свадьбы", func(t *testing.T) {
		got := selectLifeEvents([]Event{
			typedPeriod(1862, 1910, 3, "Брак: Софья Андреевна", "love"),
		}, 1882)
		if len(got) != 0 {
			t.Fatalf("состояние (брак/жильё) периодом не показываем: %+v", got)
		}
	})

	t.Run("не больше maxItems", func(t *testing.T) {
		var evs []Event
		for i := 0; i < 10; i++ {
			evs = append(evs, ev(1869, 5, string(rune('A'+i))))
		}
		if got := selectLifeEvents(evs, 1869); len(got) != maxItems {
			t.Fatalf("кап %d: got %d", maxItems, len(got))
		}
	})

	t.Run("порядок: during → same_year → ближайшее years_after", func(t *testing.T) {
		got := selectLifeEvents([]Event{
			ev(1866, 3, "Три года назад"),
			ev(1868, 3, "Год назад"),
			ev(1869, 3, "В тот же год"),
			typedPeriod(1860, 1875, 4, "Эмиграция", "relocation"),
		}, 1869)
		want := []string{"Эмиграция", "В тот же год", "Год назад", "Три года назад"}
		for i, w := range want {
			if got[i].Title != w {
				t.Fatalf("позиция %d: %q, ждали %q (%+v)", i, got[i].Title, w, got)
			}
		}
	})
}
