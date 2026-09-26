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
// сервис, logout её удаляет; хэш Go совпадает с PG-выражением
// HashLegacySessionTokens (иначе перевод старых сессий разлогинил бы всех).
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
	require.Equal(t, auth.HashSessionToken(token), pgHash, "хэш Go должен совпадать с выражением HashLegacySessionTokens")

	u, ok := svc.UserByToken(ctx, token)
	require.True(t, ok)
	require.Equal(t, email, u.Email)
	_, ok = svc.UserByToken(ctx, auth.HashSessionToken(token))
	require.False(t, ok, "значение из БД не должно работать как токен")

	require.NoError(t, svc.Logout(ctx, token))
	_, ok = svc.UserByToken(ctx, token)
	require.False(t, ok)
}

// TestHashLegacySessionTokens — сессия, записанная до хэширования (сырой токен в
// sessions.token), после перевода работает по тому же cookie; повторный вызов
// ничего не трогает (идемпотентность — он гоняется на каждом старте).
func TestHashLegacySessionTokens(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startUserMgmtPostgres(t, ctx)
	svc := auth.New(pool, 4)

	u, err := svc.CreateUser(ctx, "legacy@example.com", "Legacy", "long-enough-password", auth.RoleUser)
	require.NoError(t, err)
	// Новая сессия (уже хэш) рядом со старой — её перевод трогать не должен.
	_, fresh, err := svc.Login(ctx, "legacy@example.com", "long-enough-password", auth.SessionMetadata{})
	require.NoError(t, err)

	const legacy = "Legacy_Raw-Token_43chars_base64url_AAAAAAAA"
	_, err = pool.Exec(ctx, `INSERT INTO sessions (token, user_id, expires_at) VALUES ($1, $2, now() + interval '1 day')`, legacy, u.ID)
	require.NoError(t, err)

	n, err := svc.HashLegacySessionTokens(ctx)
	require.NoError(t, err)
	require.Equal(t, int64(1), n, "переводится только сырая сессия")

	got, ok := svc.UserByToken(ctx, legacy)
	require.True(t, ok, "старый cookie продолжает работать")
	require.Equal(t, u.ID, got.ID)
	_, ok = svc.UserByToken(ctx, fresh)
	require.True(t, ok, "новая сессия не испорчена повторным хэшем")
	var raw int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE token = $1`, legacy).Scan(&raw))
	require.Zero(t, raw)

	n, err = svc.HashLegacySessionTokens(ctx)
	require.NoError(t, err)
	require.Zero(t, n, "второй проход — no-op")
}
