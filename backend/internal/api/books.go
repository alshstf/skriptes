package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/skriptes/skriptes/backend/internal/books"
)

// BooksDeps — зависимости для эндпоинтов /api/books*.
// Service может быть nil — тогда эндпоинты не монтируются.
type BooksDeps struct {
	Service *books.Service
}

func handleListBooks(d BooksDeps, content ContentDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		params := books.ListParams{
			Query:    q.Get("q"),
			Limit:    parseIntOr(q.Get("limit"), 20),
			Offset:   parseIntOr(q.Get("offset"), 0),
			Genres:   splitCSV(q.Get("genres")),
			Lang:     q.Get("lang"),
			YearFrom: parseIntOr(q.Get("year_from"), 0),
			YearTo:   parseIntOr(q.Get("year_to"), 0),
			SeriesID: parseInt64Or(q.Get("series_id"), 0),
			AuthorID: parseInt64Or(q.Get("author_id"), 0),
			Sort:     q.Get("sort"),
			Facets:   splitCSV(q.Get("facets")),
		}
		// Передаём UserID — books.List сам решает, применять ли re-ranking
		// (см. условия там: offset==0, нет явного Sort и нет фильтра по
		// одному автору/серии).
		var userID int64
		if u, ok := UserFromContext(r.Context()); ok {
			params.UserID = u.ID
			userID = u.ID
		}
		// Скрытые из выдачи жанры/языки (admin ∪ персональные).
		if content.Resolver != nil {
			params.ExcludeGenres, params.ExcludeLangs = content.Resolver.Exclusions(r.Context(), userID)
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		// Веб-список ищет по индексу works (фасеты считают РАБОТЫ, id = works.id,
		// ссылки фронта ведут на /works/{id}). OPDS остаётся на List (издания).
		res, err := d.Service.ListWorks(ctx, params)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "search failed"})
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

// bookResponse — Book + user-specific поля. Книги в books-пакете не
// знают про пользователя; user-зависимые поля дорисовываем здесь:
//   - is_favorite — лежит ли в favorites
//   - is_read — есть ли запись в reads с completed_at IS NOT NULL
//   - read_at — когда пометили прочитанной (для отображения даты в UI);
//     nil если is_read=false
//   - reading_fraction — прогресс чтения [0,1] из in-browser ридера;
//     nil если ридер ни разу не открывали (UI тогда показывает «Читать»
//     без процента вместо «Продолжить N%»)
type bookResponse struct {
	books.Book
	IsFavorite      bool       `json:"is_favorite"`
	IsRead          bool       `json:"is_read"`
	ReadAt          *time.Time `json:"read_at,omitempty"`
	ReadingFraction *float64   `json:"reading_fraction,omitempty"`
	// Пользовательские оценки (work-level), ОТДЕЛЬНО от Book.Rating (LIBRATE):
	//   - user_rating — оценка текущего юзера (nil если не оценивал/гость);
	//   - rating_avg / rating_count — средняя по инстансу и число голосов
	//     (rating_avg nil, count 0 — оценок ещё нет).
	UserRating  *int     `json:"user_rating,omitempty"`
	RatingAvg   *float64 `json:"rating_avg,omitempty"`
	RatingCount int      `json:"rating_count"`
}

func handleGetBook(d BooksDeps, hist HistoryDeps, meta MetadataDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		b, err := d.Service.Get(ctx, id)
		if err != nil {
			if errors.Is(err, books.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "book not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeBookCard(w, r, ctx, b, hist, meta)
	}
}

// handleGetWork — GET /api/works/{id}: карточка логической книги по works.id.
// Работа видна, если у неё есть издание, не скрытое жанром/языком (иначе 404).
// Возвращает тот же DTO, что /api/books/{id} (top-level = представительное
// издание); фронт переиспользует BookDetailPage.
func handleGetWork(d BooksDeps, hist HistoryDeps, meta MetadataDeps, content ContentDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		idStr := chi.URLParam(r, "id")
		workID, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil || workID <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid id"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		var exGenres, exLangs []string
		if content.Resolver != nil {
			var userID int64
			if u, ok := UserFromContext(r.Context()); ok {
				userID = u.ID
			}
			exGenres, exLangs = content.Resolver.Exclusions(r.Context(), userID)
		}
		b, err := d.Service.GetWork(ctx, workID, exGenres, exLangs)
		if err != nil {
			if errors.Is(err, books.ErrNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "work not found"})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeBookCard(w, r, ctx, b, hist, meta)
	}
}

