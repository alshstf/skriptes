package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/booktext"
	"github.com/skriptes/skriptes/backend/internal/converter"
	"github.com/skriptes/skriptes/backend/internal/llm"
	"github.com/skriptes/skriptes/backend/internal/readerai"
)

// AskDeps — ИИ-функции читалки (POST /api/books/{id}/ask); Engine == nil →
// ручка не монтируется.
type AskDeps struct {
	Engine *readerai.Engine
	// Begin — проверка ключа, выключателей, потолка и квоты (llm.Service.Begin).
	Begin func(ctx context.Context, userID int64) error
	// OpenBook — текст книги по разделам (тот же epub, что у ридера).
	OpenBook func(ctx context.Context, bookID int64) (*booktext.Book, error)
	// LibraryUnreadable — том с книгами не читается (libcheck); nil — не проверяем.
	LibraryUnreadable func() (bool, string)
}

type askRequest struct {
	Mode     string  `json:"mode"`
	Section  int     `json:"section"`
	Excerpt  string  `json:"excerpt"`
	Hint     float64 `json:"hint"`
	Question string  `json:"question"`
	Stage    int     `json:"stage"`
	Lang     string  `json:"lang"`
	Thread   []struct {
		Role string `json:"role"`
		Text string `json:"text"`
	} `json:"thread"`
}

// Тексты для читателя: почему ИИ сейчас недоступен и что пошло не так.
var aiUnavailableText = map[string]string{
	llm.ReasonNoKey:        "ИИ-функции на этом сервере не включены.",
	llm.ReasonDisabled:     "ИИ-функции выключены администратором.",
	llm.ReasonUserDisabled: "ИИ-функции выключены администратором.",
	llm.ReasonBudget:       "Месячный лимит расходов на ИИ исчерпан. Функции вернутся в начале следующего месяца.",
	llm.ReasonQuota:        "На сегодня вопросы закончились — завтра можно будет спросить снова.",
}

// askTimeout — срок ответа на вопрос читателя (меньше WriteTimeout сервера,
// 120 с); routeTimeout — общий таймаут маршрутов в NewRouter.
const (
	askTimeout   = 110 * time.Second
	routeTimeout = 60 * time.Second
)

const (
	msgAIRefused  = "Модель отказалась отвечать на этот вопрос."
	msgAIUpstream = "ИИ сейчас недоступен. Попробуйте чуть позже."
	msgAIBook     = "Не удалось прочитать текст книги."
)

