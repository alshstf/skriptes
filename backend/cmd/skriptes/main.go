package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/meilisearch/meilisearch-go"
	"github.com/skriptes/skriptes/backend/internal/adaptations"
	"github.com/skriptes/skriptes/backend/internal/api"
	"github.com/skriptes/skriptes/backend/internal/auth"
	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/catalog"
	"github.com/skriptes/skriptes/backend/internal/collections"
	"github.com/skriptes/skriptes/backend/internal/config"
	"github.com/skriptes/skriptes/backend/internal/converter"
	"github.com/skriptes/skriptes/backend/internal/db"
	"github.com/skriptes/skriptes/backend/internal/email"
	"github.com/skriptes/skriptes/backend/internal/genres"
	"github.com/skriptes/skriptes/backend/internal/history"
	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/skriptes/skriptes/backend/internal/kindle"
	"github.com/skriptes/skriptes/backend/internal/metadata"
	"github.com/skriptes/skriptes/backend/internal/opds"
	"github.com/skriptes/skriptes/backend/internal/settings"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config load: %w", err)
	}

	logger := newLogger(cfg.LogLevel, cfg.LogFormat)
	slog.SetDefault(logger)

	dbCtx, dbCancel := context.WithTimeout(context.Background(), cfg.DatabaseTimeout)
	defer dbCancel()

	pool, err := db.NewPool(dbCtx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("db connect: %w", err)
	}
	defer pool.Close()
	logger.Info("database connected")

	if err := db.Migrate(cfg.DatabaseURL); err != nil {
		return fmt.Errorf("db migrate: %w", err)
	}
	logger.Info("migrations applied")

	// Seed справочника жанров — заполняет name_ru/parent_id для всех
	// fb2-кодов из встроенного словаря (genres_fb2.glst от Books.NET /
	// MyHomeLib). Идемпотентно: повторные старты переписывают
	// имена/иерархию. До этого момента genres-таблица могла иметь
	// name_ru = fb2_code (старая логика importer.upsertGenre); seed
	// исправит на человеческое имя там где код известен.
	seedCtx, seedCancel := context.WithTimeout(context.Background(), 15*time.Second)
	if n, err := genres.Seed(seedCtx, pool); err != nil {
		seedCancel()
		return fmt.Errorf("seed genres: %w", err)
	} else {
		logger.Info("genres dictionary seeded", "entries", n)
	}
	seedCancel()

	meili := meilisearch.New(cfg.MeiliURL, meilisearch.WithAPIKey(cfg.MeiliAPIKey))
	logger.Info("meilisearch client configured", "url", cfg.MeiliURL)

	// Стартовый scan: импортируем все *.inpx из каталога SKRIPTES_INPX_ROOT.
	// Идемпотентно: повторные старты на тех же файлах — no-op за счёт хэш-проверки.
	// Не блокируем HTTP — крутим в горутине; если /readyz нужно учитывать импорт,
	// добавим отдельный флаг в PR 5 вместе с queue/jobs API.
	// Один импортёр на процесс: его использует и стартовый скан, и ручная
	// пересинхронизация года в поиске из админки (ResyncYears).
	imp := importer.New(importer.Deps{Pool: pool, Meili: meili, Logger: logger})
	go runStartupImport(ctx(), imp, cfg.InpxRoot, logger)
	// Разовая пересинхронизация кодов языка в Meili после нормализации (миграция
	// 0015 чистит PG, но индекс Meili сам не трогает). Гейтится флагом в
	// app_settings — выполняется один раз на апгрейде, дальше no-op.
	go runOnceLangResync(ctx(), pool, imp, logger)
	// Разовый синк work_id в Meili: distinctAttribute=work_id появился в Phase 3,
	// существующие доки его не имели. Гейтится флагом, дальше no-op (после
	// группировки work_id синкается её воркером).
	go runOnceWorkIDResync(ctx(), pool, imp, logger)
	// Конфиг индекса works (на каждом старте) + разовый полный ресинк (на
	// апгрейде). Дальше индекс поддерживают импорт (полный) и таргетные синки
	// группировки/года. Гейтится флагом, в горутине — старт не блокирует.
	go runOnceWorksIndexSync(ctx(), pool, imp, logger)

	authSvc := auth.New(pool, 0)
	catalogSvc := catalog.New(pool)
	historySvc := history.New(pool)
	collectionsSvc := collections.New(pool)
	booksSvc := books.New(pool, meili, historySvc)

	conv, err := converter.New(cfg.BooksRoot, cfg.CacheRoot, cfg.FBCPath)
	if err != nil {
		return fmt.Errorf("converter init: %w", err)
	}
	logger.Info("converter ready", "fbc", cfg.FBCPath, "cache", cfg.CacheRoot)

	// Metadata enricher: цепочки провайдеров для обложек/аннотаций книг,
	// для фото/био авторов и для экранизаций. Порядок книжных — fb2
	// (локально, ~99% hit) → Open Library → Google Books. Авторские —
	// Wikipedia (top hit rate для русских классиков) → Open Library (fallback).
	// Экранизации — Wikidata (SPARQL P144); TMDB enrichment отдельной
	// фичей по запросу, требует API key.
	httpClient := &http.Client{Timeout: 10 * time.Second}
	sparqlClient := &http.Client{Timeout: 15 * time.Second} // SPARQL медленнее, отдельный timeout
	fb2Provider := metadata.NewFb2Provider()
	olProvider := metadata.NewOpenLibraryProvider(httpClient)
	gbProvider := metadata.NewGoogleBooksProvider(httpClient)
	wikiProvider := metadata.NewWikipediaProvider(httpClient)
	wdAdaptations := metadata.NewWikidataAdaptationsProvider(sparqlClient)
	enricher, err := metadata.New(
		pool,
		filepath.Join(cfg.CacheRoot, "covers"),
		[]metadata.CoverProvider{fb2Provider, olProvider, gbProvider},
		[]metadata.AnnotationProvider{fb2Provider, olProvider, gbProvider},
		[]metadata.AuthorPhotoProvider{wikiProvider, olProvider},
		[]metadata.AuthorBioProvider{wikiProvider, olProvider},
		[]metadata.AdaptationProvider{wdAdaptations},
		logger,
	)
	if err != nil {
		return fmt.Errorf("metadata init: %w", err)
	}
	// Рантайм-настройки кэша обложек: дефолты в коде, оверрайды в БД
	// (app_settings, раздел «Кэш обложек» в админке). Применяем лимиты
	// (бюджет LRU + пол свободного места) на старте.
	settingsStore := settings.New(pool)
	coverCfg, err := settingsStore.Cover(ctx())
	if err != nil {
		logger.Warn("read cover settings — using defaults", "err", err)
		coverCfg = settings.DefaultCoverConfig()
	}
	// При включённом прогреве лимит кэша = 0 (full-store, без эвикции):
	// иначе прогрев всей коллекции + LRU-бюджет = бесконечная мясорубка.
	enricher.WithCoverCache(coverCfg.EffectiveCacheMaxBytes(), coverCfg.MinFreeBytes())
	// Бакеты постеров/фото авторов — свои бюджеты, общий пол свободного места.
	enricher.SetPosterLimits(coverCfg.PosterCacheMaxBytes(), coverCfg.MinFreeBytes())
	enricher.SetPhotoLimits(coverCfg.PhotoCacheMaxBytes(), coverCfg.MinFreeBytes())
	// Самолечение висячих указателей постеров/фото (после старых очисток кэша,
	// когда они лежали вместе с обложками): зануляем битые ссылки + даём
	// дозаполнению их перекачать. В фоне, не блокируем старт HTTP.
	go enricher.HealDanglingAssets(ctx())
	// fb2 как локальный источник года (written_year/edition_year) для
	// фонового прогрева — без сети, в том же проходе что обложки/аннотации.
	enricher.WithLocalYear(fb2Provider)
	// fb2 как локальный источник атрибутов издания (переводчик/isbn/издатель/
	// src-title-info) — извлекается под тем же тумблером «Года» прогрева.
	enricher.WithLocalEdition(fb2Provider)
	logger.Info("metadata enricher ready",
		"cover_root", filepath.Join(cfg.CacheRoot, "covers"),
		"cache_max_mb", coverCfg.CacheMaxMB,
		"cache_min_free_mb", coverCfg.CacheMinFreeMB,
		"prewarm", coverCfg.Prewarm,
	)

	// Контроллер фонового прогрева: запуск/остановка по тумблеру настроек
	// в рантайме (без рестарта) + разовый прогон по кнопке. На старте
	// запускаем непрерывный прогрев, только если он включён в настройках.
	prewarmCfg := metadata.PrewarmConfig{
		Covers:      coverCfg.SyncCovers,
		Annotations: coverCfg.SyncAnnotations,
		Years:       coverCfg.SyncYears,
		Workers:     coverCfg.IntensityWorkers(),
		Delay:       coverCfg.IntensityDelay(),
	}
	// imp (importer) — YearResyncer: после прохода обработки коллекции, если
	// появились года, прогрев сам синкнёт Meili-поле year (без ручной кнопки).
	prewarmCtl := metadata.NewPrewarmController(enricher, pool, cfg.BooksRoot, prewarmCfg, imp, logger)
	if coverCfg.Prewarm {
		prewarmCtl.Start()
	}

	// Дозаполнение года написания из внешних источников (OpenLibrary
	// first_publish_year → Wikidata P577) для книг без written_year из fb2.
	// Воркер opt-in (по умолчанию выключен — ходит в публичные API),
	// включается тумблером в админке. Провайдеры те же, что для обложек.
	yearCfg, err := settingsStore.YearEnrichment(ctx())
	if err != nil {
		logger.Warn("read year enrichment settings — using defaults", "err", err)
		yearCfg = settings.DefaultYearEnrichmentConfig()
	}
	yearBackfillCtl := metadata.NewYearBackfillController(pool, olProvider, wdAdaptations, metadata.YearBackfillConfig{
		OpenLibrary:       yearCfg.OpenLibrary,
		Wikidata:          yearCfg.Wikidata,
		WholeCollection:   yearCfg.WholeCollection,
		OpenLibraryRPM:    yearCfg.OpenLibraryRPM,
		WikidataRPM:       yearCfg.WikidataRPM,
		NotFoundRetryDays: yearCfg.NotFoundRetryDays,
		ErrorRetryHours:   yearCfg.ErrorRetryHours,
	}, imp, logger)
	if yearCfg.Enabled {
		yearBackfillCtl.Start()
	}

	// Дозаполнение обложек из внешних источников (OpenLibrary → Google Books)
	// для книг без cover_path из fb2. Зеркало year-воркера: opt-in, per-source
	// rate-limit + учёт (book_cover_lookups). Сохранение делает тот же enricher.
	coverEnrichCfg, err := settingsStore.CoverEnrichment(ctx())
	if err != nil {
		logger.Warn("read cover enrichment settings — using defaults", "err", err)
		coverEnrichCfg = settings.DefaultCoverEnrichmentConfig()
	}
	coverBackfillCtl := metadata.NewCoverBackfillController(pool, enricher, olProvider, gbProvider, metadata.CoverBackfillConfig{
		OpenLibrary:       coverEnrichCfg.OpenLibrary,
		GoogleBooks:       coverEnrichCfg.GoogleBooks,
		WholeCollection:   coverEnrichCfg.WholeCollection,
		OpenLibraryRPM:    coverEnrichCfg.OpenLibraryRPM,
		GoogleBooksRPM:    coverEnrichCfg.GoogleBooksRPM,
		NotFoundRetryDays: coverEnrichCfg.NotFoundRetryDays,
		ErrorRetryHours:   coverEnrichCfg.ErrorRetryHours,
	}, logger)
	if coverEnrichCfg.Enabled {
		coverBackfillCtl.Start()
	}

	// Фоновые воркеры «людей и экранизаций» из внешних источников (external-only,
	// без fb2): био/фото авторов (Wikipedia/OL) и экранизации книг (Wikidata).
	// Оба opt-in; используют существующие маркеры metadata_fetched_at /
	// adaptations_fetched_at (как и lazy-путь), без новой таблицы.
	baCfg, err := settingsStore.BioAdaptation(ctx())
	if err != nil {
		logger.Warn("read bio/adaptation settings — using defaults", "err", err)
		baCfg = settings.DefaultBioAdaptationConfig()
	}
	authorBackfillCtl := metadata.NewAuthorBackfillController(pool, enricher, baCfg.BiosRPM, logger)
	if baCfg.Bios {
		authorBackfillCtl.Start()
	}
	adaptationBackfillCtl := metadata.NewAdaptationBackfillController(pool, enricher, baCfg.AdaptationsRPM, logger)
	if baCfg.Adaptations {
		adaptationBackfillCtl.Start()
	}

	// Группировка изданий (fb2-файлов) в логические книги (works): Tier-1
	// локально (название+язык, <src-title-info>, fb2_doc_id) + Tier-2 внешние
	// Work ID (OpenLibrary Work / Wikidata QID). Opt-in; ручной split/merge.
	wgCfg, err := settingsStore.WorkGrouping(ctx())
	if err != nil {
		logger.Warn("read work grouping settings — using defaults", "err", err)
		wgCfg = settings.DefaultWorkGroupingConfig()
	}
	workGroupCtl := metadata.NewWorkGroupController(pool, olProvider, wdAdaptations, metadata.WorkGroupConfig{
		OpenLibrary:       wgCfg.OpenLibrary,
		Wikidata:          wgCfg.Wikidata,
		WholeCollection:   wgCfg.WholeCollection,
		OpenLibraryRPM:    wgCfg.OpenLibraryRPM,
		WikidataRPM:       wgCfg.WikidataRPM,
		NotFoundRetryDays: wgCfg.NotFoundRetryDays,
		ErrorRetryHours:   wgCfg.ErrorRetryHours,
	}, imp, logger)
	if wgCfg.Enabled {
		workGroupCtl.Start()
	}

	// Видимость контента: глобально (admin) и персонально (профиль) скрытые
	// жанры/языки. Глобальный конфиг кэшируется в памяти (горячий путь
	// hard-block по id книги) и живо обновляется при сохранении из админки.
	contentResolver := settings.NewContentResolver(settingsStore)
	if err := contentResolver.Load(ctx()); err != nil {
		logger.Warn("read content settings — using defaults", "err", err)
	}

	// «Выключатели» lazy-обогащения по типам (режим «Выкл» на странице
	// «Фоновые операции»). Тоже кэшируются в памяти — читаются на горячем пути
	// GET карточек книги/автора/экранизаций, обновляются живо из админки.
	gatesResolver := settings.NewEnrichmentGateResolver(settingsStore)
	if err := gatesResolver.Load(ctx()); err != nil {
		logger.Warn("read enrichment gates — using defaults", "err", err)
	}

	// Kindle: CRUD по target'ам всегда доступен, send-to-kindle — только
	// если задан SMTP-конфиг. emailSender вернёт nil если SMTPHost пустой,
	// и handler сам отдаст 503 на send.
	kindleSvc := kindle.New(pool)
	emailSender := email.New(email.Config{
		Host:     cfg.SMTPHost,
		Port:     cfg.SMTPPort,
		User:     cfg.SMTPUser,
		Password: cfg.SMTPPassword,
		From:     cfg.SMTPFrom,
		UseTLS:   cfg.SMTPUseTLS,
	}, logger)
	if emailSender == nil {
		logger.Info("smtp not configured — send-to-kindle disabled")
	} else {
		logger.Info("smtp ready", "host", cfg.SMTPHost, "port", cfg.SMTPPort)
	}

	router := api.NewRouter(api.Deps{
		Version: cfg.Version,
		DB:      pool,
		Auth: api.AuthDeps{
			Service:        authSvc,
			CookieSecure:   cfg.CookieSecure,
			CookieDomain:   cfg.CookieDomain,
			AllowedOrigins: cfg.AllowedOrigins,
		},
		Books:       api.BooksDeps{Service: booksSvc},
		Catalog:     api.CatalogDeps{Service: catalogSvc},
		Collections: api.CollectionsDeps{Service: collectionsSvc},
		Download:    api.DownloadDeps{Books: booksSvc, Converter: conv},
		History:     api.HistoryDeps{Service: historySvc},
		Kindle: api.KindleDeps{
			Service:   kindleSvc,
			Email:     emailSender,
			Books:     booksSvc,
			Converter: conv,
			History:   historySvc,
		},
		Metadata: api.MetadataDeps{
			Service: enricher, BooksRoot: cfg.BooksRoot, Gates: gatesResolver,
			YearBackfill: yearBackfillCtl, Settings: settingsStore,
		},
		Adaptations: api.AdaptationsDeps{Service: adaptations.New(pool)},
		Settings: api.SettingsDeps{
			Store: settingsStore, Metadata: enricher, Prewarm: prewarmCtl,
			YearBackfill: yearBackfillCtl, CoverBackfill: coverBackfillCtl,
			AuthorBackfill: authorBackfillCtl, AdaptationBackfill: adaptationBackfillCtl,
			WorkGroup: workGroupCtl,
		},
		Content: api.ContentDeps{Resolver: contentResolver},
		OPDS: api.OPDSDeps{Handler: opds.NewHandler(opds.Config{
			// BaseURL пустой — handler возьмёт схему/host из заголовков
			// запроса (с поддержкой X-Forwarded-Proto/Host для proxy
			// сценариев типа Caddy). Если когда-то понадобится
			// захардкодить — добавим cfg.OPDSBaseURL отдельным полем.
			CoversRoot: filepath.Join(cfg.CacheRoot, "covers"),
		}, opds.Deps{
			Books:     booksSvc,
			Catalog:   catalogSvc,
			Converter: conv,
			BooksRoot: cfg.BooksRoot,
			Logger:    logger,
		})},
	})

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		logger.Info("http server starting", "addr", cfg.HTTPAddr, "version", cfg.Version)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failed", "err", err)
			stop()
		}
	}()

	<-sigCtx.Done()
	logger.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	logger.Info("bye")
	return nil
}

