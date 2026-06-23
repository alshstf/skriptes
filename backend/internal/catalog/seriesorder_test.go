package catalog

import (
	"testing"
	"time"
)

func TestParseTitleOrdinal(t *testing.T) {
	cases := []struct {
		title string
		want  int
		ok    bool
	}{
		{"Кадетский корпус. Книга 2", 2, true},
		{"Книга первая", 1, true},
		{"Книга четвёртая", 4, true}, // ё→е
		{"Том II", 2, true},
		{"Часть третья", 3, true},
		{"том 10", 10, true},
		{"часть 1", 1, true},
		{"Кн. 3", 3, true},
		{"Ч. 5", 5, true},
		// Негативы: без ключевого слова или без значения.
		{"Дюна", 0, false},
		{"Дюна 2", 0, false}, // цифра без ключевого слова — не ловим
		{"Стража! Стража!", 0, false},
		{"Книга", 0, false},
		{"Гарри Поттер и узник Азкабана", 0, false},
	}
	for _, c := range cases {
		got, ok := parseTitleOrdinal(c.title)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("parseTitleOrdinal(%q) = (%d,%v), want (%d,%v)", c.title, got, ok, c.want, c.ok)
		}
	}
}

func iptr(n int) *int { return &n }

// order возвращает срез bookID в порядке возрастания ранга.
func order(ranks map[int64]int) []int64 {
	out := make([]int64, len(ranks))
	for id, r := range ranks {
		out[r] = id
	}
	return out
}

