package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Deps собирает все зависимости HTTP-роутера.
// Auth (Service, AllowedOrigins, ...) — опционален: если Auth.Service == nil,
// auth-эндпоинты не монтируются и originCheck не применяется. Это полезно
// для unit-тестов простых ручек (/healthz и т.п.) без поднятия БД.
type Deps struct {
	Version  string
	DB       *pgxpool.Pool
	Auth     AuthDeps
	Books    BooksDeps
	Catalog  CatalogDeps
	Download DownloadDeps
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
				if d.Books.Service != nil {
					r.Get("/books", handleListBooks(d.Books))
					r.Get("/books/{id}", handleGetBook(d.Books))
				}
				if d.Download.Books != nil && d.Download.Converter != nil {
					r.Get("/books/{id}/download", handleDownload(d.Download))
				}
				if d.Catalog.Service != nil {
					r.Get("/authors/{id}", handleGetAuthor(d.Catalog))
					r.Get("/series/{id}", handleGetSeries(d.Catalog))
				}
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
