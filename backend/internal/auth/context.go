package auth

import "context"

// Нейтральные хелперы «пользователь в контексте». Живут в auth (а не в api),
// чтобы и api (session-middleware), и opds (Basic-auth) могли класть/читать
// пользователя без цикла импортов (api → opds).

type userCtxKey struct{}

// ContextWithUser кладёт пользователя в контекст запроса.
func ContextWithUser(ctx context.Context, u User) context.Context {
	return context.WithValue(ctx, userCtxKey{}, u)
}

// UserFromContext достаёт пользователя, положенного ContextWithUser (ok=false,
// если его там нет).
func UserFromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(userCtxKey{}).(User)
	return u, ok
}
