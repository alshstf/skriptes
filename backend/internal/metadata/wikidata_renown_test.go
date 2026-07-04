package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// wdSitelinksFixture — усечённый РЕАЛЬНЫЙ ответ wbgetentities&props=sitelinks
// (снят 2026-07-04, Q188538 «Мастер и Маргарита»; 3 sitelinks из 78 — форма та же).
const wdSitelinksFixture = `{"entities":{"Q188538":{"type":"item","id":"Q188538","sitelinks":{
  "dewiki":{"site":"dewiki","title":"Der Meister und Margarita","badges":[]},
  "enwiki":{"site":"enwiki","title":"The Master and Margarita","badges":[]},
  "ruwiki":{"site":"ruwiki","title":"Мастер и Маргарита","badges":["Q17437796"]}
}}},"success":1}`

func TestWikidataFetchRenown_KnownQIDSkipsResolve(t *testing.T) {
	var gotAction []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAction = append(gotAction, r.URL.Query().Get("action"))
		require.NotEmpty(t, r.Header.Get("User-Agent"), "Wikimedia требует UA (без него лимит 10 req/min)")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(wdSitelinksFixture))
	}))
	t.Cleanup(srv.Close)
	p := NewWikidataAdaptationsProvider(srv.Client()).WithEndpoints(srv.URL, srv.URL, "")

	res, err := p.FetchRenown(context.Background(), WorkQuery{
		Title: "Мастер и Маргарита", WikidataQID: "Q188538",
	})
	require.NoError(t, err)
	require.Equal(t, 3, res.Sitelinks)
	require.Equal(t, []string{"wbgetentities"}, gotAction,
		"с готовым QID резолв по названию пропускается — один запрос sitelinks")
}

func TestWikidataFetchRenown_NoSitelinks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"entities":{"Q1":{"type":"item","id":"Q1","sitelinks":{}}},"success":1}`))
	}))
	t.Cleanup(srv.Close)
	p := NewWikidataAdaptationsProvider(srv.Client()).WithEndpoints(srv.URL, srv.URL, "")

	_, err := p.FetchRenown(context.Background(), WorkQuery{WikidataQID: "Q1"})
	require.ErrorIs(t, err, ErrNotFound, "нет статей ни в одной вики = сигнала нет")
}