// ctx — фоновый контекст для startup-сканера.
// Отдельная функция чтобы было видно, что у скана нет shutdown-контекста
// (импорт всё равно отрабатывает до конца, даже если процесс ловит SIGTERM —
// безопасно благодаря пер-записной транзакции).
func ctx() context.Context { return context.Background() }

func runStartupImport(ctx context.Context, imp *importer.Importer, inpxRoot string, logger *slog.Logger) {
	files, err := findInpxFiles(inpxRoot)
	if err != nil {
		logger.Warn("startup import skipped — failed to scan inpx root", "root", inpxRoot, "err", err)
		return
	}
	if len(files) == 0 {
		logger.Info("startup import — no INPX files found", "root", inpxRoot)
		return
	}
	logger.Info("startup import beginning", "count", len(files), "root", inpxRoot)
	for _, f := range files {
		stats, err := imp.Run(ctx, f)
		if err != nil {
			logger.Error("startup import failed for file", "file", f, "err", err)
			continue
		}
		_ = stats // важная статистика уже залогирована изнутри Run
	}
	logger.Info("startup import finished")
}

// runOnceLangResync разово синкает нормализованные коды языка в Meili. Миграция
// 0015 приводит books.lang к нижнему регистру в PG, но Meili-индекс остаётся со
// старыми значениями ('RU' и т.п.) — этот шаг их выравнивает. Гейтится флагом
// app_settings.lang_normalized_v1: выполняется один раз (на апгрейде), дальше
// no-op. Не блокирует старт — крутится в горутине.
func runOnceLangResync(ctx context.Context, pool *pgxpool.Pool, imp *importer.Importer, logger *slog.Logger) {
	// v2: помимо регистра (0015) теперь срезаем региональные субтеги (0016),
	// поэтому Meili нужно синкнуть заново — новый ключ перезапускает one-shot.
	const flag = "lang_normalized_v2"
	var done bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_settings WHERE key = $1)`, flag).Scan(&done); err != nil {
		logger.Warn("lang resync: check flag failed — skip", "err", err)
		return
	}
	if done {
		return
	}
	n, err := imp.ResyncLangs(ctx)
	if err != nil {
		logger.Warn("lang resync to meili failed — will retry next start", "err", err)
		return
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app_settings (key, value, updated_at) VALUES ($1, 'true'::jsonb, now())
		 ON CONFLICT (key) DO NOTHING`, flag); err != nil {
		logger.Warn("lang resync: set flag failed (will rerun next start, idempotent)", "err", err)
	}
	logger.Info("one-time lang resync to meili done", "count", n)
}

