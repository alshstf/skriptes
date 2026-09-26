package auth_test

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/auth"
	"github.com/stretchr/testify/require"
)

// TestSessionTokenStoredHashed — в sessions.token лежит только SHA-256 токена:
// сырой токен из cookie в БД не встречается, сессия по нему находится через
// сервис, logout её удаляет; хэш Go совпадает с PG-выражением миграции 0039
// (иначе миграция разлогинила бы всех или, хуже, оставила бы «мёртвые» строки).
func TestSessionTokenStoredHashed(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startUserMgmtPostgres(t, ctx)
	svc := auth.New(pool, 4)

	const email, pass = "hash@example.com", "long-enough-password"
	_, err := svc.CreateUser(ctx, email, "Hash", pass, auth.RoleUser)
	require.NoError(t, err)

	_, token, err := svc.Login(ctx, email, pass, auth.SessionMetadata{IP: netip.MustParseAddr("192.0.2.1")})
	require.NoError(t, err)
	require.NotEmpty(t, token)

	var raw, hashed int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE token = $1`, token).Scan(&raw))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE token = $1`, auth.HashSessionToken(token)).Scan(&hashed))
	require.Zero(t, raw, "сырой токен не должен храниться в БД")
	require.Equal(t, 1, hashed)

	var pgHash string
	require.NoError(t, pool.QueryRow(ctx, `SELECT encode(sha256(convert_to($1, 'UTF8')), 'hex')`, token).Scan(&pgHash))
	require.Equal(t, auth.HashSessionToken(token), pgHash, "хэш Go должен совпадать с выражением миграции 0039")

	u, ok := svc.UserByToken(ctx, token)
	require.True(t, ok)
	require.Equal(t, email, u.Email)
	_, ok = svc.UserByToken(ctx, auth.HashSessionToken(token))
	require.False(t, ok, "значение из БД не должно работать как токен")

	require.NoError(t, svc.Logout(ctx, token))
	_, ok = svc.UserByToken(ctx, token)
	require.False(t, ok)
}
