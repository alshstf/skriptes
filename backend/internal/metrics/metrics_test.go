package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestHTTPMiddleware_LabelsByRoutePattern(t *testing.T) {
	r := chi.NewRouter()
	r.Use(HTTPMiddleware)
	r.Route("/api", func(r chi.Router) {
		r.Get("/books/{id}", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		})
		r.Get("/ok", func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "ok") // без явного WriteHeader — 200
		})
	})

	byID := httpRequests.WithLabelValues("GET", "/api/books/{id}", "404")
	ok := httpRequests.WithLabelValues("GET", "/api/ok", "200")
	unmatched := httpRequests.WithLabelValues("GET", "unmatched", "404")
	other := httpRequests.WithLabelValues("OTHER", "unmatched", "405")
	before := []float64{testutil.ToFloat64(byID), testutil.ToFloat64(ok), testutil.ToFloat64(unmatched), testutil.ToFloat64(other)}

	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/books/42", nil),
		httptest.NewRequest(http.MethodGet, "/api/books/43", nil),
		httptest.NewRequest(http.MethodGet, "/api/ok", nil),
		httptest.NewRequest(http.MethodGet, "/nope", nil),
		httptest.NewRequest("BREW", "/api/ok", nil),
	} {
		r.ServeHTTP(httptest.NewRecorder(), req)
	}

	// Две книги — одна метка шаблона, а не по метке на id.
	if got := testutil.ToFloat64(byID) - before[0]; got != 2 {
		t.Errorf("/api/books/{id} 404: %v, want 2", got)
	}
	if got := testutil.ToFloat64(ok) - before[1]; got != 1 {
		t.Errorf("/api/ok 200: %v, want 1", got)
	}
	if got := testutil.ToFloat64(unmatched) - before[2]; got != 1 {
		t.Errorf("unmatched 404: %v, want 1", got)
	}
	// Нестандартный метод не плодит метки: OTHER.
	if got := testutil.ToFloat64(other) - before[3]; got != 1 {
		t.Errorf("OTHER 405: %v, want 1", got)
	}
}

func TestImportFinished_LastValuesOnlyOnSuccess(t *testing.T) {
	ImportStarted()
	if got := testutil.ToFloat64(importInProgress); got != 1 {
		t.Fatalf("in_progress = %v, want 1", got)
	}
	ImportFinished("ok", ImportResult{Records: 556438, BooksInserted: 3970, RecordErrors: 1, Duration: 53 * time.Minute})
	if got := testutil.ToFloat64(importInProgress); got != 0 {
		t.Errorf("in_progress после импорта = %v, want 0", got)
	}
	if got := testutil.ToFloat64(importLastRecords); got != 556438 {
		t.Errorf("last_records = %v", got)
	}
	okAt := testutil.ToFloat64(importLastSuccess)
	if okAt == 0 {
		t.Fatal("last_success не выставлен")
	}

	failedBefore := testutil.ToFloat64(importRuns.WithLabelValues("failed"))
	ImportStarted()
	ImportFinished("failed", ImportResult{})
	if got := testutil.ToFloat64(importRuns.WithLabelValues("failed")) - failedBefore; got != 1 {
		t.Errorf("runs{failed} += %v, want 1", got)
	}
	// Неудача не трогает сведения об удачном импорте.
	if got := testutil.ToFloat64(importLastRecords); got != 556438 {
		t.Errorf("last_records после failed = %v, want 556438", got)
	}
	if got := testutil.ToFloat64(importLastSuccess); got != okAt {
		t.Errorf("last_success после failed изменился: %v → %v", okAt, got)
	}
}

func TestHandler_ExposesAppAndRuntimeMetrics(t *testing.T) {
	SetBuildInfo("1.15.0")
	LoginFailures.WithLabelValues("form").Inc()
	EnrichmentLookups.WithLabelValues("year", "openlibrary", "found").Inc()

	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`skriptes_build_info{version="1.15.0"} 1`,
		`skriptes_login_failures_total{via="form"}`,
		`skriptes_enrichment_lookups_total{outcome="found",source="openlibrary",worker="year"}`,
		"go_goroutines",
		"process_resident_memory_bytes",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("в выдаче нет %q", want)
		}
	}
}

func TestExternalSources_ValueFromStateAtScrape(t *testing.T) {
	t.Cleanup(func() { SetExternalSources(nil) })
	paused := true
	SetExternalSources(func() map[string]bool {
		return map[string]bool{"www.googleapis.com": !paused, "openlibrary.org": true}
	})
	want := func(gb string) string {
		return `# HELP skriptes_external_source_up Внешний источник обогащения по хосту: 0 — сейчас на паузе после серии сбоев (сеть, 429, 5xx), 1 — запросы идут.
# TYPE skriptes_external_source_up gauge
skriptes_external_source_up{host="openlibrary.org"} 1
skriptes_external_source_up{host="www.googleapis.com"} ` + gb + "\n"
	}
	if err := testutil.CollectAndCompare(externalSources, strings.NewReader(want("0"))); err != nil {
		t.Error(err)
	}
	paused = false // пауза кончилась — без событий, просто при следующем сборе
	if err := testutil.CollectAndCompare(externalSources, strings.NewReader(want("1"))); err != nil {
		t.Error(err)
	}
}
