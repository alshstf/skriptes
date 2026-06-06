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
