package books

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// Негативы персоны (план recsys/03, задача 3.5): мягкий малус за блок-лист
// автора/цикла, «не интересно» из ленты и «бросил». Golden-тесты
// пересортировки — приёмка любой правки поиска (грабля 13).

// Сценарий прода: «три мушкетёра» у пользователя с Дюма в блок-листе. Точное
// совпадение известной книги (Meili 1.0, известность 1189) по баллу ушло бы в
// хвост страницы — предел maxMalusDrop держит его в верхней пятёрке.
func TestSortByFinalScore_MalusKeepsExactTitleVisible(t *testing.T) {
	p := emptyPersona()
	p.BlockedAuthors[7] = struct{}{}
	scored := []scoredItem{
		{item: ListItem{ID: 1, AuthorIDs: []int64{7}}, base: 1.0, pop: popularityBoost(1189)},
	}
	for i := 2; i <= 20; i++ {
		scored = append(scored, scoredItem{item: ListItem{ID: int64(i), AuthorIDs: []int64{100 + int64(i)}}, base: 0.95, pop: popularityBoost(300)})
	}
	applyPersonaBoost(scored, p, true, "")
	require.InDelta(t, malusBlocked, scored[0].malus, 1e-9)
	require.Less(t, finalScore(scored[0]), finalScore(scored[1]), "по баллу книга заблокированного автора ниже остальных")

	sortByFinalScore(scored)
	require.EqualValues(t, maxMalusDrop, indexOfID(scored, 1), "опустилась ровно на предел, не в хвост")
	// Остальные — в порядке Meili, сдвинуты на одну позицию вверх.
	for i := 0; i < maxMalusDrop; i++ {
		require.EqualValues(t, i+2, scored[i].item.ID)
	}
	for i := maxMalusDrop + 1; i < len(scored); i++ {
		require.EqualValues(t, i+1, scored[i].item.ID)
	}
}

// Малус — в полосе близких совпадений, как буст известности: книга
// заблокированного автора проигрывает другой книге с тем же названием, но не
// явно худшему совпадению — точный запрос по-прежнему находит её первой.
func TestSortByFinalScore_MalusOnlyAmongCloseMatches(t *testing.T) {
	p := emptyPersona()
	p.BlockedAuthors[7] = struct{}{}
	// «Всадник без головы» Майн Рида (заблокирован) против частичных совпадений.
	partial := []scoredItem{
		{item: ListItem{ID: 1, AuthorIDs: []int64{7}}, base: 1.0, pop: popularityBoost(1189)},
		{item: ListItem{ID: 2, AuthorIDs: []int64{8}}, base: 0.85, pop: popularityBoost(300)},
		{item: ListItem{ID: 3, AuthorIDs: []int64{9}}, base: 0.85, pop: popularityBoost(300)},
	}
	applyPersonaBoost(partial, p, true, "")
	sortByFinalScore(partial)
	require.Equal(t, []int64{1, 2, 3}, ids(partial), "явно худшие совпадения не обгоняют")

	// Та же книга против одноимённой книги другого автора (точность равная,
	// известность ниже): «не предлагать» — одноимённая выше.
	sameTitle := []scoredItem{
		{item: ListItem{ID: 1, AuthorIDs: []int64{7}}, base: 1.0, pop: popularityBoost(1189)},
		{item: ListItem{ID: 2, AuthorIDs: []int64{8}}, base: 1.0, pop: popularityBoost(100)},
	}
	applyPersonaBoost(sameTitle, p, true, "")
	sortByFinalScore(sameTitle)
	require.Equal(t, []int64{2, 1}, ids(sameTitle), "среди равных — чужая выше")

	// Шкала: любой малус меньше разрыва «близкий / явно худший» (0.2 base), но
	// блок перебивает разницу известности классики и безвестной книги (≈0.1).
	require.Less(t, malusBlocked, 0.2)
	require.Greater(t, malusBlocked, popularityBoost(1000))
	require.Less(t, malusDropped, popularityBoost(1000), "«бросил» слабее известности: известная брошенная книга при равном названии остаётся выше безвестной")
}

