// Package metrics — метрики приложения в формате Prometheus.
//
// Отдаются отдельным внутренним HTTP-сервером (SKRIPTES_METRICS_ADDR), а не
// через основной сайт: в публичном деплое их забирает сборщик из доверенной сети
// через внутренний сайт Caddy :9180 (infra/Caddyfile.public). Метки — только с
// малым числом значений: без id книг, email и IP.
package metrics

import (
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry — свой реестр, а не глобальный prometheus.DefaultRegisterer: в выдачу
// попадает только то, что зарегистрировано здесь.
var Registry = prometheus.NewRegistry()

var factory = promauto.With(Registry)

var (
	buildInfo = factory.NewGaugeVec(prometheus.GaugeOpts{
		Name: "skriptes_build_info",
		Help: "Версия запущенного skriptes (значение всегда 1).",
	}, []string{"version"})

	httpRequests = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "skriptes_http_requests_total",
		Help: "HTTP-запросы к backend по методу, шаблону маршрута и коду ответа.",
	}, []string{"method", "route", "code"})

	httpDuration = factory.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "skriptes_http_request_duration_seconds",
		Help:    "Время обработки HTTP-запросов backend по методу и шаблону маршрута.",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30},
	}, []string{"method", "route"})

	// LoginFailures — неудачные входы: via = form | opds.
	LoginFailures = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "skriptes_login_failures_total",
		Help: "Неудачные попытки входа (via: form — форма, opds — Basic auth OPDS).",
	}, []string{"via"})

	// LoginThrottled — отказы по лимиту попыток входа: via = form | opds.
	LoginThrottled = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "skriptes_login_throttled_total",
		Help: "Отказы по лимиту неудачных попыток входа (via: form | opds).",
	}, []string{"via"})

	importRuns = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "skriptes_import_runs_total",
		Help: "Проходы импорта INPX по итогу: ok, unchanged (файл не менялся), skipped (второй INPX той же библиотеки), failed.",
	}, []string{"result"})

	importInProgress = factory.NewGauge(prometheus.GaugeOpts{
		Name: "skriptes_import_in_progress",
		Help: "1, пока идёт импорт INPX.",
	})

	importLastSuccess = factory.NewGauge(prometheus.GaugeOpts{
		Name: "skriptes_import_last_success_timestamp_seconds",
		Help: "Время окончания последнего удачного импорта изменённого INPX.",
	})

	importLastDuration = factory.NewGauge(prometheus.GaugeOpts{
		Name: "skriptes_import_last_duration_seconds",
		Help: "Длительность последнего удачного импорта.",
	})

	importLastRecords = factory.NewGauge(prometheus.GaugeOpts{
		Name: "skriptes_import_last_records",
		Help: "Записей в INPX последнего удачного импорта.",
	})

	importLastInserted = factory.NewGauge(prometheus.GaugeOpts{
		Name: "skriptes_import_last_books_inserted",
		Help: "Новых книг в последнем удачном импорте.",
	})

	importLastRecordErrors = factory.NewGauge(prometheus.GaugeOpts{
		Name: "skriptes_import_last_record_errors",
		Help: "Записей INPX с ошибкой (пропущены) в последнем удачном импорте.",
	})

	// ExternalSourcePauses — сколько раз источник ставили на паузу.
	ExternalSourcePauses = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "skriptes_external_source_pauses_total",
		Help: "Сколько раз внешний источник обогащения ставили на паузу после серии сбоев.",
	}, []string{"host"})

	// EnrichmentLookups — запросы фоновых воркеров обогащения к внешним
	// источникам: worker — воркер, source — источник, outcome — found |
	// not_found | error (как в таблицах учёта *_lookups) | paused (источник на
	// паузе прерывателя, запрос не ушёл).
	EnrichmentLookups = factory.NewCounterVec(prometheus.CounterOpts{
		Name: "skriptes_enrichment_lookups_total",
		Help: "Запросы обогащения к внешним источникам (фоновые воркеры и ленивое обогащение с карточек) по воркеру, источнику и исходу.",
	}, []string{"worker", "source", "outcome"})
)

