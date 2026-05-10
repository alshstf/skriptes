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

type Deps struct {
	Version string
	DB      *pgxpool.Pool
}

func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	// Liveness — процесс жив, можно ли его не убивать.
	r.Get("/healthz", healthz)
	// Readiness — процесс может обслуживать трафик (включая зависимости).
	r.Get("/readyz", readyz(d.DB))

	r.Route("/api", func(r chi.Router) {
		r.Get("/version", version(d.Version))
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
