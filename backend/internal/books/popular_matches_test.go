package books

import (
	"encoding/json"
	"testing"

	"github.com/meilisearch/meilisearch-go"
	"github.com/stretchr/testify/require"
)

func wh(id int64, title string, pop int64) workHit {
	return workHit{ID: id, Title: title, Popularity: pop}
}

func titles(hs []workHit) []string {
	out := make([]string, 0, len(hs))
	for _, h := range hs {
		out = append(out, h.Title)
	}
	return out
}

// Цифры — запросы прода 2026-10-06 (#401).
func TestPickPinned(t *testing.T) {
	rel := []workHit{wh(1, "Мастер", 136), wh(2, "Мастер", 136), wh(3, "Мастер", 112), wh(4, "Мастер", 112), wh(5, "Мастер", 0)}
	pop := []workHit{wh(10, "Мастер и Маргарита", 1725), wh(11, "Мастер Страшного суда", 567),
		wh(12, "Мастер собак", 478), wh(13, "Мастер снов", 443)}
	require.Equal(t, []string{"Мастер и Маргарита"}, titles(pickPinned(pop, rel, "мастер")),
		"«Мастер Страшного суда» (567) меньше трети «Мастера и Маргариты» — не закрепляем")

	rel = []workHit{wh(1, "Остров", 408), wh(2, "Остров", 300)}
	pop = []workHit{wh(10, "Остров Сокровищ", 1510), wh(11, "Остров погибших кораблей", 1046),
		wh(12, "Остров доктора Моро", 1041), wh(13, "Остров проклятых", 716)}
	require.Equal(t, []string{"Остров Сокровищ", "Остров погибших кораблей", "Остров доктора Моро"},
		titles(pickPinned(pop, rel, "остров")), "не больше трёх")

	rel = []workHit{wh(1, "Идиот", 1044), wh(2, "Идиотский бесценный мозг", 172)}
	pop = []workHit{wh(1, "Идиот", 1044), wh(2, "Идиотский бесценный мозг", 172)}
	require.Empty(t, pickPinned(pop, rel, "идиот"), "известное и так наверху")

	rel = []workHit{wh(1, "Море", 0)}
	pop = []workHit{wh(10, "Морелла", 724), wh(11, "Море и рыбки", 577)}
	require.Equal(t, []string{"Море и рыбки"}, titles(pickPinned(pop, rel, "море")),
		"«Морелла» — совпадение по началу слова, не закрепляем")

	rel = []workHit{wh(1, "Дар", 698)}
	pop = []workHit{wh(10, "Дары волхвов", 804), wh(1, "Дар", 698)}
	require.Empty(t, pickPinned(pop, rel, "дар"), "«Дары» — другое слово; «Дар» и так первый")

	rel = []workHit{wh(1, "Война", 470)}
	pop = []workHit{wh(10, "Война миров", 1404), wh(11, "Война и мир", 1321), wh(12, "Война с саламандрами", 730)}
	require.Equal(t, []string{"Война миров", "Война и мир"}, titles(pickPinned(pop, rel, "война")),
		"«Война с саламандрами» (730) меньше двух лучших по релевантности (2×470)")

	rel = []workHit{wh(1, "Мир", 160)}
	pop = []workHit{wh(10, "Мир-Кольцо", 1188)}
	require.Len(t, pickPinned(pop, rel, "мир"), 1, "дефис — граница слова")

	rel = []workHit{wh(1, "Тень", 553)}
	pop = []workHit{wh(10, "Тень Эндера", 1043)}
	require.Empty(t, pickPinned(pop, rel, "тень"), "меньше двух лучших по релевантности")

	pop = []workHit{wh(10, "Малоизвестный остров", 350)}
	require.Empty(t, pickPinned(pop, []workHit{wh(1, "Остров", 0)}, "остров"), "известность меньше 400")
}

func TestMergeHits(t *testing.T) {
	hit := func(id int64, title string) meilisearch.Hit {
		b, _ := json.Marshal(id)
		tb, _ := json.Marshal(title)
		return meilisearch.Hit{"id": b, "title": tb}
	}
	main := meilisearch.Hits{hit(1, "Мастер"), hit(2, "Мастер")}
	extra := meilisearch.Hits{hit(10, "Мастер и Маргарита"), hit(2, "Мастер")}
	got := decodeWorkHits(mergeHits(main, extra))
	require.Equal(t, []int64{1, 2, 10}, []int64{got[0].ID, got[1].ID, got[2].ID}, "без повторов, основной порядок первым")
}

func TestWholeWordHits(t *testing.T) {
	hit := func(id int64, title string) meilisearch.Hit {
		b, _ := json.Marshal(id)
		tb, _ := json.Marshal(title)
		return meilisearch.Hit{"id": b, "title": tb}
	}
	got := decodeWorkHits(wholeWordHits(meilisearch.Hits{hit(1, "Дары волхвов"), hit(2, "Дар"), hit(3, "Морелла")}, "дар"))
	require.Len(t, got, 1)
	require.Equal(t, "Дар", got[0].Title, "по началу слова — не известное совпадение")
}
