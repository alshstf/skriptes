package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

// SessionTokenBytes — длина непрозрачного токена в байтах.
// 32 байта = 256 бит энтропии, что больше чем достаточно от brute-force
// (даже при идеальном PRNG в МНК-схеме у атакующего ~2^128 операций).
const SessionTokenBytes = 32

// generateSessionToken возвращает url-safe base64 строку длиной ~43 char
// (без padding). Использует crypto/rand — для cookie-based сессий это
// единственный приемлемый источник случайности.
func generateSessionToken() (string, error) {
	b := make([]byte, SessionTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
