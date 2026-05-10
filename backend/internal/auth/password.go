package auth

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// DefaultBcryptCost соответствует ~250–500 ms на современном CPU и
// принят как разумный baseline в 2026. Поднять можно через переменную
// окружения / конфиг, если оборудование позволяет.
const DefaultBcryptCost = 12

// ErrInvalidPassword возвращается при неуспешной верификации пароля
// (включая случай, когда пользователь не найден). Возвращаем единый код
// чтобы не палить enumeration: "user not found" и "wrong password" —
// два неотличимых ответа для атакующего.
var ErrInvalidPassword = errors.New("invalid email or password")

// HashPassword хэширует пароль bcrypt с заданным cost.
// Если cost == 0 — используется DefaultBcryptCost.
func HashPassword(plain string, cost int) (string, error) {
	if plain == "" {
		return "", errors.New("password must not be empty")
	}
	if cost == 0 {
		cost = DefaultBcryptCost
	}
	// bcrypt принимает максимум 72 байта; длиннее — обрезается тихо.
	// Это плохо: пользователь думает что у него длинный пароль, а на
	// самом деле учитываются первые 72. Лучше явно ограничить.
	if len(plain) > 72 {
		return "", errors.New("password too long (max 72 bytes)")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(plain), cost)
	if err != nil {
		return "", fmt.Errorf("bcrypt generate: %w", err)
	}
	return string(h), nil
}

// VerifyPassword сравнивает plain с bcrypt-хэшем за константное время.
// Возвращает ErrInvalidPassword при несовпадении (без раскрытия деталей).
// Любая другая ошибка (битый хэш) возвращается как есть — это уже
// инфраструктурная проблема.
func VerifyPassword(plain, hash string) error {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
	if err == nil {
		return nil
	}
	if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		return ErrInvalidPassword
	}
	return err
}
