package api

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/skriptes/skriptes/backend/internal/catalog"
)

// handleListAuthors — GET /api/authors: постраничный список авторов с фильтрами
// и агрегатами (раздел «Авторы»). Отдельный маршрут от /api/authors/{id}
// (карточка одного автора) — chi различает их по наличию path-параметра.
//
// Фильтры приходят query-параметрами (см. catalog.AuthorListParams):
//
//	q, genres (CSV), langs (CSV, язык издания), src_langs (CSV, язык оригинала),
//	year_from/year_to, has_adaptations, min_rating, favorites_only, sort,
//	limit/offset.
//
// Видимость контента (admin ∪ персональные скрытые жанры/языки) применяется к
// агрегатам так же, как на карточке автора/серии — чтобы скрытый контент не
// «протекал» в счётчики/жанры/языки (граблю №14).
func handleListAuthors(d CatalogDeps, content ContentDeps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		params := catalog.AuthorListParams{
			Query:           q.Get("q"),
			Genres:          splitCSV(q.Get("genres")),
			Langs:           splitCSV(q.Get("langs")),
			SrcLangs:        splitCSV(q.Get("src_langs")),
			YearFrom:        parseIntOr(q.Get("year_from"), 0),
			YearTo:          parseIntOr(q.Get("year_to"), 0),
			HasAdaptations:  parseBool(q.Get("has_adaptations")),
			MinRating:       parseIntOr(q.Get("min_rating"), 0),
			MinReaderRating: parseFloatOr(q.Get("min_reader_rating"), 0),
			FavoritesOnly:   parseBool(q.Get("favorites_only")),
			Sort:            q.Get("sort"),
			Limit:           parseIntOr(q.Get("limit"), 50),
			Offset:          parseIntOr(q.Get("offset"), 0),
		}
		if u, ok := UserFromContext(r.Context()); ok {
			params.UserID = u.ID
		}
		if content.Resolver != nil {
			params.ExcludeGenres, params.ExcludeLangs = content.Resolver.Exclusions(r.Context(), params.UserID)
		}
		// 15с (не 5): список авторов считает агрегаты подзапросами, а на больших
		// библиотеках под нагрузкой фоновых воркеров запас нужен. Основной фикс —
		// двухфазный запрос в ListAuthorsFiltered (LIMIT до богатых подзапросов).
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		res, err := d.Service.ListAuthorsFiltered(ctx, params)
		if err != nil {
			// Логируем причину — раньше глоталась, и 500 «query failed» нельзя было
			// диагностировать по логам (таймаут sort=rating на большой библиотеке).
			slog.Error("list authors failed", "sort", params.Sort, "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

// parseBool — query-параметр-флаг: "1"/"true"/"yes"/"on" → true, иначе false.
// Свой хелпер (а не strconv.ParseBool) — терпимый к формам, которые шлёт фронт.
func parseBool(s string) bool {
	switch s {
	case "1", "true", "TRUE", "True", "yes", "on":
		return true
	default:
		return false
	}
}

// parseFloatOr — query-параметр-число с дефолтом (для min_reader_rating).
func parseFloatOr(s string, def float64) float64 {
	if s == "" {
		return def
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return def
	}
	return v
}
