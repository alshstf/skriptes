package metadata

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// clustersOf — группирует id книг по корню union-find (для проверок).
func clustersOf(books []groupBook, uf *unionFind) [][]int64 {
	byRoot := map[int][]int64{}
	for i, b := range books {
		r := uf.find(i)
		byRoot[r] = append(byRoot[r], b.id)
	}
	var out [][]int64
	for _, ids := range byRoot {
		sort.Slice(ids, func(a, b int) bool { return ids[a] < ids[b] })
		out = append(out, ids)
	}
	sort.Slice(out, func(a, b int) bool { return out[a][0] < out[b][0] })
	return out
}

func TestClusterTier1(t *testing.T) {
	t.Run("дубли одного языка по (название, язык)", func(t *testing.T) {
		books := []groupBook{
			{id: 1, workID: 1, normTitle: "война и мир", lang: "ru"},
			{id: 2, workID: 2, normTitle: "война и мир", lang: "ru"},
			{id: 3, workID: 3, normTitle: "анна каренина", lang: "ru"},
		}
		require.Equal(t, [][]int64{{1, 2}, {3}}, clustersOf(books, clusterTier1(books)))
	})

	t.Run("разные языки без src НЕ сливаются", func(t *testing.T) {
		books := []groupBook{
			{id: 1, workID: 1, normTitle: "the hobbit", lang: "en"},
			{id: 2, workID: 2, normTitle: "the hobbit", lang: "ru"},
		}
		require.Equal(t, [][]int64{{1}, {2}}, clustersOf(books, clusterTier1(books)))
	})

	t.Run("перевод ↔ оригинал по src-title-info", func(t *testing.T) {
		books := []groupBook{
			{id: 1, workID: 1, normTitle: "the hobbit", lang: "en"},
			{id: 2, workID: 2, normTitle: "хоббит", lang: "ru",
				srcTitleNorm: "the hobbit", srcAuthorNorm: "tolkien john", srcLang: "en"},
		}
		require.Equal(t, [][]int64{{1, 2}}, clustersOf(books, clusterTier1(books)))
	})

	t.Run("два перевода без оригинала сливаются по общему src-ключу", func(t *testing.T) {
		books := []groupBook{
			{id: 1, workID: 1, normTitle: "хоббит", lang: "ru",
				srcTitleNorm: "the hobbit", srcAuthorNorm: "tolkien john", srcLang: "en"},
			{id: 2, workID: 2, normTitle: "хоббит, или туда и обратно", lang: "ru",
				srcTitleNorm: "the hobbit", srcAuthorNorm: "tolkien john", srcLang: "en"},
		}
		require.Equal(t, [][]int64{{1, 2}}, clustersOf(books, clusterTier1(books)))
	})

	t.Run("fb2_doc_id — точный дубль файла", func(t *testing.T) {
		books := []groupBook{
			{id: 1, workID: 1, normTitle: "a", lang: "ru", docID: "guid-xyz"},
			{id: 2, workID: 2, normTitle: "b", lang: "ru", docID: "guid-xyz"},
			{id: 3, workID: 3, normTitle: "c", lang: "ru", docID: ""},
		}
		require.Equal(t, [][]int64{{1, 2}, {3}}, clustersOf(books, clusterTier1(books)))
	})

	t.Run("транзитивность: оригинал + два перевода в одну работу", func(t *testing.T) {
		books := []groupBook{
			{id: 1, workID: 1, normTitle: "dune", lang: "en"},
			{id: 2, workID: 2, normTitle: "дюна", lang: "ru", srcTitleNorm: "dune", srcLang: "en"},
			{id: 3, workID: 3, normTitle: "дюна", lang: "ru", srcTitleNorm: "dune", srcLang: "en"},
		}
		require.Equal(t, [][]int64{{1, 2, 3}}, clustersOf(books, clusterTier1(books)))
	})

	t.Run("пустой src не создаёт ложных союзов", func(t *testing.T) {
		books := []groupBook{
			{id: 1, workID: 1, normTitle: "разные", lang: "ru"},
			{id: 2, workID: 2, normTitle: "книги", lang: "ru"},
		}
		require.Equal(t, [][]int64{{1}, {2}}, clustersOf(books, clusterTier1(books)))
	})

	// ── Tier-1.5: один том серии ⇒ одна работа ──
	t.Run("Tier-1.5: разные названия с одним ser_no сливаются (кейс Страйка)", func(t *testing.T) {
		books := []groupBook{
			{id: 1, workID: 1, normTitle: "развороченная могила", lang: "ru",
				seriesID: 5, serNo: 7, srcTitleNorm: "the running grave", srcLang: "en"},
			{id: 2, workID: 2, normTitle: "неизбежная могила", lang: "ru",
				seriesID: 5, serNo: 7}, // src пуст — Tier-1 по src не свёл бы
		}
		require.Equal(t, [][]int64{{1, 2}}, clustersOf(books, clusterTier1(books)))
	})

	t.Run("Tier-1.5: разные ser_no НЕ сливаются", func(t *testing.T) {
		books := []groupBook{
			{id: 1, workID: 1, normTitle: "том 1", lang: "ru", seriesID: 5, serNo: 1},
			{id: 2, workID: 2, normTitle: "том 2", lang: "ru", seriesID: 5, serNo: 2},
		}
		require.Equal(t, [][]int64{{1}, {2}}, clustersOf(books, clusterTier1(books)))
	})

	t.Run("Tier-1.5: ser_no=0 (вне серии) не группирует", func(t *testing.T) {
		books := []groupBook{
			{id: 1, workID: 1, normTitle: "сборник а", lang: "ru", seriesID: 5, serNo: 0},
			{id: 2, workID: 2, normTitle: "сборник б", lang: "ru", seriesID: 5, serNo: 0},
		}
		require.Equal(t, [][]int64{{1}, {2}}, clustersOf(books, clusterTier1(books)))
	})

	t.Run("Tier-1.5: конфликт оригиналов (разные src) НЕ сливает один ser_no", func(t *testing.T) {
		books := []groupBook{
			{id: 1, workID: 1, normTitle: "книга а", lang: "ru", seriesID: 5, serNo: 3,
				srcTitleNorm: "original a", srcLang: "en"},
			{id: 2, workID: 2, normTitle: "книга б", lang: "ru", seriesID: 5, serNo: 3,
				srcTitleNorm: "original b", srcLang: "en"},
		}
		require.Equal(t, [][]int64{{1}, {2}}, clustersOf(books, clusterTier1(books)))
	})

	t.Run("Tier-1.5: один ser_no в РАЗНЫХ сериях не путается", func(t *testing.T) {
		books := []groupBook{
			{id: 1, workID: 1, normTitle: "а", lang: "ru", seriesID: 5, serNo: 1},
			{id: 2, workID: 2, normTitle: "б", lang: "ru", seriesID: 9, serNo: 1},
		}
		require.Equal(t, [][]int64{{1}, {2}}, clustersOf(books, clusterTier1(books)))
	})
}

