package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/auth"
	"github.com/skriptes/skriptes/backend/internal/db"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestUserManagement — интеграционный тест всех новых auth-методов.
// Один testcontainers-postgres + sequential проверки чтобы не платить
// двадцатикратно за startup кластера.
//
// Покрытие:
//   - CreateUser: happy, ErrPasswordTooShort, ErrEmailTaken
//   - ListUsers: количество, сортировка по created_at
//   - UpdateUser: display_name, email, role
//   - Last-admin защита: UpdateUser admin→user когда он один → ErrLastAdmin
//   - Last-admin защита: DeleteUser единственного admin'а → ErrLastAdmin
//   - ResetPassword: меняет пароль, инвалидирует ВСЕ сессии юзера
//   - ChangePassword: верифицирует current, keepSessionToken остаётся
//   - DeleteUser: hard delete + cascade на sessions
func TestUserManagement(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startUserMgmtPostgres(t, ctx)

	// bcryptCost=4 чтобы тест не висел секундами на каждый CreateUser.
	svc := auth.New(pool, 4)

	// ── CreateUser happy path ─────────────────────────────────────
	admin1, err := svc.CreateUser(ctx, "admin1@example.com", "Admin1", "password123", auth.RoleAdmin)
	require.NoError(t, err)
	require.Equal(t, "admin1@example.com", admin1.Email)
	require.Equal(t, auth.RoleAdmin, admin1.Role)

	// password too short → ErrPasswordTooShort
	_, err = svc.CreateUser(ctx, "short@example.com", "Short", "1234567", auth.RoleUser)
	require.ErrorIs(t, err, auth.ErrPasswordTooShort)

	// email taken → ErrEmailTaken
	_, err = svc.CreateUser(ctx, "admin1@example.com", "Dup", "anotherpass", auth.RoleUser)
	require.ErrorIs(t, err, auth.ErrEmailTaken)

	// Создаём ещё пару юзеров
	bob, err := svc.CreateUser(ctx, "bob@example.com", "Bob", "bobpass1234", auth.RoleUser)
	require.NoError(t, err)
	carol, err := svc.CreateUser(ctx, "carol@example.com", "Carol", "carolpass12", auth.RoleAdmin)
	require.NoError(t, err)

	// ── ListUsers ────────────────────────────────────────────────
	users, err := svc.ListUsers(ctx)
	require.NoError(t, err)
	require.Len(t, users, 3)
	// Сортировка по created_at — admin1 первый, потом bob, потом carol.
	require.Equal(t, admin1.ID, users[0].ID)
	require.Equal(t, bob.ID, users[1].ID)
	require.Equal(t, carol.ID, users[2].ID)

	// ── UpdateUser: display_name ──────────────────────────────────
	updated, err := svc.UpdateUser(ctx, bob.ID, "", "Bob Updated", "")
	require.NoError(t, err)
	require.Equal(t, "Bob Updated", updated.DisplayName)
	require.Equal(t, bob.Email, updated.Email)
	require.Equal(t, auth.RoleUser, updated.Role)

	// UpdateUser: email
	updated, err = svc.UpdateUser(ctx, bob.ID, "bob2@example.com", "", "")
	require.NoError(t, err)
	require.Equal(t, "bob2@example.com", updated.Email)

	// UpdateUser: email taken — должен дать ErrEmailTaken
	_, err = svc.UpdateUser(ctx, bob.ID, "carol@example.com", "", "")
	require.ErrorIs(t, err, auth.ErrEmailTaken)

	// UpdateUser: role admin → user (bob и так user, теперь меняем carol → user)
	// У нас два admin (admin1, carol) — это ОК, защита не сработает.
	updated, err = svc.UpdateUser(ctx, carol.ID, "", "", auth.RoleUser)
	require.NoError(t, err)
	require.Equal(t, auth.RoleUser, updated.Role)

	// ── Last-admin protection: UpdateUser ─────────────────────────
	// Теперь admin1 — единственный admin. Попытка деградировать его → ErrLastAdmin.
	_, err = svc.UpdateUser(ctx, admin1.ID, "", "", auth.RoleUser)
	require.ErrorIs(t, err, auth.ErrLastAdmin)

	// Проверяем что после ErrLastAdmin role НЕ поменялась (защита транзакционная).
	check, err := svc.GetUser(ctx, admin1.ID)
	require.NoError(t, err)
	require.Equal(t, auth.RoleAdmin, check.Role)

	// ── ResetPassword + session invalidation ─────────────────────
	// Создаём 3 сессии для bob (имитируем 3 устройства).
	for i := 0; i < 3; i++ {
		_, _, err := svc.Login(ctx, "bob2@example.com", "bobpass1234", auth.SessionMetadata{})
		require.NoError(t, err)
	}
	require.Equal(t, 3, countSessions(t, ctx, pool, bob.ID))

	// Admin ResetPassword → все 3 сессии должны исчезнуть.
	err = svc.ResetPassword(ctx, bob.ID, "newbobpass11")
	require.NoError(t, err)
	require.Equal(t, 0, countSessions(t, ctx, pool, bob.ID))

	// Старый пароль больше не работает.
	_, _, err = svc.Login(ctx, "bob2@example.com", "bobpass1234", auth.SessionMetadata{})
	require.ErrorIs(t, err, auth.ErrInvalidPassword)
	// Новый — работает.
	_, _, err = svc.Login(ctx, "bob2@example.com", "newbobpass11", auth.SessionMetadata{})
	require.NoError(t, err)

	// ResetPassword too short
	err = svc.ResetPassword(ctx, bob.ID, "short")
	require.ErrorIs(t, err, auth.ErrPasswordTooShort)

	// ── ChangePassword (self) + keepSessionToken ─────────────────
	// Логин bob 2 раза, второй токен — keepToken (текущая сессия).
	_, t1, err := svc.Login(ctx, "bob2@example.com", "newbobpass11", auth.SessionMetadata{})
	require.NoError(t, err)
	_, keepToken, err := svc.Login(ctx, "bob2@example.com", "newbobpass11", auth.SessionMetadata{})
	require.NoError(t, err)
	require.Equal(t, 3, countSessions(t, ctx, pool, bob.ID), "осталась 1 от выше + 2 свежих")

	// Wrong current password → ErrInvalidPassword
	err = svc.ChangePassword(ctx, bob.ID, "wrong", "anyvalidpass", keepToken)
	require.ErrorIs(t, err, auth.ErrInvalidPassword)
	require.Equal(t, 3, countSessions(t, ctx, pool, bob.ID), "при wrong current сессии не трогаем")

	// Right current password → success, остаётся только keepToken
	err = svc.ChangePassword(ctx, bob.ID, "newbobpass11", "bobpass2025", keepToken)
	require.NoError(t, err)
	require.Equal(t, 1, countSessions(t, ctx, pool, bob.ID))
	require.True(t, sessionExists(t, ctx, pool, keepToken), "keepToken должен сохраниться")
	require.False(t, sessionExists(t, ctx, pool, t1), "t1 должен быть revoke'нут")

	// ── DeleteUser ────────────────────────────────────────────────
	// Карола сейчас user, удалить можно.
	require.NoError(t, svc.DeleteUser(ctx, carol.ID))
	_, err = svc.GetUser(ctx, carol.ID)
	require.ErrorIs(t, err, auth.ErrUserNotFound)

	// ── Last-admin protection: DeleteUser ─────────────────────────
	err = svc.DeleteUser(ctx, admin1.ID)
	require.ErrorIs(t, err, auth.ErrLastAdmin)

	// Подтвердим что admin1 на месте.
	_, err = svc.GetUser(ctx, admin1.ID)
	require.NoError(t, err)
}

func countSessions(t *testing.T, ctx context.Context, pool *pgxpool.Pool, userID int64) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM sessions WHERE user_id = $1`, userID).Scan(&n))
	return n
}

func sessionExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, token string) bool {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT COUNT(*) FROM sessions WHERE token = $1`, token).Scan(&n))
	return n == 1
}

func startUserMgmtPostgres(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	pgC, err := postgres.Run(ctx,
		"postgres:17-alpine",
		postgres.WithDatabase("skriptes_test"),
		postgres.WithUsername("skriptes"),
		postgres.WithPassword("skriptes"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pgC.Terminate(context.Background()) })
	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, db.Migrate(dsn))
	pool, err := db.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}
