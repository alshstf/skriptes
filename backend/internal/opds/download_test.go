package opds_test

import (
	"archive/zip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/skriptes/skriptes/backend/internal/auth"
	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/converter"
	"github.com/skriptes/skriptes/backend/internal/dlimit"
	"github.com/skriptes/skriptes/backend/internal/opds"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestDownload — скачивание через OPDS (#389, B1): fb2 — одна книга, а не весь
// архив раздачи; скрытое пользователю — 404; лимит скачиваний — 429 с Retry-After.
func TestDownload(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)

	root := t.TempDir()
	f, err := os.Create(filepath.Join(root, "a.zip"))
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	for name, body := range map[string]string{"1.fb2": "<FictionBook>один</FictionBook>", "2.fb2": "<FictionBook>два</FictionBook>"} {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = io.WriteString(w, body)
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	require.NoError(t, f.Close())

	var collID, archID int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO collections (name, inpx_filename) VALUES ('c','c.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	book := func(file, lang string) int64 {
		var wid, id int64
		require.NoError(t, pool.QueryRow(ctx, `INSERT INTO works (title, normalized_title) VALUES ($1, $2) RETURNING id`, file, file).Scan(&wid))
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id, lang)
			VALUES ($1,$2,$3,$4,'fb2',$5,$6,$7,$8) RETURNING id`, collID, archID, file, file, file, file, wid, lang).Scan(&id))
		return id
	}
	ru, en := book("1", "ru"), book("2", "en")

	conv, err := converter.New(root, t.TempDir(), "")
	require.NoError(t, err)
	h := opds.NewHandler(opds.Config{}, opds.Deps{
		Books:     books.New(pool, nil, nil),
		Converter: conv,
		Exclusions: func(context.Context, int64) ([]string, []string) {
			return nil, []string{"EN"}
		},
		Limiter: dlimit.New(5, 2, 10*time.Minute),
	})
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.ContextWithUser(req.Context(), auth.User{ID: 7})))
		})
	})
	r.Get("/opds/books/{id}/download", h.Download)
	get := func(id int64) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/opds/books/"+strconv.FormatInt(id, 10)+"/download?format=fb2", nil))
		return rec
	}

	rec := get(ru)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "<FictionBook>один</FictionBook>", rec.Body.String(), "одна книга, а не zip-архив")

	require.Equal(t, http.StatusNotFound, get(en).Code, "английский скрыт пользователю")

	require.Equal(t, http.StatusOK, get(ru).Code)
	rec = get(ru)
	require.Equal(t, http.StatusTooManyRequests, rec.Code, "два скачивания за окно, третье — ждать")
	require.NotEmpty(t, rec.Header().Get("Retry-After"))
}