func eqIDs(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAssignSeriesOrder(t *testing.T) {
	day := func(d int) time.Time { return time.Date(2020, 1, d, 0, 0, 0, 0, time.UTC) }

	t.Run("ser_no у всех", func(t *testing.T) {
		items := []seriesSortItem{
			{bookID: 10, serNo: iptr(3), normTitle: "в"},
			{bookID: 11, serNo: iptr(1), normTitle: "а"},
			{bookID: 12, serNo: iptr(2), normTitle: "б"},
		}
		if got := order(assignSeriesOrder(items)); !eqIDs(got, []int64{11, 12, 10}) {
			t.Fatalf("ser_no: got %v", got)
		}
	})

	t.Run("written_year когда ser_no нет", func(t *testing.T) {
		items := []seriesSortItem{
			{bookID: 10, writtenYear: iptr(2010), normTitle: "в"},
			{bookID: 11, writtenYear: iptr(2005), normTitle: "а"},
			{bookID: 12, writtenYear: iptr(2008), normTitle: "б"},
		}
		if got := order(assignSeriesOrder(items)); !eqIDs(got, []int64{11, 12, 10}) {
			t.Fatalf("written_year: got %v", got)
		}
	})

	t.Run("edition_year когда нет ser_no/written", func(t *testing.T) {
		items := []seriesSortItem{
			{bookID: 10, editionYear: iptr(1999), normTitle: "в"},
			{bookID: 11, editionYear: iptr(1990), normTitle: "а"},
		}
		if got := order(assignSeriesOrder(items)); !eqIDs(got, []int64{11, 10}) {
			t.Fatalf("edition_year: got %v", got)
		}
	})

	t.Run("эвристика названия когда нет годов", func(t *testing.T) {
		items := []seriesSortItem{
			{bookID: 10, title: "Книга третья", normTitle: "книга третья"},
			{bookID: 11, title: "Книга первая", normTitle: "книга первая"},
			{bookID: 12, title: "Книга вторая", normTitle: "книга вторая"},
		}
		if got := order(assignSeriesOrder(items)); !eqIDs(got, []int64{11, 12, 10}) {
			t.Fatalf("heuristic: got %v", got)
		}
	})

	t.Run("date_added как последний резерв", func(t *testing.T) {
		items := []seriesSortItem{
			{bookID: 10, title: "Зет", normTitle: "зет", dateAdded: day(3)},
			{bookID: 11, title: "Альфа", normTitle: "альфа", dateAdded: day(1)},
			{bookID: 12, title: "Бета", normTitle: "бета", dateAdded: day(2)},
		}
		if got := order(assignSeriesOrder(items)); !eqIDs(got, []int64{11, 12, 10}) {
			t.Fatalf("date_added: got %v", got)
		}
	})

	t.Run("тайбрейк по normalized_title", func(t *testing.T) {
		d := day(1)
		items := []seriesSortItem{
			{bookID: 10, normTitle: "бета", dateAdded: d},
			{bookID: 11, normTitle: "альфа", dateAdded: d},
		}
		if got := order(assignSeriesOrder(items)); !eqIDs(got, []int64{11, 10}) {
			t.Fatalf("tiebreak: got %v", got)
		}
	})

	t.Run("частичный ser_no: книги без номера вставляются по году", func(t *testing.T) {
		// У одной книги ser_no=1 (год 2010), у других номера нет, но есть год
		// (2005, 2008) — оба РАНЬШЕ единственного датированного якоря (2010), поэтому
		// встают перед ним, по году. Раньше all-or-nothing отбрасывал ser_no и брал
		// written_year; теперь — ser_no-каркас + вставка, итог тот же [11,12,10].
		items := []seriesSortItem{
			{bookID: 10, serNo: iptr(1), writtenYear: iptr(2010), normTitle: "в"},
			{bookID: 11, writtenYear: iptr(2005), normTitle: "а"},
			{bookID: 12, writtenYear: iptr(2008), normTitle: "б"},
		}
		if got := order(assignSeriesOrder(items)); !eqIDs(got, []int64{11, 12, 10}) {
			t.Fatalf("partial ser_no: got %v", got)
		}
	})

	t.Run("частичная эвристика → падаем на date_added", func(t *testing.T) {
		// Не у всех названия парсятся → уровень эвристики пропускается.
		items := []seriesSortItem{
			{bookID: 10, title: "Книга первая", normTitle: "книга первая", dateAdded: day(3)},
			{bookID: 11, title: "Просто роман", normTitle: "просто роман", dateAdded: day(1)},
		}
		if got := order(assignSeriesOrder(items)); !eqIDs(got, []int64{11, 10}) {
			t.Fatalf("partial heuristic: got %v", got)
		}
	})

	// ── ser_no-каркас + вставка по году (Option A+) ──────────────────────────

	t.Run("каркас: orphan заполняет пропуск по году", func(t *testing.T) {
		// Кейс пользователя: #1@1999, #3@2001, без-номера@2000 → orphan садится в
		// пропуск №2 (ключ 1.5), а не уходит в конец. Порядок [1, orphan, 3].
		items := []seriesSortItem{
			{bookID: 1, serNo: iptr(1), writtenYear: iptr(1999), normTitle: "a"},
			{bookID: 3, serNo: iptr(3), writtenYear: iptr(2001), normTitle: "c"},
			{bookID: 2, writtenYear: iptr(2000), normTitle: "b"},
		}
		if got := order(assignSeriesOrder(items)); !eqIDs(got, []int64{1, 2, 3}) {
			t.Fatalf("gap-fill: got %v", got)
		}
	})

	t.Run("каркас: поздний омнибус без номера — за нумерованными", func(t *testing.T) {
		// Как «МИФические истории»: 1..3 (часть с годами), омнибус без номера с
		// годом ПОЗЖЕ всех нумерованных → сразу за последним номером, не в перемешку.
		items := []seriesSortItem{
			{bookID: 1, serNo: iptr(1), writtenYear: iptr(1978), normTitle: "a"},
			{bookID: 2, serNo: iptr(2), normTitle: "b"}, // номер без года
			{bookID: 3, serNo: iptr(3), writtenYear: iptr(1982), normTitle: "c"},
			{bookID: 99, writtenYear: iptr(2015), normTitle: "omnibus"},
		}
		if got := order(assignSeriesOrder(items)); !eqIDs(got, []int64{1, 2, 3, 99}) {
			t.Fatalf("late omnibus: got %v", got)
		}
	})

	t.Run("каркас: orphan без года — в самый хвост после датированных", func(t *testing.T) {
		items := []seriesSortItem{
			{bookID: 1, serNo: iptr(1), writtenYear: iptr(2000), normTitle: "a"},
			{bookID: 50, writtenYear: iptr(2005), normTitle: "dated-orphan"},
			{bookID: 60, normTitle: "no-year", dateAdded: day(1)},
		}
		if got := order(assignSeriesOrder(items)); !eqIDs(got, []int64{1, 50, 60}) {
			t.Fatalf("no-year orphan: got %v", got)
		}
	})

	t.Run("каркас: orphan раньше всех датированных номеров — перед ними", func(t *testing.T) {
		items := []seriesSortItem{
			{bookID: 3, serNo: iptr(3), writtenYear: iptr(2001), normTitle: "c"},
			{bookID: 5, serNo: iptr(5), writtenYear: iptr(2005), normTitle: "e"},
			{bookID: 1, writtenYear: iptr(1999), normTitle: "early"},
		}
		if got := order(assignSeriesOrder(items)); !eqIDs(got, []int64{1, 3, 5}) {
			t.Fatalf("early orphan: got %v", got)
		}
	})

	t.Run("каркас: два orphan в одном пропуске — по году", func(t *testing.T) {
		// #1@2000, #4@2010, два без-номера (2003, 2006) → оба в пропуск, по году.
		items := []seriesSortItem{
			{bookID: 1, serNo: iptr(1), writtenYear: iptr(2000), normTitle: "a"},
			{bookID: 4, serNo: iptr(4), writtenYear: iptr(2010), normTitle: "d"},
			{bookID: 30, writtenYear: iptr(2006), normTitle: "y"},
			{bookID: 20, writtenYear: iptr(2003), normTitle: "x"},
		}
		// 1(2000) → key1; 20(2003)→ после #1 → 1.5; 30(2006)→ после #1 → 1.5; 4→key4.
		// 20 и 30 равны по ключу → по году: 20(2003) < 30(2006).
		if got := order(assignSeriesOrder(items)); !eqIDs(got, []int64{1, 20, 30, 4}) {
			t.Fatalf("two orphans in gap: got %v", got)
		}
	})
}
