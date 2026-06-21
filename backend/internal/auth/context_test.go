package auth

import (
	"context"
	"testing"
)

// TestContextWithUser_Roundtrip — нейтральные хелперы «пользователь в контексте»
// (их использует и api session-middleware, и opds Basic-auth).
func TestContextWithUser_Roundtrip(t *testing.T) {
	if _, ok := UserFromContext(context.Background()); ok {
		t.Fatal("пустой контекст не должен содержать пользователя")
	}
	want := User{ID: 7, Email: "a@e.com"}
	ctx := ContextWithUser(context.Background(), want)
	got, ok := UserFromContext(ctx)
	if !ok {
		t.Fatal("ожидали пользователя в контексте")
	}
	if got.ID != want.ID || got.Email != want.Email {
		t.Fatalf("roundtrip: got %+v, want %+v", got, want)
	}
}
