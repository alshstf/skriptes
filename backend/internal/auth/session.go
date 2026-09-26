package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
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

// hashSessionToken — то, что лежит в sessions.token: hex(SHA-256) от токена из
// cookie. Сам токен в БД не хранится — утёкший дамп/бэкап не даёт войти под чужой
// сессией. Соль не нужна: токен — 256 бит случайности, не подбирается по словарю.
// Совпадает с PG `encode(sha256(convert_to(token,'UTF8')),'hex')` (HashLegacySessionTokens).
func hashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