// runOnceWorkIDResync разово синкает books.work_id в Meili. distinctAttribute=
// work_id включён в Phase 3 — существующие доки этого поля не имели, без него
// distinct не схлопывал бы. Гейтится флагом app_settings.work_id_synced_v1:
// один раз на апгрейде, дальше no-op (после импорта/группировки work_id
// синкается их воркерами). В горутине, старт не блокирует.
func runOnceWorkIDResync(ctx context.Context, pool *pgxpool.Pool, imp *importer.Importer, logger *slog.Logger) {
	// Настройки индекса (в т.ч. distinctAttribute=work_id) применяем на КАЖДОМ
	// старте — идемпотентно. Иначе на стабильном деплое без нового импорта
	// distinct не включился бы (configureIndex живёт только внутри Run).
	if err := imp.ConfigureIndex(ctx); err != nil {
		logger.Warn("meili configure index at startup failed", "err", err)
	}
	const flag = "work_id_synced_v1"
	var done bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_settings WHERE key = $1)`, flag).Scan(&done); err != nil {
		logger.Warn("work_id resync: check flag failed — skip", "err", err)
		return
	}
	if done {
		return
	}
	n, err := imp.ResyncWorkIDs(ctx)
	if err != nil {
		logger.Warn("work_id resync to meili failed — will retry next start", "err", err)
		return
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app_settings (key, value, updated_at) VALUES ($1, 'true'::jsonb, now())
		 ON CONFLICT (key) DO NOTHING`, flag); err != nil {
		logger.Warn("work_id resync: set flag failed (will rerun next start, idempotent)", "err", err)
	}
	logger.Info("one-time work_id resync to meili done", "count", n)
}

