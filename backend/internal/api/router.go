package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/opds"
)

// Deps собирает все зависимости HTTP-роутера.
// Auth (Service, AllowedOrigins, ...) — опционален: если Auth.Service == nil,
// auth-эндпоинты не монтируются и originCheck не применяется. Это полезно
// для unit-тестов простых ручек (/healthz и т.п.) без поднятия БД.
type Deps struct {
	Version     string
	DB          *pgxpool.Pool
	Auth        AuthDeps
	Books       BooksDeps
	Catalog     CatalogDeps
	Download    DownloadDeps
	History     HistoryDeps
	Metadata    MetadataDeps
	Kindle      KindleDeps
	Adaptations AdaptationsDeps
	// OPDS — опционально. Если Handler == nil, /opds/* не монтируется.
	// BaseURL прокидывается извне (cfg.AllowedOrigins[0] обычно).
	OPDS OPDSDeps
}

// OPDSDeps — handler уже сконфигурен в main.go (там удобнее всех
// зависимостей собрать); сюда передаётся готовый объект для wiring'а.
type OPDSDeps struct {
	Handler *opds.Handler
}

func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	if d.Auth.Service != nil && len(d.Auth.AllowedOrigins) > 0 {
		r.Use(originCheck(d.Auth.AllowedOrigins))
	}

	// Liveness — процесс жив, можно ли его не убивать.
	r.Get("/healthz", healthz)
	// Readiness — процесс может обслуживать трафик (включая зависимости).
	r.Get("/readyz", readyz(d.DB))

	// OPDS-каталог для e-reader приложений (KOReader/Moon+Reader/...).
	// Отдельная mount-точка от /api: своя авторизация (HTTP Basic
	// вместо session cookie + CSRF), отдельный media-type — другие
	// клиенты, другая лента эндпоинтов. Монтируется только если
	// сконфигурен Handler И есть Auth.Service (нужен ValidateCredentials).
	if d.OPDS.Handler != nil && d.Auth.Service != nil {
		r.Route("/opds", func(r chi.Router) {
			r.Use(requireBasicAuth(d.Auth))
			h := d.OPDS.Handler
			r.Get("/", h.Root)
			r.Get("/opensearch.xml", h.OpenSearchDescription)
			r.Get("/recent", h.Recent)
			r.Get("/search", h.Search)
			r.Get("/authors", h.AuthorsList)
			r.Get("/authors/{id}", h.AuthorBooks)
			r.Get("/series", h.SeriesList)
			r.Get("/series/{id}", h.SeriesBooks)
			r.Get("/genres", h.GenresList)
			r.Get("/genres/{id}", h.GenreBooks)
			r.Get("/books/{id}/download", h.Download)
			r.Get("/covers/{name}", h.Cover)
		})
	}

	r.Route("/api", func(r chi.Router) {
		r.Get("/version", version(d.Version))
		if d.Auth.Service != nil {
			// Публичные auth-эндпоинты.
			r.Post("/auth/login", handleLogin(d.Auth))
			r.Post("/auth/logout", handleLogout(d.Auth))
			// Защищённые: требуют валидной session-cookie.
			r.Group(func(r chi.Router) {
				r.Use(requireAuth(d.Auth))
				r.Get("/auth/me", handleMe(d.Auth))
				// Self-management. /api/me — обновление своего профиля
				// (display_name, email), /api/me/password — смена своего
				// пароля с верификацией текущего.
				r.Patch("/me", handleUpdateMe(d.Auth))
				r.Patch("/me/password", handleChangeMyPassword(d.Auth))
				if d.Books.Service != nil {
					r.Get("/books", handleListBooks(d.Books))
					r.Get("/books/{id}", handleGetBook(d.Books, d.History, d.Metadata))
				}
				if d.Adaptations.Service != nil {
					r.Get("/books/{id}/adaptations", handleListAdaptations(d.Adaptations, d.Books, d.Metadata))
				}
				if d.Metadata.Service != nil {
					r.Get("/covers/{name}", handleCover(d.Metadata))
				}
				if d.Books.Service != nil || d.Catalog.Service != nil {
					r.Get("/search/suggest", handleSuggest(d.Books, d.Catalog, d.History))
				}
				if d.Download.Books != nil && d.Download.Converter != nil {
					r.Get("/books/{id}/download", handleDownload(d.Download, d.History))
				}
				if d.Catalog.Service != nil {
					r.Get("/authors/{id}", handleGetAuthor(d.Catalog, d.History, d.Metadata))
					r.Get("/series/{id}", handleGetSeries(d.Catalog, d.History))
				}
				if d.History.Service != nil {
					r.Post("/books/{id}/favorite", handleAddFavorite(d.History))
					r.Delete("/books/{id}/favorite", handleRemoveFavorite(d.History))
					r.Post("/authors/{id}/favorite", handleAddFavoriteAuthor(d.History))
					r.Delete("/authors/{id}/favorite", handleRemoveFavoriteAuthor(d.History))
					r.Post("/series/{id}/favorite", handleAddFavoriteSeries(d.History))
					r.Delete("/series/{id}/favorite", handleRemoveFavoriteSeries(d.History))
					r.Get("/me/favorites", handleListFavorites(d.History))
					r.Get("/me/recent", handleRecentViews(d.History))
					// Явная отметка «прочитано» (кнопка на карточке книги +
					// auto-mark из ридера при дочитывании). Основной сигнал
					// для read_count в статистике автора/серии.
					r.Post("/books/{id}/read", handleMarkRead(d.History))
					r.Delete("/books/{id}/read", handleUnmarkRead(d.History))
					// Позиция чтения для in-browser ридера (epub-cfi).
					r.Get("/books/{id}/position", handleGetPosition(d.History))
					r.Put("/books/{id}/position", handleSavePosition(d.History))
				}
				// In-browser ридер: тот же путь конвертации что и /download,
				// но без Content-Disposition: attachment и без записи в reads.
				if d.Download.Books != nil && d.Download.Converter != nil {
					r.Get("/books/{id}/epub", handleEpub(d.Download))
				}
				if d.Kindle.Service != nil {
					r.Get("/me/kindle-targets", handleListKindleTargets(d.Kindle))
					r.Post("/me/kindle-targets", handleAddKindleTarget(d.Kindle))
					r.Patch("/me/kindle-targets/{id}", handleUpdateKindleTarget(d.Kindle))
					r.Delete("/me/kindle-targets/{id}", handleDeleteKindleTarget(d.Kindle))
					if d.Kindle.Books != nil && d.Kindle.Converter != nil {
						r.Post("/books/{id}/send-to-kindle", handleSendToKindle(d.Kindle))
					}
				}
			})

			// Admin-only group: те же session-cookie + role=admin check.
			// Отдельный r.Group чтобы requireAdmin не дублировался на user-роутах.
			r.Group(func(r chi.Router) {
				r.Use(requireAdmin(d.Auth))
				r.Get("/admin/users", handleListUsers(d.Auth))
				r.Post("/admin/users", handleCreateUser(d.Auth))
				r.Patch("/admin/users/{id}", handleUpdateUser(d.Auth))
				r.Patch("/admin/users/{id}/password", handleResetPassword(d.Auth))
				r.Delete("/admin/users/{id}", handleDeleteUser(d.Auth))
			})
		}
	})

	return r
}

func healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func readyz(pool *pgxpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if pool == nil {
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "db": "disabled"})
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.Ping(ctx); err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "db": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "db": "ok"})
	}
}

func version(v string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"version": v})
	}
}

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
