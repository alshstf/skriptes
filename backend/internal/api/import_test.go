package api

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/skriptes/skriptes/backend/internal/auth"
	"github.com/stretchr/testify/require"
)

func importRequest(t *testing.T, fields map[string]string, file string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		require.NoError(t, mw.WriteField(k, v))
	}
	if file != "" {
		fw, err := mw.CreateFormFile("file", "export.csv")
		require.NoError(t, err)
		_, _ = fw.Write([]byte(file))
	}
	require.NoError(t, mw.Close())
	r := httptest.NewRequest(http.MethodPost, "/api/me/import/preview", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	return r.WithContext(context.WithValue(r.Context(), userCtxKey{}, auth.User{ID: 1}))
}

// TestImportPreviewRejects — до базы не доходит: предел файла, нет файла,
// неизвестный формат и формат без книг отвечают понятной ошибкой.
func TestImportPreviewRejects(t *testing.T) {
	h := handleImportPreview(ImportDeps{MaxBytes: 64}, ContentDeps{})
	cases := []struct {
		name   string
		fields map[string]string
		file   string
		code   int
		msg    string
	}{
		{"больше предела", nil, strings.Repeat("a", 65), http.StatusRequestEntityTooLarge, "файл больше"},
		{"без файла", map[string]string{"source": "csv"}, "", http.StatusBadRequest, "нужен файл"},
		{"чужой source", map[string]string{"source": "bookmate"}, "title\nX\n", http.StatusBadRequest, "нужен файл"},
		{"не формат", nil, "просто текст\n", http.StatusUnprocessableEntity, "формат файла не распознан"},
		{"без книг", nil, "title,author\n", http.StatusUnprocessableEntity, "ни одной книги"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			h(w, importRequest(t, c.fields, c.file))
			require.Equal(t, c.code, w.Code, w.Body.String())
			require.Contains(t, w.Body.String(), c.msg)
		})
	}
}

func TestImportTemplate(t *testing.T) {
	w := httptest.NewRecorder()
	handleImportTemplate()(w, httptest.NewRequest(http.MethodGet, "/api/me/import/template.csv", nil))
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Header().Get("Content-Disposition"), "skriptes-import-template.csv")
	require.True(t, strings.HasPrefix(w.Body.String(), "\ufefftitle,author,"), "BOM — чтобы Excel открыл кириллицу")
}
