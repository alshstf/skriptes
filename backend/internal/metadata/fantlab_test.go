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
   "markcount":6724,"name":"","rusname":"Метро 2033","work_id":4351,"year":2005},
  {"all_autor_rusname":"Шимун Врочек","altname":"Метро 2033: Питер-2. Убер и компания","autor_rusname":"Шимун Врочек",
   "markcount":72,"name":"","rusname":"Метро 2035: Питер. Война","work_id":648001,"year":2018}
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