// Малус — один, самый сильный, а не сумма: книга заблокированного автора, которую
// ещё и скрыли из ленты и бросили, получает malusBlocked, а не 1.2.
func TestApplyPersonaBoost_MalusStrongestOnce(t *testing.T) {
	p := emptyPersona()
	p.BlockedAuthors[7] = struct{}{}
	p.BlockedSeries[3] = struct{}{}
	p.NotInterestedWorks[42] = struct{}{}
	p.DroppedWorks[42] = struct{}{}
	sid := int64(3)
	scored := []scoredItem{{item: ListItem{ID: 42, AuthorIDs: []int64{7}, SeriesID: &sid}}}
	applyPersonaBoost(scored, p, true, "")
	require.InDelta(t, malusBlocked, scored[0].malus, 1e-9)

	// По отдельности — своя шкала: блок > не интересно > бросил.
	for _, tc := range []struct {
		name string
		set  func(it *ListItem)
		want float64
	}{
		{"блок цикла", func(it *ListItem) { it.SeriesID = &sid }, malusBlocked},
		{"не интересно", func(it *ListItem) { it.ID = 42; p.DroppedWorks = map[int64]struct{}{} }, malusNotInterested},
		{"бросил", func(it *ListItem) {
			it.ID = 42
			p.NotInterestedWorks = map[int64]struct{}{}
			p.DroppedWorks[42] = struct{}{}
		}, malusDropped},
	} {
		it := ListItem{ID: 1, AuthorIDs: []int64{1}}
		tc.set(&it)
		one := []scoredItem{{item: it}}
		applyPersonaBoost(one, p, true, "")
		require.InDelta(t, tc.want, one[0].malus, 1e-9, tc.name)
	}
	require.Greater(t, malusBlocked, malusNotInterested)
	require.Greater(t, malusNotInterested, malusDropped)
}

// Без негативов порядок — прежний: малус нулевой, finalScore = base+personal+pop,
// при равных баллах — порядок Meili (стабильность).
func TestSortByFinalScore_NoMalusUnchanged(t *testing.T) {
	p := emptyPersona()
	p.FavoriteAuthors[5] = struct{}{}
	scored := []scoredItem{
		{item: ListItem{ID: 1, AuthorIDs: []int64{1}}, base: 0.9},
		{item: ListItem{ID: 2, AuthorIDs: []int64{5}}, base: 0.8},
		{item: ListItem{ID: 3, AuthorIDs: []int64{1}}, base: 0.9},
	}
	applyPersonaBoost(scored, p, true, "")
	for _, sc := range scored {
		require.Zero(t, sc.malus)
	}
	sortByFinalScore(scored)
	require.Equal(t, []int64{2, 1, 3}, ids(scored))
}

// Предел считается от места БЕЗ малуса, а не от места Meili: если книги любимого
// автора и так обогнали бы хит, малус добавляет к этому не больше maxMalusDrop.
// Так позитивная часть выдачи остаётся прежней, а малус ограничен сам по себе.
func TestSortByFinalScore_MalusCapRelativeToBonusOrder(t *testing.T) {
	p := emptyPersona()
	p.FavoriteAuthors[5] = struct{}{}
	p.BlockedAuthors[7] = struct{}{}
	scored := []scoredItem{
		{item: ListItem{ID: 1, AuthorIDs: []int64{7}}, base: 1.0}, // Meili #1, заблокирован
		{item: ListItem{ID: 2, AuthorIDs: []int64{5}}, base: 0.9}, // любимый автор: +0.5
		{item: ListItem{ID: 3, AuthorIDs: []int64{5}}, base: 0.9},
		{item: ListItem{ID: 4, AuthorIDs: []int64{9}}, base: 0.9},
		{item: ListItem{ID: 5, AuthorIDs: []int64{9}}, base: 0.9},
		{item: ListItem{ID: 6, AuthorIDs: []int64{9}}, base: 0.9},
		{item: ListItem{ID: 7, AuthorIDs: []int64{9}}, base: 0.9},
		{item: ListItem{ID: 8, AuthorIDs: []int64{9}}, base: 0.9},
	}
	applyPersonaBoost(scored, p, true, "")
	sortByFinalScore(scored)
	// Без малуса: 2, 3, 1, 4… (хит — третий); с малусом — не ниже 3+maxMalusDrop.
	require.Equal(t, []int64{2, 3, 4, 5, 6, 1, 7, 8}, ids(scored))
	require.EqualValues(t, 2+maxMalusDrop, indexOfID(scored, 1))
}