func init() {
	Registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
}

// SetBuildInfo выставляет skriptes_build_info{version}.
func SetBuildInfo(version string) {
	buildInfo.WithLabelValues(version).Set(1)
}

// Handler — выдача /metrics.
func Handler() http.Handler {
	return promhttp.HandlerFor(Registry, promhttp.HandlerOpts{Registry: Registry})
}

// ImportResult — итог одного прохода импорта INPX для метрик.
type ImportResult struct {
	Records       int
	BooksInserted int
	RecordErrors  int
	Duration      time.Duration
}

// ImportStarted / ImportFinished окружают imp.Run одного файла.
func ImportStarted() { importInProgress.Set(1) }

// ImportFinished учитывает проход: result — ok | unchanged | skipped | failed;
// last_* обновляются только у удачного импорта изменённого файла.
func ImportFinished(result string, r ImportResult) {
	importInProgress.Set(0)
	importRuns.WithLabelValues(result).Inc()
	if result != "ok" {
		return
	}
	importLastSuccess.SetToCurrentTime()
	importLastDuration.Set(r.Duration.Seconds())
	importLastRecords.Set(float64(r.Records))
	importLastInserted.Set(float64(r.BooksInserted))
	importLastRecordErrors.Set(float64(r.RecordErrors))
}

// HTTPMiddleware считает запросы и их длительность по шаблону маршрута chi
// («/api/books/{id}», а не конкретный путь — иначе меток было бы столько же,
// сколько книг). Шаблон известен только после маршрутизации, поэтому читается
// после next.ServeHTTP. Не нашедшие маршрута — route="unmatched".
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		route := "unmatched"
		if rctx := chi.RouteContext(r.Context()); rctx != nil {
			if p := rctx.RoutePattern(); p != "" {
				route = p
			}
		}
		code := ww.Status()
		if code == 0 {
			code = http.StatusOK
		}
		method := normMethod(r.Method)
		httpRequests.WithLabelValues(method, route, strconv.Itoa(code)).Inc()
		httpDuration.WithLabelValues(method, route).Observe(time.Since(start).Seconds())
	})
}

// normMethod — метод запроса как метка: нестандартные сводятся в OTHER, чтобы
// клиент не мог наплодить меток.
func normMethod(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
		http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return m
	}
	return "OTHER"
}

// skriptes_external_source_up — 0, пока внешний источник (хост) на паузе
// прерывателя после серии сбоев (#299); 1 — доступен. Считается при сборе по
// состоянию прерывателя (#471): раньше 0 ставился при открытии паузы и
// снимался только итогом пробного запроса, и если после паузы к источнику не шло
// запросов (проход воркера кончился), метрика держала 0 сутками — алерт
// SkriptesSourcePaused горел ложно. Теперь 0 — только пока пауза идёт.
var sourceState atomic.Value // func() map[string]bool: хост → доступен

// SetExternalSourceState — откуда брать состояние источников (metadata, прерыватель).
func SetExternalSourceState(fn func() map[string]bool) { sourceState.Store(fn) }

type sourceUpCollector struct{ desc *prometheus.Desc }

func (c sourceUpCollector) Describe(ch chan<- *prometheus.Desc) { ch <- c.desc }

func (c sourceUpCollector) Collect(ch chan<- prometheus.Metric) {
	fn, _ := sourceState.Load().(func() map[string]bool)
	if fn == nil {
		return
	}
	for host, up := range fn() {
		v := 0.0
		if up {
			v = 1
		}
		ch <- prometheus.MustNewConstMetric(c.desc, prometheus.GaugeValue, v, host)
	}
}

func init() {
	Registry.MustRegister(sourceUpCollector{desc: prometheus.NewDesc("skriptes_external_source_up",
		"Доступность внешнего источника обогащения по хосту: 0 — на паузе после серии сбоев (сеть, 429, 5xx).",
		[]string{"host"}, nil)})
}
