package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/converter"
	"github.com/skriptes/skriptes/backend/internal/email"
	"github.com/skriptes/skriptes/backend/internal/history"
	"github.com/skriptes/skriptes/backend/internal/kindle"
)

// KindleDeps — зависимости send-to-kindle веба.
// Email может быть nil — endpoint вернёт 503; CRUD по target'ам
// всё равно доступен.
type KindleDeps struct {
	Service   *kindle.Service
	Email     *email.Sender
	Books     *books.Service       // для получения метаданных книги
	Converter *converter.Converter // fb2 → epub
	History   *history.Service     // фиксация приобретения (для запросов оценки)
}

// ── CRUD endpoints для /api/me/kindle-targets ───────────────────

func handleListKindleTargets(d KindleDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		items, err := d.Service.List(ctx, u.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
	}
}

type kindleTargetReq struct {
	Label string `json:"label"`
	Email string `json:"email"`
}

func handleAddKindleTarget(d KindleDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		var req kindleTargetReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		t, err := d.Service.Add(ctx, u.ID, req.Label, req.Email)
		switch {
		case errors.Is(err, kindle.ErrInvalidEmail):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid email"})
			return
		case errors.Is(err, kindle.ErrDuplicate):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "email already added"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
			return
		}
		writeJSON(w, http.StatusOK, t)
	}
}

func handleUpdateKindleTarget(d KindleDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		var req kindleTargetReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		t, err := d.Service.Update(ctx, u.ID, id, req.Label, req.Email)
		switch {
		case errors.Is(err, kindle.ErrNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "target not found"})
			return
		case errors.Is(err, kindle.ErrInvalidEmail):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid email"})
			return
		case errors.Is(err, kindle.ErrDuplicate):
			writeJSON(w, http.StatusConflict, map[string]string{"error": "email already added"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save failed"})
			return
		}
		writeJSON(w, http.StatusOK, t)
	}
}

func handleDeleteKindleTarget(d KindleDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		err = d.Service.Delete(ctx, u.ID, id)
		switch {
		case errors.Is(err, kindle.ErrNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "target not found"})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete failed"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ── Send-to-Kindle для конкретной книги ─────────────────────────

type sendToKindleReq struct {
	TargetID int64 `json:"target_id"`
}

// handleSendToKindle — POST /api/books/{id}/send-to-kindle.
//
// Контракт: body { target_id }. Возвращает 200 OK после успешной
// доставки SMTP-серверу (не "доставлено на устройство" — Amazon Kindle
// service асинхронный, обычно 1-5 минут от SMTP до устройства).
//
// 503 если SMTP не сконфигурирован, 404 если target не принадлежит
// пользователю, 502 если SMTP вернул ошибку.
func handleSendToKindle(d KindleDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.Email == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "smtp not configured"})
			return
		}
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
			return
		}
		bookID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || bookID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid book id"})
			return
		}
		var req sendToKindleReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.TargetID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
		defer cancel()

		target, err := d.Service.Get(ctx, u.ID, req.TargetID)
		if err != nil {
			if errors.Is(err, kindle.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "kindle target not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "lookup target"})
			return
		}

		book, err := d.Books.Get(ctx, bookID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "book not found"})
			return
		}

		res, err := d.Converter.Convert(ctx, converter.SourceBook{
			ID:       book.ID,
			Archive:  book.Archive,
			FileName: book.FileName,
			Ext:      book.Ext,
		}, converter.FormatEpub3)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "convert failed: " + err.Error()})
			return
		}

		// Открываем файл и отдаём в email.Sender — gomail копирует данные
		// в свой буфер, поэтому close после Send не критичен но аккуратнее.
		f, err := os.Open(res.Path) //nolint:gosec // res.Path — внутренний путь cache, не user input
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "open epub: " + err.Error()})
			return
		}
		defer func() { _ = f.Close() }()

		subject := "skriptes: " + book.Title
		body := fmt.Sprintf("Книга «%s» — отправлена через skriptes.\n", book.Title)
		err = d.Email.Send(target.Email, subject, body, &email.Attachment{
			Filename: res.Filename,
			Mime:     res.ContentType,
			Data:     f,
		})
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "send failed: " + err.Error()})
			return
		}

		// Отправка на Kindle = приобретение: через задержку книга попадёт в
		// запрос оценки (основной канал чтения у пользователя — Kindle).
		recordAcquisitionAsync(d.History, u.ID, bookID)

		writeJSON(w, http.StatusOK, map[string]string{
			"status": "sent",
			"to":     target.Email,
		})
	}
}
