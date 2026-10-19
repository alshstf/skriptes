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
	"github.com/stretchr/testify/require"
	tcmeili "github.com/testcontainers/testcontainers-go/modules/meilisearch"

	"github.com/skriptes/skriptes/backend/internal/auth"
	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/catalog"
	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/skriptes/skriptes/backend/internal/inpx/inpxtest"
	"github.com/skriptes/skriptes/backend/internal/opds"
	"github.com/skriptes/skriptes/backend/internal/testpg"
)

// Скрытие на инстансе (админ, 0064) сквозь PG и Meili: книги скрытого автора
// пропадают из лент и поиска OPDS (Meili-фильтр), сам автор — из перечня
// авторов (PG), возврат открывает всё обратно.
func TestOPDSInstanceHidden(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	const masterKey = "test-master-key"
	mC, err := tcmeili.Run(ctx, "getmeili/meilisearch:v1.13", tcmeili.WithMasterKey(masterKey))
	require.NoError(t, err)
	t.Cleanup(func() { _ = mC.Terminate(context.Background()) })
	addr, err := mC.Address(ctx)
	require.NoError(t, err)
	mgr := meili.New(addr, meili.WithAPIKey(masterKey))
	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr, MeiliURL: addr, MeiliAPIKey: masterKey})
	path, err := inpxtest.WriteINPX(t.TempDir(), "lib.inpx", []inpxtest.Book{
		{LibID: "1", Title: "Дюна", Authors: []string{"Герберт,Фрэнк"}, Genres: []string{"sf"}, Lang: "ru"},
		{LibID: "2", Title: "Солярис", Authors: []string{"Лем,Станислав"}, Genres: []string{"sf"}, Lang: "ru"},
	})
	require.NoError(t, err)
	_, err = imp.Run(ctx, path)
	require.NoError(t, err)

	cat := catalog.New(pool)
	h := opds.NewHandler(opds.Config{BaseURL: "https://lib.example"}, opds.Deps{Books: books.New(pool, mgr, nil), Catalog: cat})
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.ContextWithUser(req.Context(), auth.User{ID: 1})))
		})
	})
	r.Get("/opds/v2/recent", h.V2Recent)
	r.Get("/opds/v2/search", h.V2Search)
	titles := func(path string) []string {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		pubs, _ := out["publications"].([]any)
		var ts []string
		for _, p := range pubs {
			ts = append(ts, p.(map[string]any)["metadata"].(map[string]any)["title"].(string))
		}
		return ts
	}
	authorNames := func() []string {
		items, _, err := cat.ListAuthors(ctx, 50, 0)
		require.NoError(t, err)
		var ns []string
		for _, a := range items {
			ns = append(ns, a.FullName)
		}
		return ns
	}

	// Ничего не скрыто — видно всё (поля instance_hidden нет ни в одном документе).
	require.Eventually(t, func() bool { return len(titles("/opds/v2/recent")) == 2 }, 20*time.Second, 200*time.Millisecond)

	var lem int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT id FROM authors WHERE last_name = 'Лем'`).Scan(&lem))
	_, err = pool.Exec(ctx, `INSERT INTO hidden_authors (author_id) VALUES ($1)`, lem)
	require.NoError(t, err)
	nb, nw, err := imp.SyncInstanceHidden(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, nb)
	require.Equal(t, 1, nw)
	require.Equal(t, []string{"Дюна"}, titles("/opds/v2/recent"))
	require.Empty(t, titles("/opds/v2/search?query=солярис"))
	require.NotContains(t, authorNames(), "Лем Станислав")

	_, err = pool.Exec(ctx, `DELETE FROM hidden_authors WHERE author_id = $1`, lem)
	require.NoError(t, err)
	_, _, err = imp.SyncInstanceHidden(ctx)
	require.NoError(t, err)
	require.Len(t, titles("/opds/v2/recent"), 2)
	require.Equal(t, []string{"Солярис"}, titles("/opds/v2/search?query=солярис"))
}
