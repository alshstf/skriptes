package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// fantlabFixture — усечённый РЕАЛЬНЫЙ ответ api.fantlab.ru/search-works
// (снят 2026-07-04, q=«Метро 2033»): матч самой книги + матч-сосед другого
// автора. Лишние поля выброшены (парсим defensively только нужные).
const fantlabFixture = `{"matches":[
  {"all_autor_rusname":"Дмитрий Глуховский","altname":"М-Е-Т-Р-О","autor_rusname":"Дмитрий Глуховский",
   "markcount":6724,"name":"","rusname":"Метро 2033","work_id":4351,"year":2005,"work_type_id":1},
  {"all_autor_rusname":"Шимун Врочек","altname":"Метро 2033: Питер-2. Убер и компания","autor_rusname":"Шимун Врочек",
   "markcount":72,"name":"","rusname":"Метро 2035: Питер. Война","work_id":648001,"year":2018,"work_type_id":1},
  {"all_autor_rusname":"Роберт Шекли","altname":"","autor_rusname":"Роберт Шекли",
   "markcount":310,"name":"","rusname":"Лавка миров","work_id":9999,"year":1991,"work_type_id":3}
]}`

func fantlabTestServer(t *testing.T) *FantlabProvider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NotEmpty(t, r.URL.Query().Get("q"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(fantlabFixture))
	}))
	t.Cleanup(srv.Close)
	return NewFantlabProvider(srv.Client()).WithEndpoint(srv.URL)
}

func TestFantlabFetchRenown_HappyPath(t *testing.T) {
	p := fantlabTestServer(t)
	res, err := p.FetchRenown(context.Background(), WorkQuery{
		Title: "Метро 2033", LastName: "Глуховский", FirstName: "Дмитрий",
	})
	require.NoError(t, err)
	require.Equal(t, 6724, res.Ratings)
	require.Zero(t, res.Want)
	require.Equal(t, "novel", res.Kind, "work_type_id=1 (роман) → уверенно обычное произведение")
}

func TestFantlabFetchRenown_CollectionKind(t *testing.T) {
	p := fantlabTestServer(t)
	// work_type_id=3 (сборник) → Kind=collection.
	res, err := p.FetchRenown(context.Background(), WorkQuery{
		Title: "Лавка миров", LastName: "Шекли", FirstName: "Роберт",
	})
	require.NoError(t, err)
	require.Equal(t, "collection", res.Kind)
}

func TestFantlabKindMapping(t *testing.T) {
	require.Equal(t, "collection", fantlabKind(3))
	require.Equal(t, "anthology", fantlabKind(17), "антология")
	require.Equal(t, "anthology", fantlabKind(56), "серия антологий")
	for _, id := range []int{1, 44, 45, 21, 5, 8, 41} {
		require.Equalf(t, "novel", fantlabKind(id), "id=%d — обычное произведение", id)
	}
	// Цикл — серия, не сборник; статья/незнакомый id — не решаем.
	for _, id := range []int{4, 7, 11, 12, 52, 0, 999} {
		require.Emptyf(t, fantlabKind(id), "id=%d — тип не решается", id)
	}
}

func TestFantlabFetchRenown_AuthorGate(t *testing.T) {
	p := fantlabTestServer(t)
	// Однофамилец-мимо: название совпало, автор — нет → precision-гейт режет.
	_, err := p.FetchRenown(context.Background(), WorkQuery{
		Title: "Метро 2033", LastName: "Иванов", FirstName: "Пётр",
	})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestFantlabFetchRenown_TitleGate(t *testing.T) {
	p := fantlabTestServer(t)
	// Другое название того же автора → не наш матч (markcount чужой книги
	// не присваивается).
	_, err := p.FetchRenown(context.Background(), WorkQuery{
		Title: "Метро 2034", LastName: "Глуховский", FirstName: "Дмитрий",
	})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestFantlabFetchRenown_AltNameMatch(t *testing.T) {
	p := fantlabTestServer(t)
	// Совпадение по altname (издательское название) тоже принимается.
	res, err := p.FetchRenown(context.Background(), WorkQuery{
		Title: "М-Е-Т-Р-О", LastName: "Глуховский", FirstName: "Дмитрий",
	})
	require.NoError(t, err)
	require.Equal(t, 6724, res.Ratings)
}

func TestFantlabFetchRenown_EmptyTitle(t *testing.T) {
	p := fantlabTestServer(t)
	_, err := p.FetchRenown(context.Background(), WorkQuery{LastName: "Глуховский"})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestNormalizeRenownTitle(t *testing.T) {
	require.Equal(t, "метро 2033", normalizeRenownTitle("  Метро   2033 "))
	require.Equal(t, "елка", normalizeRenownTitle("Ёлка"))
}
