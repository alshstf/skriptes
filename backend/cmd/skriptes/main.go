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

// version — версия сборки, впекается линкером при релизе
// (Dockerfile: -ldflags "-X main.version=${VERSION}", release.yml передаёт тег
// v1.9.0). "dev" — локальная сборка без бампа. Приоритетнее env SKRIPTES_VERSION:
// на проде образ обычно пинится moving-тегом `latest`, и env="latest" неинформативен —
// а впечённая версия точна (это реальный собранный тег). См. effectiveVersion.
var version = "dev"

// effectiveVersion — что показать в UI/логах: впечённая версия сборки (без
// ведущего «v»), иначе fallback на env SKRIPTES_VERSION (envVersion). Так образ
// `:latest`, собранный из тега v1.9.0, рапортует «1.9.0», а не «latest».
func effectiveVersion(envVersion string) string {
	if v := strings.TrimPrefix(version, "v"); v != "" && v != "dev" {
		return v
	}
	return envVersion
}

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
	// Локальные оверрайды метаданных (ручная корректура каталога, только админ).
	// imp ресинкает works-индекс после правки индексируемого поля (lang/title/…).
	overrideCtl := metadata.NewOverrideController(pool, imp, logger)
	go runStartupImport(ctx(), pool, imp, overrideCtl, cfg.InpxRoot, logger)
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
	// Локализацию works.title запускаем В ТОЙ ЖЕ горутине ПОСЛЕ синка индекса:
	// ей нужен сконфигурированный works-индекс для таргетного ресинка
	// изменённых работ (порядок между отдельными горутинами не гарантирован).
	go func() {
		// Классификация сборников — ДО полного ресинка индекса: бамп схемы
		// works-индекса (v6, поле kind) ресинкает все доки, и kind должен уже
		// стоять, иначе первая выдача уйдёт без типов до следующего ресинка.
		runOnceWorkKindClassify(ctx(), pool, logger)
		// Служебные авторы works-индекс не трогают (авторская, не works-сущность) —
		// порядок относительно ресинка не важен, живёт в той же горутине для простоты.
		runOnceServiceAuthorClassify(ctx(), pool, logger)
		runOnceWorksIndexSync(ctx(), pool, imp, logger)
		runOnceWorkTitleLocalize(ctx(), pool, imp, logger)
		runOnceSrcLangSync(ctx(), pool, imp, logger)
	}()

	authSvc := auth.New(pool, 0)
	catalogSvc := catalog.New(pool)
	historySvc := history.New(pool)
	// Популярность works-индекса = вовлечённость инстанса (Σ изданий: views + 3×reads,
	// считается в workDocSelect). Трекер помечает работу при просмотре/чтении и батчем
	// (раз в 30с) таргетно ре-апсертит изменившиеся в индекс — свежесть между полными
	// ресинками без upsert'а на каждое событие. sort=popularity на /books.
	popTracker := importer.NewPopularityTracker(imp, logger)
	historySvc.SetEngagementHook(popTracker.MarkBook)
	go popTracker.Run(ctx(), 30*time.Second)
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
	// OL/GB — отдельные клиенты с осмысленным User-Agent: анонимный Go-UA
	// троттлится (особенно OpenLibrary → наблюдались context deadline). OL даём
	// 20с — его search.json медленный. Wiki ставит свой UA сам, остаётся на httpClient.
	olHTTPClient := metadata.NewEnricherHTTPClient(20 * time.Second)
	gbHTTPClient := metadata.NewEnricherHTTPClient(10 * time.Second)
	fb2Provider := metadata.NewFb2Provider()
	gbProvider := metadata.NewGoogleBooksProvider(gbHTTPClient).WithAPIKey(cfg.GoogleBooksAPIKey).WithCountry(cfg.GoogleBooksCountry)
	// Диагностика: без ключа GB-запросы уходят анонимно → 429 и не видны в usage
	// проекта. Логируем факт наличия (не сам ключ), чтобы сразу видеть мисконфиг.
	logger.Info("google books provider configured", "api_key_set", cfg.GoogleBooksAPIKey != "")
	wdAdaptations := metadata.NewWikidataAdaptationsProvider(sparqlClient)
	// Слой 2 точности обогащения авторов: после имя-гейта резолв автора
	// проверяет профессию кандидата (Wikidata P106) и отсекает однофамильцев-
	// не-писателей. Реализацию (OccupationVerdict) держит wdAdaptations — у него
	// уже есть SPARQL-клиент. Гейт на ОБОИХ авторских путях: Wikipedia (QID через
	// pageprops) и OpenLibrary (QID бесплатно из remote_ids.wikidata) — иначе
	// wiki-отказ по профессии протёк бы в OL-fallback (цепочка bio/photo).
	wikiProvider := metadata.NewWikipediaProvider(httpClient).WithOccupationGate(wdAdaptations.OccupationVerdict)
	olProvider := metadata.NewOpenLibraryProvider(olHTTPClient).WithOccupationGate(wdAdaptations.OccupationVerdict)
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
	// TMDB — приоритетный источник постеров экранизаций (по P4947/P4983 из
	// SPARQL-ответа Wikidata). Без ключа — только Commons P18 (~16% покрытия).
	if cfg.TMDBAPIKey != "" {
		enricher.WithTMDBPosters(metadata.NewTMDBPosterProvider(cfg.TMDBAPIKey))
	}
	logger.Info("tmdb poster provider configured", "api_key_set", cfg.TMDBAPIKey != "")
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

	// Дозаполнение языка оригинала (books.src_lang) из Wikidata (P407 с
	// precision-гейтами) для переводов без fb2 <src-lang>. Зеркало year-воркера:
	// opt-in, rate-limit + учёт (book_src_lang_lookups). Источник один —
	// Wikidata; OL сознательно не источник (поля «язык оригинала» у него нет).
	// imp — WorksIndexSyncer: таргетный ресинк src_lang[]/orig_lang[] работ.
	srcLangCfg, err := settingsStore.SrcLangEnrichment(ctx())
	if err != nil {
		logger.Warn("read src_lang enrichment settings — using defaults", "err", err)
		srcLangCfg = settings.DefaultSrcLangEnrichmentConfig()
	}
	srcLangBackfillCtl := metadata.NewSrcLangBackfillController(pool, wdAdaptations, metadata.SrcLangBackfillConfig{
		Wikidata:          srcLangCfg.Wikidata,
		WholeCollection:   srcLangCfg.WholeCollection,
		WikidataRPM:       srcLangCfg.WikidataRPM,
		NotFoundRetryDays: srcLangCfg.NotFoundRetryDays,
		ErrorRetryHours:   srcLangCfg.ErrorRetryHours,
	}, imp, logger)
	if srcLangCfg.Enabled {
		srcLangBackfillCtl.Start()
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

	// Фоновое дозаполнение внешнего рейтинга (books.external_rating) из Google
	// Books / OpenLibrary для книг без рейтинга. Зеркало cover-воркера: opt-in,
	// per-source rate-limit + учёт (book_external_rating_lookups); пишет рейтинг
	// прямо в books (кэша нет).
	extRatingCfg, err := settingsStore.ExternalRating(ctx())
	if err != nil {
		logger.Warn("read external rating settings — using defaults", "err", err)
		extRatingCfg = settings.DefaultExternalRatingConfig()
	}
	externalRatingCtl := metadata.NewExternalRatingBackfillController(pool, gbProvider, olProvider, metadata.ExternalRatingBackfillConfig{
		GoogleBooks:         extRatingCfg.GoogleBooks,
		OpenLibrary:         extRatingCfg.OpenLibrary,
		WholeCollection:     extRatingCfg.WholeCollection,
		GoogleBooksRPM:      extRatingCfg.GoogleBooksRPM,
		GoogleBooksDailyCap: extRatingCfg.GoogleBooksDailyCap,
		OpenLibraryRPM:      extRatingCfg.OpenLibraryRPM,
		NotFoundRetryDays:   extRatingCfg.NotFoundRetryDays,
		ErrorRetryHours:     extRatingCfg.ErrorRetryHours,
	}, logger)
	if extRatingCfg.Enabled {
		externalRatingCtl.Start()
	}

	// Фоновое дозаполнение счётчиков «известности» работ (works.fantlab_marks /
	// ol_ratings_count / ol_want_count) из Fantlab и OpenLibrary — сигналы
	// интегральной популярности (computeWorkPopularity). Work-level зеркало
	// внешнего рейтинга; после найденного — таргетный ресинк works-индекса (imp).
	renownCfg, err := settingsStore.Renown(ctx())
	if err != nil {
		logger.Warn("read renown settings — using defaults", "err", err)
		renownCfg = settings.DefaultRenownConfig()
	}
	fantlabProvider := metadata.NewFantlabProvider(olHTTPClient)
	renownCtl := metadata.NewRenownBackfillController(pool, fantlabProvider, olProvider, wdAdaptations, imp, metadata.RenownBackfillConfig{
		Fantlab:           renownCfg.Fantlab,
		OpenLibrary:       renownCfg.OpenLibrary,
		Wikidata:          renownCfg.Wikidata,
		WholeCollection:   renownCfg.WholeCollection,
		FantlabRPM:        renownCfg.FantlabRPM,
		OpenLibraryRPM:    renownCfg.OpenLibraryRPM,
		WikidataRPM:       renownCfg.WikidataRPM,
		FoundRefreshDays:  renownCfg.FoundRefreshDays,
		NotFoundRetryDays: renownCfg.NotFoundRetryDays,
		ErrorRetryHours:   renownCfg.ErrorRetryHours,
	}, logger)
	if renownCfg.Enabled {
		renownCtl.Start()
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
		Version: effectiveVersion(cfg.Version),
		DB:      pool,
		Auth: api.AuthDeps{
			Service:             authSvc,
			CookieSecure:        cfg.CookieSecure,
			CookieDomain:        cfg.CookieDomain,
			AllowedOrigins:      cfg.AllowedOrigins,
			LoginRateLimitIP:    cfg.LoginRateLimitIP,
			LoginRateLimitEmail: cfg.LoginRateLimitEmail,
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
			YearBackfill: yearBackfillCtl, SrcLangBackfill: srcLangBackfillCtl,
			CoverBackfill:  coverBackfillCtl,
			ExternalRating: externalRatingCtl,
			Renown:         renownCtl,
			AuthorBackfill: authorBackfillCtl, AdaptationBackfill: adaptationBackfillCtl,
			WorkGroup: workGroupCtl,
			Overrides: overrideCtl,
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
			History:   historySvc,
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
		logger.Info("http server starting", "addr", cfg.HTTPAddr, "version", effectiveVersion(cfg.Version))
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

func runStartupImport(ctx context.Context, pool *pgxpool.Pool, imp *importer.Importer, overrideCtl *metadata.OverrideController, inpxRoot string, logger *slog.Logger) {
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
	// Ре-применить ручные оверрайды полей, которые импорт ПЕРЕЗАПИСЫВАЕТ (lang) —
	// иначе ре-импорт коллекции сбросил бы правки (грабля №19).
	if n, err := overrideCtl.ReapplyAfterImport(ctx); err != nil {
		logger.Warn("reapply metadata overrides after import failed", "err", err)
	} else if n > 0 {
		logger.Info("reapplied metadata overrides after import", "count", n)
	}
	// Классифицировать НОВЫЕ работы импорта (сборники/антологии). Идемпотентно и
	// дёшево; правит только kind_source IS NULL/'heuristic', полный ресинк индекса
	// в конце imp.Run уже забрал kind для ранее классифицированных — свежие метки
	// подтянутся следующим ресинком (некритично: новинки редко сборники).
	if n, err := metadata.ClassifyWorkKinds(ctx, pool); err != nil {
		logger.Warn("classify work kinds after import failed", "err", err)
	} else if n > 0 {
		logger.Info("classified work kinds after import", "count", n)
	}
	// Разметить НОВЫХ служебных авторов импорта («Коллектив авторов» и т.п.) —
	// агрегаты-псевдоавторы вне списка /authors. Идемпотентно; ручные метки
	// (is_service_source='manual') не перетирает.
	if n, err := metadata.ClassifyServiceAuthors(ctx, pool); err != nil {
		logger.Warn("classify service authors after import failed", "err", err)
	} else if n > 0 {
		logger.Info("classified service authors after import", "count", n)
	}
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

// runOnceWorkKindClassify — разовый эвристический бэкфилл типов работ
// (works.kind: сборник/антология/том собрания — миграция 0034). Гейтится флагом
// app_settings.work_kind_classified_v1. Зовётся ДО runOnceWorksIndexSync в той же
// горутине: бамп схемы индекса (v6, поле kind) ресинкает все доки — kind должен
// уже стоять. Дальше типы поддерживает вызов после импорта (runStartupImport).
func runOnceWorkKindClassify(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	const flag = "work_kind_classified_v1"
	var done bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_settings WHERE key = $1)`, flag).Scan(&done); err != nil {
		logger.Warn("work kind classify: check flag failed — skip", "err", err)
		return
	}
	if done {
		return
	}
	n, err := metadata.ClassifyWorkKinds(ctx, pool)
	if err != nil {
		logger.Warn("work kind classify failed — will retry next start", "err", err)
		return
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app_settings (key, value, updated_at) VALUES ($1, 'true'::jsonb, now())
		 ON CONFLICT (key) DO NOTHING`, flag); err != nil {
		logger.Warn("work kind classify: set flag failed (will rerun next start, idempotent)", "err", err)
	}
	logger.Info("one-time work kind classification done", "count", n)
}

// runOnceServiceAuthorClassify — разовый эвристический бэкфилл «служебных
// авторов» (агрегатов-псевдоавторов) на существующей коллекции. Гейт
// service_authors_classified_v1: один раз на апгрейде; дальше новых метит
// after-import вызов. Зеркало runOnceWorkKindClassify.
func runOnceServiceAuthorClassify(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	const flag = "service_authors_classified_v1"
	var done bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_settings WHERE key = $1)`, flag).Scan(&done); err != nil {
		logger.Warn("service author classify: check flag failed — skip", "err", err)
		return
	}
	if done {
		return
	}
	n, err := metadata.ClassifyServiceAuthors(ctx, pool)
	if err != nil {
		logger.Warn("service author classify failed — will retry next start", "err", err)
		return
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app_settings (key, value, updated_at) VALUES ($1, 'true'::jsonb, now())
		 ON CONFLICT (key) DO NOTHING`, flag); err != nil {
		logger.Warn("service author classify: set flag failed (will rerun next start, idempotent)", "err", err)
	}
	logger.Info("one-time service author classification done", "count", n)
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
// (идемпотентно) и разово делает полный ResyncWorksIndex на апгрейде. Гейт
// app_settings версионирован схемой дока (importer.WorksIndexSyncedFlagKey):
// бамп importer.WorksIndexSchemaVersion форсит ресинк на ближайшем старте —
// иначе новое вычисляемое поле workDoc тихо остаётся нулевым на стабильном
// деплое (так popularity был мёртв всю 1.5.x). Веб-список/Cmd+K ищут по
// works-индексу — без ресинка на свежем инстансе индекс был бы пустым. Дальше
// индекс поддерживают импорт (полный ресинк) и таргетные синки группировки/года.
func runOnceWorksIndexSync(ctx context.Context, pool *pgxpool.Pool, imp *importer.Importer, logger *slog.Logger) {
	if err := imp.ConfigureWorksIndex(ctx); err != nil {
		logger.Warn("meili configure works index at startup failed", "err", err)
	}
	flag := importer.WorksIndexSyncedFlagKey()
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
	// GC устаревших версий ключа — не копить мусор при бампах схемы.
	if _, err := pool.Exec(ctx,
		`DELETE FROM app_settings WHERE key LIKE 'works_index_synced_v%' AND key <> $1`, flag); err != nil {
		logger.Warn("works index sync: gc old flag keys failed", "err", err)
	}
	logger.Info("one-time works index resync done", "count", n, "flag", flag)
}

// runOnceWorkTitleLocalize — разовый backfill: локализует works.title на
// доминирующий язык библиотеки для работ, у которых есть издание в этом языке
// (см. metadata.LocalizeWorkTitles). Чинит «перевод+оригинал слиты, каноникой
// стало иноязычное издание» — карточка и works-поиск показывали английский
// заголовок при русских изданиях. Изменённые работы таргетно ресинкаются в
// works-индекс (поиск по локализованному названию начинает находить).
// Гейт app_settings.work_title_localized_v1: один раз на апгрейде, дальше no-op
// (новые такие работы локализует группировка в apply). Зовётся ПОСЛЕ
// runOnceWorksIndexSync — индекс уже сконфигурирован/наполнен.
func runOnceWorkTitleLocalize(ctx context.Context, pool *pgxpool.Pool, imp *importer.Importer, logger *slog.Logger) {
	const flag = "work_title_localized_v1"
	var done bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_settings WHERE key = $1)`, flag).Scan(&done); err != nil {
		logger.Warn("work title localize: check flag failed — skip", "err", err)
		return
	}
	if done {
		return
	}
	changed, dom, err := metadata.LocalizeWorkTitles(ctx, pool)
	if err != nil {
		logger.Warn("work title localize failed — will retry next start", "err", err)
		return
	}
	// Ресинк индекса для изменённых работ ДО установки флага: если он упадёт, не
	// фиксируем гейт — на следующем старте title уже локализованы (changed=∅),
	// поэтому индекс досинкнётся полным ResyncWorksIndex как фолбэк.
	if len(changed) > 0 {
		if err := imp.UpsertWorksToIndex(ctx, changed); err != nil {
			logger.Warn("work title localize: works index resync failed — retry next start", "err", err)
			if _, rerr := imp.ResyncWorksIndex(ctx); rerr != nil {
				logger.Warn("work title localize: full works index resync fallback failed", "err", rerr)
				return
			}
		}
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app_settings (key, value, updated_at) VALUES ($1, 'true'::jsonb, now())
		 ON CONFLICT (key) DO NOTHING`, flag); err != nil {
		logger.Warn("work title localize: set flag failed (idempotent rerun)", "err", err)
	}
	logger.Info("one-time work title localization done", "lang", dom, "changed", len(changed))
}