// writeBookCard дорисовывает user-зависимые поля к карточке (общее для
// /api/books/{id} и /api/works/{id}) и отдаёт JSON. b.ID — открытое/
// представительное издание (для fraction/view/скачивания), b.WorkID — работа
// (для work-level is_favorite/is_read и прогресса по изданиям).
func writeBookCard(w http.ResponseWriter, r *http.Request, ctx context.Context, b books.Book, hist HistoryDeps, meta MetadataDeps) {
	var isFav, isRead bool
	var readAt *time.Time
	var fraction *float64
	if u, ok := UserFromContext(r.Context()); ok && hist.Service != nil {
		// fraction («продолжить N%») — прогресс ОТКРЫТОГО издания: привязан к
		// конкретному файлу (CFI), агрегировать по работе нельзя. Дефолты
		// is_read/read_at тоже отсюда (на случай, если work_id неизвестен).
		if rd, ca, fr, err := hist.Service.ReadStatus(ctx, u.ID, b.ID); err == nil {
			isRead = rd
			readAt = ca
			fraction = fr
		}
		// is_favorite / is_read — на уровне КНИГИ (любое издание избрано/
		// прочитано ⇒ книга избрана/прочитана). Для singleton-работы == по изданию.
		if b.WorkID > 0 {
			if v, err := hist.Service.IsWorkFavorite(ctx, u.ID, b.WorkID); err == nil {
				isFav = v
			}
			if rd, ca, err := hist.Service.WorkReadStatus(ctx, u.ID, b.WorkID); err == nil {
				isRead = rd
				readAt = ca
			}
		} else if v, err := hist.Service.IsFavorite(ctx, u.ID, b.ID); err == nil {
			isFav = v
		}
		// Прогресс/«прочитано» на КАЖДОЕ издание (для секции «Издания»).
		if b.WorkID > 0 && len(b.Editions) > 0 {
			if reads, err := hist.Service.WorkEditionReads(ctx, u.ID, b.WorkID); err == nil {
				for i := range b.Editions {
					if er, ok := reads[b.Editions[i].ID]; ok {
						b.Editions[i].ReadingFraction = er.Fraction
						b.Editions[i].IsRead = er.Completed
					}
				}
			}
		}
		recordViewAsync(hist.Service, u.ID, b.ID)
	}

	// Оценки читателей (work-level): средняя по инстансу — всегда (даже гостю),
	// оценка текущего юзера — если залогинен. Отдельно от Book.Rating (LIBRATE).
	var userRating *int
	var ratingAvg *float64
	var ratingCount int
	if hist.Service != nil && b.WorkID > 0 {
		if avg, cnt, err := hist.Service.WorkRatingAggregate(ctx, b.WorkID); err == nil && cnt > 0 {
			ratingAvg = &avg
			ratingCount = cnt
		}
		if u, ok := UserFromContext(r.Context()); ok {
			if rt, has, err := hist.Service.UserRating(ctx, u.ID, b.WorkID); err == nil && has {
				userRating = &rt
			}
		}
	}

	// Lazy enrichment: если у книги нет обложки, в фоне сходим в провайдеры.
	triggerBookEnrichmentAsync(meta, b)

	writeJSON(w, http.StatusOK, bookResponse{
		Book:            b,
		IsFavorite:      isFav,
		IsRead:          isRead,
		ReadAt:          readAt,
		ReadingFraction: fraction,
		UserRating:      userRating,
		RatingAvg:       ratingAvg,
		RatingCount:     ratingCount,
	})
}

func parseIntOr(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

func parseInt64Or(s string, def int64) int64 {
	if s == "" {
		return def
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// splitCSV — разбивает значение query-параметра вида "a,b,c" в []string,
// пропуская пустые элементы. Пустая или отсутствующая строка → nil.
func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