// Несколько хитов с малусом: у каждого свой срок, порядок между ними — по баллу
// и Meili; никто не глубже своего предела.
func TestSortByFinalScore_ManyMalusHitsEachWithinCap(t *testing.T) {
	p := emptyPersona()
	p.BlockedAuthors[7] = struct{}{}
	var scored []scoredItem
	for i := 1; i <= 12; i++ {
		aid := int64(100 + i)
		if i%3 == 1 { // 1, 4, 7, 10 — заблокированный автор
			aid = 7
		}
		scored = append(scored, scoredItem{item: ListItem{ID: int64(i), AuthorIDs: []int64{aid}}, base: 1.0 - float64(i)*0.001})
	}
	applyPersonaBoost(scored, p, true, "")
	sortByFinalScore(scored)
	for _, id := range []int64{1, 4, 7, 10} {
		require.LessOrEqual(t, indexOfID(scored, id), int(id-1)+maxMalusDrop, "хит %d", id)
		require.GreaterOrEqual(t, indexOfID(scored, id), int(id-1), "малус не поднимает")
	}
	require.Equal(t, []int64{2, 3, 5, 1, 6, 8, 4, 9, 11, 7, 12, 10}, ids(scored))
}

// Короткая выдача: малус в палитре на 5 строк не может увести хит за пределы
// списка (срок ≥ длины — просто последний).
func TestSortByFinalScore_MalusShortList(t *testing.T) {
	p := emptyPersona()
	p.NotInterestedWorks[1] = struct{}{}
	scored := []scoredItem{
		{item: ListItem{ID: 1}, base: 1.0},
		{item: ListItem{ID: 2}, base: 0.9},
	}
	applyPersonaBoost(scored, p, true, "")
	sortByFinalScore(scored)
	require.Equal(t, []int64{2, 1}, ids(scored))

	single := []scoredItem{{item: ListItem{ID: 1}, base: 1.0}}
	applyPersonaBoost(single, p, true, "")
	sortByFinalScore(single)
	require.Equal(t, []int64{1}, ids(single))
}

// Негативы по работе: в индексе works — по id хита, в индексе изданий (OPDS) —
// по work_id документа; id работ и изданий пересекаются (#399).
func TestApplyPersonaBoost_NegativesByWorkID(t *testing.T) {
	p := emptyPersona()
	p.NotInterestedWorks[9] = struct{}{}
	p.DroppedWorks[9] = struct{}{}

	works := []scoredItem{{item: ListItem{ID: 5, WorkID: 0}}, {item: ListItem{ID: 9}}}
	applyPersonaBoost(works, p, true, "")
	require.Zero(t, works[0].malus)
	require.InDelta(t, malusNotInterested, works[1].malus, 1e-9)

	editions := []scoredItem{{item: ListItem{ID: 9, WorkID: 5}}, {item: ListItem{ID: 5, WorkID: 9}}}
	applyPersonaBoost(editions, p, false, "")
	require.Zero(t, editions[0].malus, "издание 9 — чужой работы 5")
	require.InDelta(t, malusNotInterested, editions[1].malus, 1e-9, "издание 5 работы 9")

	// Издание без work_id (ещё не сгруппировано) — без малуса по работе.
	orphan := []scoredItem{{item: ListItem{ID: 9}}}
	applyPersonaBoost(orphan, p, false, "")
	require.Zero(t, orphan[0].malus)
}

