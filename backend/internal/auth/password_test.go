package auth_test

import (
	"strings"
	"testing"

	"github.com/skriptes/skriptes/backend/internal/auth"
	"github.com/stretchr/testify/require"
)

// minCost держит тесты быстрыми (~ms).
const testCost = 4

func TestHashAndVerify_RoundTrip(t *testing.T) {
	hash, err := auth.HashPassword("correct horse battery staple", testCost)
	require.NoError(t, err)
	require.NotEmpty(t, hash)
	require.NoError(t, auth.VerifyPassword("correct horse battery staple", hash))
}

func TestVerify_WrongPassword(t *testing.T) {
	hash, _ := auth.HashPassword("right", testCost)
	require.ErrorIs(t, auth.VerifyPassword("wrong", hash), auth.ErrInvalidPassword)
}

func TestHash_RejectsEmpty(t *testing.T) {
	_, err := auth.HashPassword("", testCost)
	require.Error(t, err)
}

func TestHash_RejectsTooLong(t *testing.T) {
	long := strings.Repeat("x", 73)
	_, err := auth.HashPassword(long, testCost)
	require.Error(t, err)
}

func TestHash_DifferentSaltsForSamePassword(t *testing.T) {
	h1, _ := auth.HashPassword("same", testCost)
	h2, _ := auth.HashPassword("same", testCost)
	require.NotEqual(t, h1, h2, "bcrypt должен использовать рандомные соли")
}
