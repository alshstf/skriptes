package auth

import (
	"context"
	"crypto/md5" // #nosec G501 -- протокол KOReader-синхронизации: клиент присылает MD5 пароля
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Пароли устройств (#389, B2): читалки входят по OPDS и синхронизации без
// основного пароля. Пароль показывается один раз при создании; в базе —
// только проверочное значение (devicePasswordVerifier).

// Device — пароль устройства в профиле (без самого пароля).
type Device struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
}

// ErrDeviceNotFound — нет такого пароля устройства у пользователя.
var ErrDeviceNotFound = errors.New("device not found")

const (
	// devicePasswordLen — символов в пароле устройства: 16 из 32 — 80 бит.
	devicePasswordLen = 16
	// devicePasswordAlphabet — строчные буквы и цифры без похожих (0/o, 1/l/i):
	// пароль набирают на клавиатуре читалки.
	devicePasswordAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	// maxDeviceNameLen — потолок длины названия устройства.
	maxDeviceNameLen = 64
	// MaxDevicesPerUser — сколько паролей устройств может быть у пользователя.
	MaxDevicesPerUser = 20
)

// ErrTooManyDevices — у пользователя уже MaxDevicesPerUser паролей.
var ErrTooManyDevices = errors.New("too many device passwords")

// generateDevicePassword — случайный пароль из devicePasswordAlphabet.
func generateDevicePassword() (string, error) {
	var b strings.Builder
	max := big.NewInt(int64(len(devicePasswordAlphabet)))
	for i := 0; i < devicePasswordLen; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("read random: %w", err)
		}
		b.WriteByte(devicePasswordAlphabet[n.Int64()])
	}
	return b.String(), nil
}

// NormalizeDevicePassword — пароль, как его ввели: без пробелов и дефисов
// (профиль показывает его группами), в нижнем регистре.
func NormalizeDevicePassword(p string) string {
	return strings.ToLower(strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(p)))
}

// DeviceKey — hex(MD5(пароль)): так пароль присылает KOReader-синхронизация.
func DeviceKey(password string) string {
	sum := md5.Sum([]byte(password)) // #nosec G401 -- см. импорт
	return hex.EncodeToString(sum[:])
}

// deviceVerifier — то, что хранится в device_passwords.verifier: SHA-256 от
// DeviceKey. Одна проверка для Basic-входа (пароль → ключ → SHA-256) и для
// синхронизации (ключ → SHA-256).
func deviceVerifier(key string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(key)))
	return hex.EncodeToString(sum[:])
}

// CreateDevice заводит пароль устройства; пароль возвращается только здесь.
func (s *Service) CreateDevice(ctx context.Context, userID int64, name string) (Device, string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Устройство"
	}
	if r := []rune(name); len(r) > maxDeviceNameLen {
		name = string(r[:maxDeviceNameLen])
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM device_passwords WHERE user_id = $1`, userID).Scan(&n); err != nil {
		return Device{}, "", fmt.Errorf("count devices: %w", err)
	}
	if n >= MaxDevicesPerUser {
		return Device{}, "", ErrTooManyDevices
	}
	password, err := generateDevicePassword()
	if err != nil {
		return Device{}, "", err
	}
	d := Device{Name: name}
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO device_passwords (user_id, name, verifier) VALUES ($1, $2, $3)
		RETURNING id, created_at`, userID, name, deviceVerifier(DeviceKey(password))).Scan(&d.ID, &d.CreatedAt); err != nil {
		return Device{}, "", fmt.Errorf("insert device: %w", err)
	}
	return d, password, nil
}

// ListDevices — пароли устройств пользователя, новые сверху.
func (s *Service) ListDevices(ctx context.Context, userID int64) ([]Device, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, created_at, last_used_at FROM device_passwords
		WHERE user_id = $1 ORDER BY created_at DESC, id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.ID, &d.Name, &d.CreatedAt, &d.LastUsedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteDevice отзывает пароль устройства.
func (s *Service) DeleteDevice(ctx context.Context, userID, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM device_passwords WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete device: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrDeviceNotFound
	}
	return nil
}

// ValidateDeviceKey — пользователь по email и ключу устройства (DeviceKey).
// Несовпадение — ErrInvalidPassword. Отмечает время последнего входа.
func (s *Service) ValidateDeviceKey(ctx context.Context, email, key string) (User, error) {
	var (
		u        User
		deviceID int64
	)
	err := s.pool.QueryRow(ctx, `
		SELECT u.id, u.email, u.display_name, u.role, COALESCE(u.kindle_email::text, ''), u.created_at, d.id
		FROM device_passwords d JOIN users u ON u.id = d.user_id
		WHERE d.verifier = $1 AND u.email = $2`, deviceVerifier(key), email).
		Scan(&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.KindleEmail, &u.CreatedAt, &deviceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrInvalidPassword
	}
	if err != nil {
		return User{}, fmt.Errorf("validate device: %w", err)
	}
	// Время последнего использования — не чаще раза в минуту на пароль.
	_, _ = s.pool.Exec(ctx, `UPDATE device_passwords SET last_used_at = now()
		WHERE id = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute')`, deviceID)
	return u, nil
}

// ValidateDevicePassword — то же по паролю устройства (Basic-вход OPDS).
func (s *Service) ValidateDevicePassword(ctx context.Context, email, password string) (User, error) {
	return s.ValidateDeviceKey(ctx, email, DeviceKey(NormalizeDevicePassword(password)))
}
