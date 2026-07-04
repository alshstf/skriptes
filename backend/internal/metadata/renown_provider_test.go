package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// olRenownFixture — усечённый РЕАЛЬНЫЙ ответ /search.json (снят 2026-07-04):
// переводы одной книги у OL — раздельные records, счётчики суммируются.
const olRenownFixture = `{"numFound":2,"docs":[
  {"author_name":["Дмитрий Глуховский"],"key":"/works/OL16796766W","title":"Метро 2033","ratings_count":19,"want_to_read_count":258},
  {"author_name":["Дмитрий Глуховский"],"key":"/works/OL19999631W","title":"Metro 2033","ratings_count":17,"want_to_read_count":44}
]}`

func olRenownTestServer(t *testing.T, body string) *OpenLibraryProvider {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NotEmpty(t, r.URL.Query().Get("title"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return NewOpenLibraryProvider(srv.Client()).WithEndpoints(srv.URL, "")
}

func TestOpenLibraryFetchRenown_SumsMatchedDocs(t *testing.T) {
	p := olRenownTestServer(t, olRenownFixture)
	res, err := p.FetchRenown(context.Background(), WorkQuery{
		Title: "Metro 2033", LastName: "Глуховский", FirstName: "Дмитрий",
	})
	require.NoError(t, err)
	require.Equal(t, 19+17, res.Ratings, "переводы одной книги — раздельные OL-records, суммируем")
	require.Equal(t, 258+44, res.Want)
}

func TestOpenLibraryFetchRenown_AuthorGate(t *testing.T) {
	p := olRenownTestServer(t, olRenownFixture)
	_, err := p.FetchRenown(context.Background(), WorkQuery{
		Title: "Metro 2033", LastName: "Иванов", FirstName: "Пётр",
	})
	require.ErrorIs(t, err, ErrNotFound, "однофамильцы не проходят precision-гейт")
}

func TestOpenLibraryFetchRenown_PrefersSrcTitle(t *testing.T) {
	var gotTitle string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTitle = r.URL.Query().Get("title")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"numFound":0,"docs":[]}`))
	}))
	t.Cleanup(srv.Close)
	p := NewOpenLibraryProvider(srv.Client()).WithEndpoints(srv.URL, "")
	_, err := p.FetchRenown(context.Background(), WorkQuery{
		Title: "Метро 2033", SrcTitle: "Metro 2033", LastName: "Глуховский",
	})
	require.ErrorIs(t, err, ErrNotFound)
	require.Equal(t, "Metro 2033", gotTitle, "переводная книга ищется по оригиналу")
}

func TestOpenLibraryFetchRenown_ZeroCounts(t *testing.T) {
	p := olRenownTestServer(t, `{"numFound":1,"docs":[
		{"author_name":["Дмитрий Глуховский"],"key":"/works/OLxW","title":"Metro 2033","ratings_count":0,"want_to_read_count":0}
	]}`)
	_, err := p.FetchRenown(context.Background(), WorkQuery{
		Title: "Metro 2033", LastName: "Глуховский",
	})
	require.ErrorIs(t, err, ErrNotFound, "нулевые счётчики = сигнала нет")
}