// runOnceSrcLangSync — разовый полный ресинк works-индекса после появления поля
// src_lang (язык оригинала, фасет/фильтр на /books): существующие доки его не
// имеют, а filterable-атрибут применяет ConfigureWorksIndex на каждом старте.
// Гейт app_settings.src_lang_synced_v1: один раз на апгрейде, дальше no-op
// (дальше src_lang доезжает полным ресинком импорта и авто-ресинком прогрева —
// Prewarmer.maybeResyncSrcLangs). Зовётся ПОСЛЕ runOnceWorksIndexSync: на свежем
// инстансе тот уже наполнил индекс доками с src_lang — тогда этот шаг ставит
// флаг по нулевой работе быстро (повторный полный ресинк идемпотентен).
// ⚠️ Новым изменениям схемы workDoc отдельный гейт НЕ заводить — бампать
// importer.WorksIndexSchemaVersion (см. runOnceWorksIndexSync).
func runOnceSrcLangSync(ctx context.Context, pool *pgxpool.Pool, imp *importer.Importer, logger *slog.Logger) {
	const flag = "src_lang_synced_v1"
	var done bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_settings WHERE key = $1)`, flag).Scan(&done); err != nil {
		logger.Warn("src_lang sync: check flag failed — skip", "err", err)
		return
	}
	if done {
		return
	}
	n, err := imp.ResyncWorksIndex(ctx)
	if err != nil {
		logger.Warn("src_lang works resync failed — will retry next start", "err", err)
		return
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app_settings (key, value, updated_at) VALUES ($1, 'true'::jsonb, now())
		 ON CONFLICT (key) DO NOTHING`, flag); err != nil {
		logger.Warn("src_lang sync: set flag failed (will rerun next start, idempotent)", "err", err)
	}
	logger.Info("one-time src_lang works resync done", "count", n)
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
