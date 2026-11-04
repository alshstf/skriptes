package api

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/converter"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// Проверка перед скачиванием (#631): 204, когда исходник на месте, и 404 с
// текстом для читателя, когда архива нет или fb2 в нём нет.
func TestDownloadCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)

	root := t.TempDir()
	f, err := os.Create(filepath.Join(root, "a.zip"))
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	_, err = zw.Create("1.fb2")
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	require.NoError(t, f.Close())
	conv, err := converter.New(root, t.TempDir(), "fbc")
	require.NoError(t, err)

	var collID int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO collections (name, inpx_filename) VALUES ('c','c.inpx') RETURNING id`).Scan(&collID))
	archive := func(name string) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `INSERT INTO archives (collection_id, filename) VALUES ($1,$2) RETURNING id`, collID, name).Scan(&id))
		return id
	}
	book := func(archID int64, file string) int64 {
		var wid, id int64
		require.NoError(t, pool.QueryRow(ctx, `INSERT INTO works (title, normalized_title) VALUES ($1, $2) RETURNING id`, file, file).Scan(&wid))
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id)
			VALUES ($1,$2,$3,$4,'fb2',$5,$6,$7) RETURNING id`, collID, archID, file, file, file, file, wid).Scan(&id))
		return id
	}
	a, gone := archive("a.zip"), archive("gone.zip")
	ok, noFile, noArchive := book(a, "1"), book(a, "2"), book(gone, "3")

	r := chi.NewRouter()
	r.Get("/books/{id}/download/check", handleDownloadCheck(DownloadDeps{Books: books.New(pool, nil, nil), Converter: conv}))
	check := func(id int64, format string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/books/%d/download/check?format=%s", id, format), nil))
		return rec
	}

	require.Equal(t, http.StatusNoContent, check(ok, "epub3").Code)
	for _, id := range []int64{noFile, noArchive} {
		rec := check(id, "fb2")
		require.Equal(t, http.StatusNotFound, rec.Code)
		var body map[string]string
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Equal(t, "archive file not available", body["error"])
		require.Equal(t, msgBookFileMissing, body["message"])
	}
	require.Equal(t, http.StatusNotFound, check(999999, "epub3").Code)
	require.Equal(t, http.StatusBadRequest, check(ok, "doc").Code)
}

// Том не читается (libcheck) — скачивание, ридер и проверка отвечают 503
// сразу, ещё до БД и диска: иначе запрос висит в ядре до I/O error.
func TestLibraryUnreadable_FailsFast(t *testing.T) {
	d := DownloadDeps{LibraryUnreadable: func() (bool, string) { return true, "books volume does not respond" }}
	r := chi.NewRouter()
	r.Get("/books/{id}/download/check", handleDownloadCheck(d))
	r.Get("/books/{id}/download", handleDownload(d, HistoryDeps{}))
	r.Get("/books/{id}/epub", handleEpub(d))
	for _, path := range []string{"/books/1/download/check?format=epub3", "/books/1/download?format=fb2", "/books/1/epub"} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, http.StatusServiceUnavailable, rec.Code, path)
		var body map[string]string
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.Equal(t, msgLibraryUnreadable, body["message"], path)
	}
}
