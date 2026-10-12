package timeline

import (
	"testing"

	"github.com/skriptes/skriptes/backend/internal/bio"
)

func dostoevsky(t *testing.T) *bio.Author {
	t.Helper()
	ds, err := bio.Load()
	if err != nil {
		t.Fatal(err)
	}
	return ds.Authors["Q991"]
}

func keys(items []ContextItem) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Event.Key)
	}
	return out
}

func has(items []ContextItem, key string) *ContextItem {
	for i := range items {
		if items[i].Event.Key == key {
			return &items[i]
		}
	}
	return nil
}

// «Идиот» 1867–1869: линкованные Европа и смерть Сони первыми по приоритету,
// затем major в окне; показ хронологический; кап 4.
func TestWindowIdiot(t *testing.T) {
	a := dostoevsky(t)
	items := Window(a, "idiot", bio.Written{From: 1867, To: 1869}, nil)
	if len(items) != 4 {
		t.Fatalf("items=%v", keys(items))
	}
	eu := has(items, "relocation-1867")
	if eu == nil || !eu.Linked || eu.Relation.Kind != "during" || eu.Note == "" {
		t.Fatalf("Европа: %+v", eu)
	}
	if s := has(items, "loss-1868"); s == nil || !s.Linked || s.Note == "" {
		t.Fatalf("смерть Сони: %+v", s)
	}
	for i := 1; i < len(items); i++ {
		if items[i-1].Event.Date.From > items[i].Event.Date.From {
			t.Fatalf("не хронологично: %v", keys(items))
		}
	}
}

// «Игрок» 1866: три линкованные вехи, одна из них — за 3 года до (минорная,
// но линк важнее тира).
func TestWindowIgrok(t *testing.T) {
	a := dostoevsky(t)
	items := Window(a, "igrok", bio.Written{From: 1866, To: 1866}, nil)
	if l := has(items, "love-1863"); l == nil || l.Relation.Kind != "before" || l.Relation.Years != 3 {
		t.Fatalf("Суслова: %+v (%v)", l, keys(items))
	}
	if c := has(items, "creation-1866"); c == nil || c.Relation.Kind != "same_year" {
		t.Fatalf("диктовка: %+v", c)
	}
}

// «Преступление и наказание» 1865–1866: линков нет — major в окне + major за
// ≤ 2 года до (две смерти 1864).
func TestWindowCrime(t *testing.T) {
	a := dostoevsky(t)
	items := Window(a, "prestuplenie", bio.Written{From: 1865, To: 1866}, nil)
	for _, k := range []string{"loss-1864", "loss-1864-2", "poverty-1865", "creation-1866"} {
		if has(items, k) == nil {
			t.Fatalf("нет %s: %v", k, keys(items))
		}
	}
}

// «Подросток» 1874–1875: в окне только minor → фолбэк на ближайшую major за
// ≤ 10 лет (возвращение из Европы 1871).
func TestWindowFallback(t *testing.T) {
	a := dostoevsky(t)
	items := Window(a, "podrostok", bio.Written{From: 1874, To: 1875}, nil)
	if len(items) < 2 {
		t.Fatalf("items=%v", keys(items))
	}
	found := false
	for _, it := range items {
		if it.Relation.Kind == "before" && it.Event.Tier == bio.TierMajor {
			found = true
		}
	}
	if !found {
		t.Fatalf("нет фолбэка на major до: %v", keys(items))
	}
}

// Скрытая веха не попадает в окно; якоря рождения/смерти — только по линку.
func TestWindowHiddenAndAnchors(t *testing.T) {
	a := dostoevsky(t)
	items := Window(a, "idiot", bio.Written{From: 1867, To: 1869}, map[string]bool{"loss-1868": true})
	if has(items, "loss-1868") != nil {
		t.Fatal("скрытая веха показана")
	}
	// Работа без линков прямо после рождения: рождение — не контекст.
	items = Window(a, "", bio.Written{From: 1823, To: 1823}, nil)
	if has(items, "birth-1821") != nil {
		t.Fatal("рождение попало в окно как «за 2 года до»")
	}
}

func TestNormTitleAndMatch(t *testing.T) {
	if normTitle("Бойня номер пять, или Крестовый поход детей") != "бойня номер пять или крестовый поход детей" {
		t.Fatal(normTitle("Бойня номер пять, или Крестовый поход детей"))
	}
	works := []catalogWork{
		{id: 1, title: "Идиот", norm: normTitle("Идиот")},
		{id: 2, title: "Игрок", norm: normTitle("Игрок"), qid: "Q1"},
		{id: 3, title: "Бойня номер пять", norm: normTitle("Бойня номер пять")},
	}
	if m := matchWork(bio.Work{Ref: "x", Title: "Другое", QID: "Q1"}, works); m == nil || m.id != 2 {
		t.Fatal("QID должен матчиться первым")
	}
	if m := matchWork(bio.Work{Ref: "x", Title: "ИДИОТ."}, works); m == nil || m.id != 1 {
		t.Fatal("нормализованное название")
	}
	if m := matchWork(bio.Work{Ref: "x", Title: "Бойня номер пять, или Крестовый поход детей", Aliases: []string{"Бойня номер пять"}}, works); m == nil || m.id != 3 {
		t.Fatal("алиас")
	}
	if matchWork(bio.Work{Ref: "x", Title: "Идиоты"}, works) != nil {
		t.Fatal("частичное совпадение не считается")
	}
}
