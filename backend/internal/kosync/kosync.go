// Package kosync — сервер синхронизации KOReader (#389, B4): протокол
// koreader-sync-server (KOReader, Readest). Вход — email и ключ пароля
// устройства (MD5, auth.ValidateDeviceKey), документ — частичный MD5 файла.
package kosync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/auth"
)

// readThreshold — с какой доли книга считается прочитанной (последняя страница
// у читалок даёт 0,99…1).
const readThreshold = 0.99

// Progress — позиция чтения в формате протокола.
type Progress struct {
	Document   string  `json:"document"`
	Progress   string  `json:"progress"`
	Percentage float64 `json:"percentage"`
	Device     string  `json:"device"`
	DeviceID   string  `json:"device_id"`
	Timestamp  int64   `json:"timestamp,omitempty"`
}

// Store — документы и позиции в базе.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore — хранилище синхронизации.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// RememberDocument — документ KOReader для отданного файла книги.
func (s *Store) RememberDocument(ctx context.Context, document string, bookID int64, format string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO book_documents (document, book_id, format) VALUES ($1, $2, $3)
		ON CONFLICT (document) DO UPDATE SET book_id = EXCLUDED.book_id, format = EXCLUDED.format
		WHERE book_documents.book_id IS DISTINCT FROM EXCLUDED.book_id OR book_documents.format IS DISTINCT FROM EXCLUDED.format`,
		document, bookID, format)
	if err != nil {
		return fmt.Errorf("remember document: %w", err)
	}
	return nil
}

// RememberAsync — то же в фоне (запись не должна задерживать отдачу файла).
func (s *Store) RememberAsync(document string, bookID int64, format string, logger *slog.Logger) {
	if s == nil || document == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.RememberDocument(ctx, document, bookID, format); err != nil && logger != nil {
			logger.Warn("kosync: remember document failed", "book_id", bookID, "err", err)
		}
	}()
}

// SaveProgress сохраняет позицию; если документ — книга каталога, долю
// прочитанного переносит в чтение (карточка, «Продолжить чтение»), а с
// readThreshold отмечает прочитанной. Позицию веб-ридера (last_pos) не трогает.
func (s *Store) SaveProgress(ctx context.Context, userID int64, p Progress) (time.Time, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var at time.Time
	if err := tx.QueryRow(ctx, `
		INSERT INTO kosync_progress (user_id, document, progress, percentage, device, device_id, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (user_id, document) DO UPDATE SET
			progress = EXCLUDED.progress, percentage = EXCLUDED.percentage,
			device = EXCLUDED.device, device_id = EXCLUDED.device_id, updated_at = now()
		RETURNING updated_at`, userID, p.Document, p.Progress, p.Percentage, p.Device, p.DeviceID).Scan(&at); err != nil {
		return time.Time{}, fmt.Errorf("save progress: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO reads (user_id, book_id, fraction, completed_at, updated_at)
		SELECT $1, d.book_id, $3::real, CASE WHEN $3 >= $4 THEN now() END, now()
		FROM book_documents d JOIN books b ON b.id = d.book_id
		WHERE d.document = $2
		ON CONFLICT (user_id, book_id) DO UPDATE SET
			fraction = EXCLUDED.fraction,
			completed_at = COALESCE(reads.completed_at, EXCLUDED.completed_at),
			updated_at = now()`, userID, p.Document, p.Percentage, readThreshold); err != nil {
		return time.Time{}, fmt.Errorf("save reading: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return time.Time{}, err
	}
	return at, nil
}

// GetProgress — последняя позиция пользователя в документе.
func (s *Store) GetProgress(ctx context.Context, userID int64, document string) (Progress, bool, error) {
	p := Progress{Document: document}
	var at time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT progress, percentage, device, device_id, updated_at
		FROM kosync_progress WHERE user_id = $1 AND document = $2`, userID, document).
		Scan(&p.Progress, &p.Percentage, &p.Device, &p.DeviceID, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return Progress{}, false, nil
	}
	if err != nil {
		return Progress{}, false, fmt.Errorf("get progress: %w", err)
	}
	p.Timestamp = at.Unix()
	return p, true, nil
}

// ── HTTP ─────────────────────────────────────────────────────────

// Handler — ручки протокола; вход проверяет middleware в api (пользователь —
// в auth-контексте).
type Handler struct {
	Store  *Store
	Logger *slog.Logger
}

// mimeJSON — тип ответов koreader-sync-server.
const mimeJSON = "application/json"

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", mimeJSON)
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// Register — POST /users/create: регистрации нет, вход — паролем устройства.
func (h *Handler) Register(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusForbidden, map[string]any{
		"code":    2005,
		"message": "Регистрации нет: создайте пароль устройства в профиле skriptes и войдите с ним (логин — email)",
	})
}

// Authorize — GET /users/auth: вход уже проверен.
func (h *Handler) Authorize(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"authorized": "OK"})
}

// maxField — потолок длины строковых полей позиции.
const maxField = 512

// UpdateProgress — PUT /syncs/progress.
func (h *Handler) UpdateProgress(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"code": 2001, "message": "Unauthorized"})
		return
	}
	var p Progress
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&p); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": 2003, "message": "Invalid request"})
		return
	}
	p.Document = strings.ToLower(strings.TrimSpace(p.Document))
	if p.Document == "" || len(p.Document) > 64 || len(p.Progress) > maxField || len(p.Device) > maxField ||
		len(p.DeviceID) > maxField || p.Percentage < 0 || p.Percentage > 1 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"code": 2003, "message": "Invalid request"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	at, err := h.Store.SaveProgress(ctx, u.ID, p)
	if err != nil {
		h.Logger.Error("kosync: save progress failed", "user_id", u.ID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": 2000, "message": "Unknown server error"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"document": p.Document, "timestamp": at.Unix()})
}

// GetProgress — GET /syncs/progress/{document}; нет позиции — пустой объект.
func (h *Handler) GetProgress(w http.ResponseWriter, r *http.Request) {
	u, ok := auth.UserFromContext(r.Context())
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"code": 2001, "message": "Unauthorized"})
		return
	}
	doc := strings.ToLower(strings.TrimSpace(chi.URLParam(r, "document")))
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	p, found, err := h.Store.GetProgress(ctx, u.ID, doc)
	if err != nil {
		h.Logger.Error("kosync: get progress failed", "user_id", u.ID, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"code": 2000, "message": "Unknown server error"})
		return
	}
	if !found {
		writeJSON(w, http.StatusOK, map[string]any{})
		return
	}
	writeJSON(w, http.StatusOK, p)
}
