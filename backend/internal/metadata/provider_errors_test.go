package metadata

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestStatusErr — классификация не-2xx статусов внешних провайдеров.
// Суть фикса: 429/битый ключ/5xx НЕ должны маппиться в ErrNotFound (иначе
// backfill пишет outcome "not_found" с TTL 90 дней и книга «отравлена» —
// именно так на проде накопилось 110k GB not_found из анонимных 429).
func TestStatusErr(t *testing.T) {
	// 404 — честное отсутствие (path-запросы /isbn/…, /works/…/ratings.json).
	require.ErrorIs(t, statusErr(http.StatusNotFound), ErrNotFound)
	require.NotErrorIs(t, statusErr(http.StatusNotFound), ErrUpstream)

	// Транзиент/операционное → ErrUpstream, НЕ ErrNotFound (backfill → "error",
	// короткий ретрай).
	for _, code := range []int{
		http.StatusTooManyRequests,     // 429 rate limit (анонимная квота GB)
		http.StatusBadRequest,          // 400 API_KEY_INVALID
		http.StatusForbidden,           // 403
		http.StatusServiceUnavailable,  // 503 (реальный ответ GB на рус. запрос)
		http.StatusInternalServerError, // 500
	} {
		err := statusErr(code)
		require.ErrorIsf(t, err, ErrUpstream, "code %d → ErrUpstream", code)
		require.NotErrorIsf(t, err, ErrNotFound, "code %d НЕ ErrNotFound", code)
	}
}

// TestGoogleBooks_FetchRating_KeyAndClassification — регрессия ДВУХ багов GB:
//  1. FetchRating ЗАБЫВАЛ addKey → все запросы рейтинга уходили анонимно (429),
//     0 вызовов под ключом в консоли Google. Проверяем, что key= теперь долетает.
//  2. Не-200 маппился в ErrNotFound → отравление. Проверяем 429→ErrUpstream,
//     а 200-без-рейтинга → честный ErrNotFound.
func TestGoogleBooks_FetchRating_KeyAndClassification(t *testing.T) {
	// (1)+(2a): ключ уходит, 200 c averageRating → результат.
	var gotKey string
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.URL.Query().Get("key")
		_, _ = io.WriteString(w, `{"items":[{"volumeInfo":{"averageRating":4.5,"ratingsCount":120}}]}`)
	}))
	defer ok.Close()
	p := NewGoogleBooksProvider(ok.Client()).WithEndpoint(ok.URL).WithAPIKey("k-42")
	res, err := p.FetchRating(context.Background(), WorkQuery{Title: "T", Authors: []string{"A"}})
	require.NoError(t, err)
	require.Equal(t, "k-42", gotKey, "FetchRating обязан слать key= (баг: забывал addKey)")
	require.InDelta(t, 4.5, res.Average, 0.001)
	require.Equal(t, 120, res.Count)

	// (2b): 429 → ErrUpstream, НЕ ErrNotFound.
	rl := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer rl.Close()
	p2 := NewGoogleBooksProvider(rl.Client()).WithEndpoint(rl.URL).WithAPIKey("k")
	_, err = p2.FetchRating(context.Background(), WorkQuery{Title: "T", Authors: []string{"A"}})
	require.ErrorIs(t, err, ErrUpstream)
	require.NotErrorIs(t, err, ErrNotFound)

	// (2c): 200 без averageRating → честный ErrNotFound (нашли, но рейтинга нет).
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"items":[{"volumeInfo":{}}]}`)
	}))
	defer empty.Close()
	p3 := NewGoogleBooksProvider(empty.Client()).WithEndpoint(empty.URL).WithAPIKey("k")
	_, err = p3.FetchRating(context.Background(), WorkQuery{Title: "T", Authors: []string{"A"}})
	require.ErrorIs(t, err, ErrNotFound)
}

// TestOpenLibrary_FetchRating_TransientIsNotNotFound — тот же контракт для OL:
// 503 при резолве work-ключа → ErrUpstream, не ErrNotFound.
func TestOpenLibrary_FetchRating_TransientIsNotNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	p := NewOpenLibraryProvider(srv.Client()).WithEndpoints(srv.URL+"/search.json", srv.URL)
	_, err := p.FetchRating(context.Background(), WorkQuery{Title: "T", Authors: []string{"A"}})
	require.ErrorIs(t, err, ErrUpstream)
	require.NotErrorIs(t, err, ErrNotFound)
}
