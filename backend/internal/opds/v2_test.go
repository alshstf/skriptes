package opds_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	meili "github.com/meilisearch/meilisearch-go"
	"github.com/skriptes/skriptes/backend/internal/auth"
	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/catalog"
	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/skriptes/skriptes/backend/internal/inpx/inpxtest"
	"github.com/skriptes/skriptes/backend/internal/opds"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
	tcmeili "github.com/testcontainers/testcontainers-go/modules/meilisearch"
)

// TestOPDS2 — каталог OPDS 2.0 (#389, B7): корень с разделами и шаблоном
// поиска, ленты книг с публикациями и ссылками скачивания, поиск, скрытый
// пользователю язык не показывается.
func TestOPDS2(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	const masterKey = "test-master-key-1234567890"
	mC, err := tcmeili.Run(ctx, "getmeili/meilisearch:v1.13", tcmeili.WithMasterKey(masterKey))
	require.NoError(t, err)
	t.Cleanup(func() { _ = mC.Terminate(context.Background()) })
	addr, err := mC.Address(ctx)
	require.NoError(t, err)
	mgr := meili.New(addr, meili.WithAPIKey(masterKey))

	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr, MeiliURL: addr, MeiliAPIKey: masterKey})
	path, err := inpxtest.WriteINPX(t.TempDir(), "lib.inpx", []inpxtest.Book{
		{LibID: "1", Title: "Дюна", Series: "Хроники Дюны", SerNo: 1, Authors: []string{"Герберт,Фрэнк"}, Genres: []string{"sf"}, Lang: "ru"},
		{LibID: "2", Title: "Dune Messiah", Authors: []string{"Герберт,Фрэнк"}, Genres: []string{"sf"}, Lang: "en"},
	})
	require.NoError(t, err)
	_, err = imp.Run(ctx, path)
	require.NoError(t, err)

	h := opds.NewHandler(opds.Config{BaseURL: "https://lib.example"}, opds.Deps{
		Books: books.New(pool, mgr, nil), Catalog: catalog.New(pool),
		Exclusions: func(context.Context, int64) ([]string, []string) { return nil, []string{"en"} },
	})
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.ContextWithUser(req.Context(), auth.User{ID: 1})))
		})
	})
	r.Get("/opds/v2/", h.V2Root)
	r.Get("/opds/v2/recent", h.V2Recent)
	r.Get("/opds/v2/search", h.V2Search)
	r.Get("/opds/v2/genres", h.V2Genres)
	get := func(path string) (int, string, map[string]any) {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, rec.Header().Get("Content-Type"), out
	}

	code, ct, root := get("/opds/v2/")
	require.Equal(t, http.StatusOK, code)
	require.Contains(t, ct, "application/opds+json")
	require.Len(t, root["navigation"], 4)
	var templated bool
	for _, l := range root["links"].([]any) {
		lm := l.(map[string]any)
		if lm["rel"] == "search" {
			templated = lm["templated"] == true
			require.Equal(t, "https://lib.example/opds/v2/search{?query}", lm["href"])
		}
	}
	require.True(t, templated, "поиск — шаблонной ссылкой")

	var pubs []any
	require.Eventually(t, func() bool {
		_, _, recent := get("/opds/v2/recent")
		pubs, _ = recent["publications"].([]any)
		return len(pubs) == 1
	}, 20*time.Second, 200*time.Millisecond, "английское издание скрыто пользователю")
	pub := pubs[0].(map[string]any)
	meta := pub["metadata"].(map[string]any)
	require.Equal(t, "Дюна", meta["title"])
	require.Equal(t, "http://schema.org/Book", meta["@type"])
	series := meta["belongsTo"].(map[string]any)["series"].([]any)[0].(map[string]any)
	require.Equal(t, "Хроники Дюны", series["name"])
	link := pub["links"].([]any)[0].(map[string]any)
	require.Equal(t, "http://opds-spec.org/acquisition", link["rel"])
	require.Contains(t, link["href"], "/opds/books/")

	code, _, found := get("/opds/v2/search?query=" + "дюна")
	require.Equal(t, http.StatusOK, code)
	require.Len(t, found["publications"], 1)
	code, _, _ = get("/opds/v2/search")
	require.Equal(t, http.StatusBadRequest, code)

	_, _, genres := get("/opds/v2/genres")
	require.NotNil(t, genres["metadata"])
}