// runOnceWorksIndexSync применяет настройки индекса works на каждом старте
// (идемпотентно) и разово делает полный ResyncWorksIndex на апгрейде (гейт
// app_settings.works_index_synced_v1). Веб-список/Cmd+K ищут по works-индексу —
// без этого на стабильном деплое без импорта индекс был бы пустым. Дальше
// индекс поддерживают импорт (полный ресинк) и таргетные синки группировки/года.
func runOnceWorksIndexSync(ctx context.Context, pool *pgxpool.Pool, imp *importer.Importer, logger *slog.Logger) {
	if err := imp.ConfigureWorksIndex(ctx); err != nil {
		logger.Warn("meili configure works index at startup failed", "err", err)
	}
	const flag = "works_index_synced_v1"
	var done bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_settings WHERE key = $1)`, flag).Scan(&done); err != nil {
		logger.Warn("works index sync: check flag failed — skip", "err", err)
		return
	}
	if done {
		return
	}
	n, err := imp.ResyncWorksIndex(ctx)
	if err != nil {
		logger.Warn("works index resync failed — will retry next start", "err", err)
		return
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app_settings (key, value, updated_at) VALUES ($1, 'true'::jsonb, now())
		 ON CONFLICT (key) DO NOTHING`, flag); err != nil {
		logger.Warn("works index sync: set flag failed (will rerun next start, idempotent)", "err", err)
	}
	logger.Info("one-time works index resync done", "count", n)
}

// findInpxFiles возвращает все *.inpx из каталога (нерекурсивно), отсортированные.
func findInpxFiles(root string) ([]string, error) {
	if root == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(strings.ToLower(name), ".inpx") {
			out = append(out, filepath.Join(root, name))
		}
	}
	sort.Strings(out)
	return out, nil
}

func newLogger(level, format string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	if format == "text" {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(h)
}
