package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/api"
	"github.com/skriptes/skriptes/backend/internal/auth"
	"github.com/skriptes/skriptes/backend/internal/kosync"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestKosync — синхронизация KOReader (#389, B4): вход ключом пароля устройства
// (основной пароль — нет), регистрации нет, позиция сохраняется и отдаётся, у
// книги каталога доля прочитанного переходит в чтение, с 0,99 — «прочитано».
func TestKosync(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	svc := auth.New(pool, 4)
	const email, password = "koreader@example.com", "correct horse battery"
	u, err := svc.CreateUser(ctx, email, "K", password, auth.RoleUser)
	require.NoError(t, err)
	_, devicePassword, err := svc.CreateDevice(ctx, u.ID, "KOReader")
	require.NoError(t, err)
	key := auth.DeviceKey(devicePassword)

	var collID, archID, bookID int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO collections (name, inpx_filename) VALUES ('c','c.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title)
		VALUES ($1,$2,'1','1','fb2','Книга','книга') RETURNING id`, collID, archID).Scan(&bookID))
	store := kosync.NewStore(pool)
	const doc = "0123456789abcdef0123456789abcdef"
	require.NoError(t, store.RememberDocument(ctx, doc, bookID, "epub3"))

	srv := httptest.NewServer(api.NewRouter(api.Deps{
		Version: "test", DB: pool,
		Auth:   api.AuthDeps{Service: svc},
		Kosync: api.KosyncDeps{Handler: &kosync.Handler{Store: store}},
	}))
	t.Cleanup(srv.Close)
	call := func(method, path, user, k string, body any) (int, map[string]any) {
		var rd *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		} else {
			rd = bytes.NewReader(nil)
		}
		req, _ := http.NewRequestWithContext(ctx, method, srv.URL+path, rd)
		req.Header.Set("Accept", "application/vnd.koreader.v1+json")
		if user != "" {
			req.Header.Set("x-auth-user", user)
			req.Header.Set("x-auth-key", k)
		}
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	code, _ := call(http.MethodGet, "/kosync/users/auth", "", "", nil)
	require.Equal(t, http.StatusUnauthorized, code)
	code, _ = call(http.MethodGet, "/kosync/users/auth", email, auth.DeviceKey(password), nil)
	require.Equal(t, http.StatusUnauthorized, code, "основной пароль синхронизации не подходит")
	code, body := call(http.MethodGet, "/kosync/users/auth", email, key, nil)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "OK", body["authorized"])
	code, _ = call(http.MethodPost, "/kosync/users/create", "", "", map[string]string{"username": "x", "password": "y"})
	require.Equal(t, http.StatusForbidden, code, "регистрации нет")

	code, body = call(http.MethodGet, "/kosync/syncs/progress/"+doc, email, key, nil)
	require.Equal(t, http.StatusOK, code)
	require.Empty(t, body, "позиции ещё нет — пустой объект")

	code, body = call(http.MethodPut, "/kosync/syncs/progress", email, key, map[string]any{
		"document": doc, "progress": "/body/DocFragment[3]/body/p[7]/text().0", "percentage": 0.42,
		"device": "KOReader", "device_id": "D1",
	})
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, doc, body["document"])
	require.NotZero(t, body["timestamp"])

	code, body = call(http.MethodGet, "/kosync/syncs/progress/"+doc, email, key, nil)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "/body/DocFragment[3]/body/p[7]/text().0", body["progress"])
	require.InDelta(t, 0.42, body["percentage"], 0.0001)
	require.Equal(t, "KOReader", body["device"])

	var fraction float64
	var completed *time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT fraction, completed_at FROM reads WHERE user_id = $1 AND book_id = $2`,
		u.ID, bookID).Scan(&fraction, &completed))
	require.InDelta(t, 0.42, fraction, 0.0001, "доля прочитанного — в чтение")
	require.Nil(t, completed)

	code, _ = call(http.MethodPut, "/kosync/syncs/progress", email, key, map[string]any{
		"document": doc, "progress": "/body/DocFragment[40]", "percentage": 0.995, "device": "KOReader", "device_id": "D1",
	})
	require.Equal(t, http.StatusOK, code)
	require.NoError(t, pool.QueryRow(ctx, `SELECT completed_at FROM reads WHERE user_id = $1 AND book_id = $2`,
		u.ID, bookID).Scan(&completed))
	require.NotNil(t, completed, "дочитано — отмечено прочитанным")

	code, _ = call(http.MethodPut, "/kosync/syncs/progress", email, key, map[string]any{
		"document": "ffffffffffffffffffffffffffffffff", "progress": "x", "percentage": 0.1,
	})
	require.Equal(t, http.StatusOK, code, "книга не из каталога — позиция всё равно хранится")
	code, _ = call(http.MethodPut, "/kosync/syncs/progress", email, key, map[string]any{"document": doc, "percentage": 1.5})
	require.Equal(t, http.StatusBadRequest, code)
}