// handleAsk — POST /api/books/{id}/ask: вопрос читателя по книге, ответ —
// потоком событий (text/event-stream, по JSON-событию readerai.Event в строке
// data; ошибка после начала потока — событие {"kind":"error","text":…}).
//
// До начала потока — обычные JSON-ошибки: 400 (плохой запрос), 403/429 (ИИ
// недоступен: выключен, потолок, квота — с reason и message), 503 (том с
// книгами не читается). Модели уходит только текст до места чтения.
func handleAsk(d AskDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		u, ok := UserFromContext(r.Context())
		if !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		var body askRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
			return
		}
		ask := readerai.Ask{
			BookID: id, UserID: u.ID, Mode: readerai.Mode(body.Mode),
			Pos:      readerai.Position{Section: body.Section, Excerpt: body.Excerpt, Hint: body.Hint},
			Question: body.Question, MinStage: body.Stage, Lang: body.Lang,
		}
		if !ask.Mode.Valid() {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown mode"})
			return
		}
		for _, t := range body.Thread {
			if t.Role != llm.RoleUser && t.Role != llm.RoleAssistant {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid thread"})
				return
			}
			ask.Thread = append(ask.Thread, llm.Message{Role: t.Role, Text: t.Text})
		}
		if d.LibraryUnreadable != nil {
			if bad, _ := d.LibraryUnreadable(); bad {
				writeBookFileError(w, http.StatusServiceUnavailable, "books volume unreadable", msgLibraryUnreadable)
				return
			}
		}
		// Первый вопрос по длинной книге строит пересказы её кусков и может идти
		// дольше общего таймаута маршрутов (60 с, middleware.Timeout) — у ручки свой
		// срок. Общий таймаут отменяет контекст запроса так же, как ушедший читатель,
		// поэтому уход читателя слушаем только до него.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), askTimeout)
		defer cancel()
		go func() {
			started := time.Now()
			select {
			case <-r.Context().Done():
				if time.Since(started) < routeTimeout-2*time.Second {
					cancel()
				}
			case <-ctx.Done():
			}
		}()
		if err := d.Begin(ctx, u.ID); err != nil {
			var un *llm.Unavailable
			if errors.As(err, &un) {
				status := http.StatusForbidden
				if un.Reason == llm.ReasonQuota {
					status = http.StatusTooManyRequests
				}
				writeJSON(w, status, map[string]string{"error": "ai unavailable", "reason": un.Reason, "message": aiUnavailableText[un.Reason]})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "ai check failed"})
			return
		}
		book, err := d.OpenBook(ctx, id)
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, books.ErrNotFound) || errors.Is(err, converter.ErrSourceMissing) {
				status = http.StatusNotFound
			}
			slog.Warn("ask: open book text failed", "book_id", id, "err", err)
			writeJSON(w, status, map[string]string{"error": "book text unavailable", "message": msgAIBook})
			return
		}

		flusher, _ := w.(http.Flusher)
		started := false
		send := func(v any) {
			if !started {
				// no-transform — чтобы прокси (Caddy encode) не сжимал и не
				// буферизовал поток.
				w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
				w.Header().Set("Cache-Control", "no-cache, no-transform")
				w.Header().Set("X-Accel-Buffering", "no")
				w.WriteHeader(http.StatusOK)
				started = true
			}
			raw, _ := json.Marshal(v)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
			if flusher != nil {
				flusher.Flush()
			}
		}
		err = d.Engine.Answer(ctx, book, ask, func(ev readerai.Event) { send(ev) })
		if err == nil {
			return
		}
		if errors.Is(err, readerai.ErrBadRequest) && !started {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad request"})
			return
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return // читатель закрыл шторку
		}
		text := msgAIUpstream
		switch {
		case errors.Is(err, llm.ErrRefused):
			text = msgAIRefused
		case errors.Is(err, llm.ErrAuth):
			slog.Error("ask: LLM API key rejected — check SKRIPTES_LLM_API_KEY", "err", err)
		default:
			slog.Warn("ask: answer failed", "book_id", id, "mode", body.Mode, "err", err)
		}
		send(map[string]string{"kind": "error", "text": text})
	}
}

// BookTextCache — текст недавно открытых книг: разбор epub на каждое обращение
// (до десятков мегабайт) был бы расточителен, а вопросы по одной книге идут
// сериями. Ключ — путь и время изменения epub.
type BookTextCache struct {
	mu      sync.Mutex
	entries map[string]bookTextEntry
	order   []string
	max     int
}

type bookTextEntry struct {
	mod  time.Time
	book *booktext.Book
}

// NewBookTextCache — кэш на max книг.
func NewBookTextCache(max int) *BookTextCache {
	return &BookTextCache{entries: map[string]bookTextEntry{}, max: max}
}

// Open — книга из кэша или с диска.
func (c *BookTextCache) Open(path string) (*booktext.Book, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if e, ok := c.entries[path]; ok && e.mod.Equal(st.ModTime()) {
		c.mu.Unlock()
		return e.book, nil
	}
	c.mu.Unlock()
	book, err := booktext.Open(path)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[path]; !ok {
		c.order = append(c.order, path)
		if len(c.order) > c.max {
			delete(c.entries, c.order[0])
			c.order = c.order[1:]
		}
	}
	c.entries[path] = bookTextEntry{mod: st.ModTime(), book: book}
	return book, nil
}

// OpenBookText — текст книги для ИИ-функций: тот же epub3, что получает ридер
// (конвертация при первом обращении, дальше — из кэша конвертера).
func OpenBookText(d DownloadDeps, cache *BookTextCache) func(ctx context.Context, bookID int64) (*booktext.Book, error) {
	return func(ctx context.Context, bookID int64) (*booktext.Book, error) {
		book, err := d.Books.Get(ctx, bookID)
		if err != nil {
			return nil, err
		}
		res, err := d.Converter.Convert(ctx, converter.SourceBook{ID: book.ID, Archive: book.Archive, FileName: book.FileName, Ext: book.Ext}, converter.FormatEpub3)
		if err != nil {
			return nil, err
		}
		return cache.Open(res.Path)
	}
}