func TestPickCanonicalWork(t *testing.T) {
	// Доминирующая работа (больше членов) выигрывает; тай-брейк — меньший id.
	books := []groupBook{
		{id: 1, workID: 10}, {id: 2, workID: 10}, {id: 3, workID: 7},
	}
	require.Equal(t, int64(10), pickCanonicalWork(books, []int{0, 1, 2}))

	// Тай по количеству → меньший work_id.
	books2 := []groupBook{{id: 1, workID: 5}, {id: 2, workID: 9}}
	require.Equal(t, int64(5), pickCanonicalWork(books2, []int{0, 1}))
}

func TestExtFieldFor(t *testing.T) {
	require.Equal(t, "ol_work", extFieldFor("openlibrary"))
	require.Equal(t, "wd_qid", extFieldFor("wikidata"))
}

// tier2BucketConflicts — гейт union'а по внешнему work_key: конфликт оригиналов
// или номеров тома внутри бакета = ошибочный резолв, не сливать.
func TestTier2BucketConflicts(t *testing.T) {
	books := []groupBook{
		{id: 1, srcTitleNorm: "original a", serNo: 0}, // 0
		{id: 2, srcTitleNorm: "original b", serNo: 0}, // 1
		{id: 3, srcTitleNorm: "", serNo: 0},           // 2
		{id: 4, srcTitleNorm: "original a", serNo: 3}, // 3
		{id: 5, srcTitleNorm: "original a", serNo: 4}, // 4
		{id: 6, srcTitleNorm: "", serNo: 3},           // 5
	}
	cases := []struct {
		name string
		idxs []int
		want bool
	}{
		{"разные непустые src — конфликт", []int{0, 1}, true},
		{"одинаковый src — ок", []int{0, 3}, false},
		{"пустой src не конфликтует с непустым", []int{0, 2}, false},
		{"оба пустых — ок", []int{2, 5}, false},
		{"разные ненулевые ser_no — конфликт (разные тома)", []int{3, 4}, true},
		{"ser_no 0 не конфликтует с ненулевым", []int{0, 3}, false},
		{"одиночный бакет — ок", []int{1}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tier2BucketConflicts(books, tc.idxs))
		})
	}
}

// Tier-1.5, mixed-кейс: пустой src не «мостит» конфликт двух разных оригиналов —
// бакет с конфликтом пропускается ЦЕЛИКОМ (и книга без src тоже не сливается).
func TestClusterTier1_Tier15_EmptySrcDoesNotBridgeConflict(t *testing.T) {
	books := []groupBook{
		{id: 1, workID: 1, normTitle: "книга а", lang: "ru", seriesID: 5, serNo: 3,
			srcTitleNorm: "original a", srcLang: "en"},
		{id: 2, workID: 2, normTitle: "книга б", lang: "ru", seriesID: 5, serNo: 3,
			srcTitleNorm: "original b", srcLang: "en"},
		{id: 3, workID: 3, normTitle: "книга в", lang: "ru", seriesID: 5, serNo: 3}, // src пуст
	}
	require.Equal(t, [][]int64{{1}, {2}, {3}}, clustersOf(books, clusterTier1(books)))
}