// Антология: заблокированный автор среди сотен — не сигнал (как и подписка, #399).
func TestApplyPersonaBoost_BlockedAnthologyAuthorIgnored(t *testing.T) {
	p := emptyPersona()
	p.BlockedAuthors[7] = struct{}{}
	authors := make([]int64, 334)
	for i := range authors {
		authors[i] = int64(1000 + i)
	}
	authors[42] = 7
	scored := []scoredItem{{item: ListItem{ID: 1, AuthorIDs: authors}}, {item: ListItem{ID: 2, AuthorIDs: []int64{1, 2, 7}}}}
	applyPersonaBoost(scored, p, true, "")
	require.Zero(t, scored[0].malus)
	require.InDelta(t, malusBlocked, scored[1].malus, 1e-9)
}

func ids(scored []scoredItem) []int64 {
	out := make([]int64, 0, len(scored))
	for _, sc := range scored {
		out = append(out, sc.item.ID)
	}
	return out
}

func indexOfID(scored []scoredItem, id int64) int {
	for i, sc := range scored {
		if sc.item.ID == id {
			return i
		}
	}
	panic(fmt.Sprintf("id %d not in list", id))
}

// Точное название — без малуса (прод 2026-10-22: «На дне» 1.0, «На дне могилы»
// 0.99994, одноимённая безвестная 1.0 — разницу делает только известность, любой
// малус ронял точное название на предел). Неточный запрос — малус с пределом.
func TestApplyPersonaBoost_ExactTitleNoMalus(t *testing.T) {
	p := emptyPersona()
	p.BlockedAuthors[7] = struct{}{} // Горький
	hits := func() []scoredItem {
		return []scoredItem{
			{item: ListItem{ID: 1, Title: "На дне", AuthorIDs: []int64{7}}, base: 1.0, pop: popularityBoost(606)},
			{item: ListItem{ID: 2, Title: "На дне", AuthorIDs: []int64{8}}, base: 1.0},
			{item: ListItem{ID: 3, Title: "На дне", AuthorIDs: []int64{9}}, base: 1.0},
			{item: ListItem{ID: 4, Title: "На дне могилы", AuthorIDs: []int64{10}}, base: 0.999944, pop: popularityBoost(374)},
			{item: ListItem{ID: 5, Title: "На дне Одессы", AuthorIDs: []int64{11}}, base: 0.999944, pop: popularityBoost(130)},
			{item: ListItem{ID: 6, Title: "На дне. Избранное", AuthorIDs: []int64{7}}, base: 0.999944, pop: popularityBoost(112)},
		}
	}
	exact := hits()
	applyPersonaBoost(exact, p, true, "на дне")
	require.Zero(t, exact[0].malus, "точное название — без малуса")
	require.InDelta(t, malusBlocked, exact[5].malus, 1e-9, "сборник того же автора с другим названием — с малусом")
	sortByFinalScore(exact)
	require.EqualValues(t, 1, exact[0].item.ID, "Горький остаётся первым")

	// Нормализация: регистр, ё, пунктуация по краям слов.
	yo := []scoredItem{{item: ListItem{ID: 1, Title: "Три мушкетёра", AuthorIDs: []int64{7}}, base: 1.0}}
	applyPersonaBoost(yo, p, true, "  ТРИ МУШКЕТЕРА! ")
	require.Zero(t, yo[0].malus)

	// Неточный запрос — малус есть, предел держит.
	partial := hits()
	applyPersonaBoost(partial, p, true, "дне")
	require.InDelta(t, malusBlocked, partial[0].malus, 1e-9)
	sortByFinalScore(partial)
	require.EqualValues(t, maxMalusDrop, indexOfID(partial, 1))
}
