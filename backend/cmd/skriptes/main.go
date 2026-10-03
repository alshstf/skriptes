package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
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
	"github.com/skriptes/skriptes/backend/internal/logredact"
	"github.com/skriptes/skriptes/backend/internal/metadata"
	"github.com/skriptes/skriptes/backend/internal/metrics"
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

	// Один импортёр на процесс: его использует и импорт INPX (стартовый скан +
	// слежение, запускается в конце горутины разовых шагов ниже), и ручная
	// пересинхронизация года в поиске из админки (ResyncYears).
	imp := importer.New(importer.Deps{Pool: pool, Meili: meili, Logger: logger, InpxFiles: cfg.InpxFiles,
		MeiliURL: cfg.MeiliURL, MeiliAPIKey: cfg.MeiliAPIKey})
	// Локальные оверрайды метаданных (ручная корректура каталога, только админ).
	// imp ресинкает works-индекс после правки индексируемого поля (lang/title/…).
	overrideCtl := metadata.NewOverrideController(pool, imp, logger)
	// Разовая пересинхронизация кодов языка в Meili после нормализации (миграция
	// 0015 чистит PG, но индекс Meili сам не трогает). Гейтится флагом в
	// app_settings — выполняется один раз на апгрейде, дальше no-op.
	metadata.Go(func(c context.Context) { runOnceLangResync(c, pool, imp, logger) })
	// Разовый синк work_id в Meili: distinctAttribute=work_id появился в Phase 3,
	// существующие доки его не имели. Гейтится флагом, дальше no-op (после
	// группировки work_id синкается её воркером).
	metadata.Go(func(c context.Context) { runOnceWorkIDResync(c, pool, imp, logger) })
	// Конфиг индекса works (на каждом старте) + разовый полный ресинк (на
	// апгрейде). Дальше индекс поддерживают импорт (полный) и таргетные синки
	// группировки/года. Гейтится флагом, в горутине — старт не блокирует.
	// Локализацию works.title запускаем В ТОЙ ЖЕ горутине ПОСЛЕ синка индекса:
	// ей нужен сконфигурированный works-индекс для таргетного ресинка
	// изменённых работ (порядок между отдельными горутинами не гарантирован).
	// Контроллер группировки создаётся ниже — разовому разбору склеек в цепочке
	// он нужен; цепочка подождёт его здесь (инициализация main быстрая).
	workGroupReady := make(chan *metadata.WorkGroupController, 1)
	metadata.Go(func(c context.Context) {
		// Классификация сборников — ДО полного ресинка индекса: бамп схемы
		// works-индекса (v6, поле kind) ресинкает все доки, и kind должен уже
		// стоять, иначе первая выдача уйдёт без типов до следующего ресинка.
		runOnceWorkKindClassify(c, pool, logger)
		// Служебные авторы works-индекс не трогают (авторская, не works-сущность) —
		// порядок относительно ресинка не важен, живёт в той же горутине для простоты.
		runOnceServiceAuthorClassify(c, pool, logger)
		runOnceGenreAliases(c, pool, imp, logger)
		runOnceWorksIndexSync(c, pool, imp, logger)
		// Миграция 0039 могла схлопнуть дубли книг — убрать их из поиска (индексы
		// к этому моменту сконфигурированы). Без дублей — no-op.
		if n, err := imp.PurgeDedupedDocs(c); err != nil {
			logger.Warn("search cleanup after book dedup failed — will retry next start", "err", err)
		} else if n > 0 {
			logger.Info("search cleanup after book dedup done", "books_removed", n)
		}
		runOnceWorkTitleLocalize(c, pool, imp, logger)
		runOnceSrcLangSync(c, pool, imp, logger)
		runOnceSrcLangCanonical(c, pool, imp, logger)
		// Серии работ, которые выпуск INPX проставил уже импортированным книгам (#275):
		// индекс сконфигурирован и наполнен — досинкиваем только изменённые работы.
		runOnceWorkSeriesSync(c, pool, imp, logger)
		// Известность авторов — ПОСЛЕ ресинка works-индекса: оба гоняют один и
		// тот же тяжёлый скан workDocSelect, параллелить их незачем (и kind к
		// этому моменту classифицирован — сборники вне вклада).
		runOnceAuthorRenown(c, pool, imp, logger)
		runOnceSplitAlienEditions(c, pool, imp, logger)
		// Склейки, которые новые гейты Tier-2 уже не допустили бы (#279), — до
		// импорта: и разбор, и импорт массово пишут в works/books.
		runOnceRegroupTitleConflicts(c, pool, <-workGroupReady, logger)
		// Правила, которые применяет только импорт (межавторские серии), сменились —
		// следующий импорт (ниже, в этой же горутине) пройдёт полностью.
		runOnceForceReimport(c, pool, logger)
		// Сверка индексов с PG на каждом старте (#283): убирает фантомы, которые
		// оставили прошлые импорты или оборванный остановкой синк группировки
		// (#270), и заполняет works-индекс, если Meili пуст после восстановления.
		if r, err := imp.ReconcileIndexes(c); err != nil {
			logger.Warn("search index reconcile failed", "err", err)
		} else {
			logger.Info("search index reconcile done", "works_removed", r.WorksRemoved,
				"works_added", r.WorksAdded, "books_removed", r.BooksRemoved, "books_missing", r.BooksMissing)
			// Индекс книг почти пуст (база восстановлена из дампа на пустой Meili, #305) —
			// его пишет только импорт: сбросить хэш, стартовый импорт ниже пройдёт полностью.
			if r.BooksNeedReimport() {
				if _, err := pool.Exec(c, `UPDATE collections SET last_inpx_hash = NULL`); err != nil {
					logger.Warn("books index is missing documents; forcing full reimport failed", "err", err)
				} else {
					logger.Warn("books index is missing documents — full INPX reimport scheduled",
						"missing", r.BooksMissing, "live", r.BooksLive)
				}
			}
		}
		// Импорт INPX — после разовых шагов, а не параллельно с ними: и те, и шаги
		// после импорта массово пишут в works, вперемешку ловили deadlock (#300).
		// Стартовый скан всех *.inpx из SKRIPTES_INPX_ROOT (или только
		// SKRIPTES_INPX_FILES), дальше раз в SKRIPTES_INPX_WATCH_INTERVAL — новый
		// или изменённый INPX без рестарта. Повторный старт на тех же файлах —
		// no-op за счёт хэш-проверки. HTTP не ждёт ни того, ни другого.
		runImportLoop(c, pool, imp, overrideCtl, cfg.InpxRoot, cfg.InpxFiles, cfg.InpxWatchInterval, logger)
	})

	authSvc := auth.New(pool, 0)
	// Сессии до 1.12.0 хранили сырой токен — переводим в SHA-256 (идемпотентно, на
	// каждом старте; см. HashLegacySessionTokens). Ошибка не фатальна: такие сессии
	// просто не пройдут проверку, пользователь войдёт заново.
	if n, err := authSvc.HashLegacySessionTokens(ctx()); err != nil {
		logger.Error("hash legacy session tokens", "err", err)
	} else if n > 0 {
		logger.Info("legacy session tokens hashed", "count", n)
	}
	catalogSvc := catalog.New(pool)
	historySvc := history.New(pool)
	// Популярность works-индекса = вовлечённость инстанса (Σ изданий: views + 3×reads,
	// считается в workDocSelect). Трекер помечает работу при просмотре/чтении и батчем
	// (раз в 30с) таргетно ре-апсертит изменившиеся в индекс — свежесть между полными
	// ресинками без upsert'а на каждое событие. sort=popularity на /books.
	popTracker := importer.NewPopularityTracker(imp, logger)
	historySvc.SetEngagementHook(popTracker.MarkBook)
	metadata.Go(func(c context.Context) { popTracker.Run(c, 30*time.Second) })
	// Хранимые число работ и рейтинг авторов — ключи сортировок /authors (#302).
	metadata.Go(func(c context.Context) { runAuthorStatsLoop(c, pool, logger) })
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
	// Политика приёма кандидата-автора (metadata/candidate_policy.go): после
	// гейта имени статья проходит проверку по фактам Wikidata (профессия, годы,
	// книги — CandidateFacts держит wdAdaptations, у него уже есть SPARQL-клиент) и
	// профилю книг автора. Проверка на ОБОИХ авторских путях: Wikipedia (QID через
	// pageprops) и OpenLibrary (QID бесплатно из remote_ids.wikidata) — иначе
	// отказ Википедии протёк бы в OL-fallback (цепочка bio/photo).
	candidateCheck := metadata.NewCandidateCheck(wdAdaptations.CandidateFacts)
	wikiProvider := metadata.NewWikipediaProvider(httpClient).WithCandidateCheck(candidateCheck)
	olProvider := metadata.NewOpenLibraryProvider(olHTTPClient).WithCandidateCheck(candidateCheck)
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
	metadata.Go(enricher.HealDanglingAssets)
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
	// Per-source тумблер TMDB-постеров (админка «Экранизации»): применяем
	// сохранённое значение на старте (дефолт true; без env-ключа — no-op).
	enricher.SetTMDBPostersEnabled(baCfg.TMDBPosters)

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
	workGroupReady <- workGroupCtl

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
	// Перепроверка био и фото авторов текущими гейтами (#280) — фоном, около
	// суток на 42 тыс. авторов; после рестарта продолжается с того же места.
	metadata.Go(func(c context.Context) {
		runAuthorMetaRecheck(c, pool, enricher, baCfg.BiosRPM, gatesResolver, logger)
	})

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
			TrustCFConnectingIP: cfg.TrustCFConnectingIP,
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

	// Метрики Prometheus — отдельный внутренний сервер, не основной сайт: наружу его не
	// публикуем (в публичном деплое — внутренний сайт Caddy :9180 только для сборщика).
	// Если не поднялся — пишем ошибку, приложение работает дальше.
	var metricsSrv *http.Server
	if cfg.MetricsAddr != "" {
		metrics.SetBuildInfo(effectiveVersion(cfg.Version))
		mux := http.NewServeMux()
		mux.Handle("/metrics", metrics.Handler())
		metricsSrv = &http.Server{
			Addr:              cfg.MetricsAddr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
		}
		go func() {
			logger.Info("metrics server starting", "addr", cfg.MetricsAddr)
			if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				logger.Error("metrics server failed", "err", err)
			}
		}()
	}

	go func() {
		logger.Info("http server starting", "addr", cfg.HTTPAddr, "version", effectiveVersion(cfg.Version))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server failed", "err", err)
			stop()
		}
	}()

	<-sigCtx.Done()
	logger.Info("shutting down")

	// Порядок: сначала HTTP (новые запросы и ленивое обогащение не приходят),
	// потом фоновые работы (воркеры, разовые шаги, импорт), и только потом —
	// отложенный pool.Close. Иначе воркеры писали в закрытый пул и сыпали WARN
	// «closed pool» на каждом деплое (#270). Docker ждёт 10 с до SIGKILL.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if metricsSrv != nil {
		_ = metricsSrv.Shutdown(shutdownCtx)
	}
	httpErr := srv.Shutdown(shutdownCtx)
	if !metadata.Shutdown(4 * time.Second) {
		logger.Warn("background work did not stop in time — closing anyway")
	}
	if httpErr != nil {
		return fmt.Errorf("graceful shutdown: %w", httpErr)
	}
	logger.Info("bye")
	return nil
}

// ctx — контекст синхронных шагов инициализации (загрузка настроек и т.п.).
// Фоновые работы получают свой контекст от metadata.Go — его отменяет остановка.
func ctx() context.Context { return context.Background() }

// runImportLoop — импорт INPX на старте и затем без рестарта: раз в interval
// проверяет каталог (размер/mtime) и импортирует новые или изменённые файлы,
// когда их запись закончилась (#247); файл, импорт которого упал, повторяется
// на следующей проверке. Один цикл на процесс, поэтому два импорта
// одновременно не идут. interval <= 0 — только стартовый импорт.
func runImportLoop(ctx context.Context, pool *pgxpool.Pool, imp *importer.Importer, overrideCtl *metadata.OverrideController,
	inpxRoot string, only []string, interval time.Duration, logger *slog.Logger) {
	watch := importer.NewInpxWatch(inpxRoot, only)
	files, missing, err := watch.Baseline()
	if err != nil {
		logger.Warn("startup import skipped — failed to scan inpx root", "root", inpxRoot, "err", err)
	} else {
		if len(missing) > 0 {
			logger.Warn("SKRIPTES_INPX_FILES lists files that are not in inpx root", "root", inpxRoot, "missing", missing)
		}
		if len(files) == 0 {
			logger.Info("startup import — no INPX files found", "root", inpxRoot)
		} else {
			logger.Info("startup import beginning", "count", len(files), "root", inpxRoot)
			runImportPass(ctx, pool, imp, overrideCtl, watch, files, logger)
			logger.Info("startup import finished")
		}
	}
	if interval <= 0 {
		logger.Info("inpx watch disabled — new INPX is imported on restart only")
		return
	}
	logger.Info("inpx watch started", "root", inpxRoot, "interval", interval)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		ready, err := watch.Poll()
		if err != nil {
			logger.Warn("inpx watch: scan failed", "root", inpxRoot, "err", err)
			continue
		}
		if len(ready) == 0 {
			continue
		}
		// Новые, изменённые или не импортировавшиеся из-за ошибки файлы.
		logger.Info("inpx watch: importing without restart", "files", ready)
		runImportPass(ctx, pool, imp, overrideCtl, watch, ready, logger)
		logger.Info("inpx import finished")
	}
}

// runImportPass импортирует файлы по очереди и делает общие шаги после импорта —
// только если хоть один файл реально импортировался (или упал на полпути: записи
// коммитятся по одной). На старте без нового INPX шаги не нужны: оверрайды,
// классификация и известность уже посчитаны по тем же данным (#300).
// Удачный импорт и осознанный пропуск отмечаются в watch; упавший — нет, его
// watch вернёт на следующей проверке.
func runImportPass(ctx context.Context, pool *pgxpool.Pool, imp *importer.Importer, overrideCtl *metadata.OverrideController,
	watch *importer.InpxWatch, files []string, logger *slog.Logger) {
	imported := false
	for _, f := range files {
		metrics.ImportStarted()
		stats, err := imp.Run(ctx, f) // статистика логируется изнутри Run
		var overlap *importer.OverlapError
		switch {
		case err == nil:
			watch.MarkDone(f)
			if stats.Records == 0 { // файл не менялся — Run вышел, не читая записи
				metrics.ImportFinished("unchanged", metrics.ImportResult{})
			} else {
				imported = true
				metrics.ImportFinished("ok", metrics.ImportResult{
					Records: stats.Records, BooksInserted: stats.BooksInserted,
					RecordErrors: stats.Errors, Duration: stats.Duration,
				})
			}
		case errors.As(err, &overlap):
			metrics.ImportFinished("skipped", metrics.ImportResult{})
			// Рядом лежит второй INPX той же библиотеки (#250): каждый выпуск
			// импортировался бы дважды, метаданные перезаписывали бы друг друга.
			// Не повторяем, пока файл не изменится.
			logger.Warn("INPX skipped — another INPX in use describes the same books; "+
				"keep one INPX of a library or list the one to use in SKRIPTES_INPX_FILES",
				"file", overlap.File, "collection", overlap.Collection, "collection_file", overlap.CollectionFile,
				"matched", overlap.Matched, "sampled", overlap.Sampled)
			watch.MarkDone(f)
		case ctx.Err() != nil:
			// Остановка процесса посреди импорта: записи коммитятся по одной,
			// файл не отмечен — импорт продолжится на следующем старте.
			logger.Info("import interrupted by shutdown", "file", f)
			return
		default:
			imported = true
			metrics.ImportFinished("failed", metrics.ImportResult{})
			logger.Error("import failed for file", "file", f, "err", err)
		}
	}
	if !imported {
		return
	}
	// Ре-применить ручные оверрайды полей, которые импорт ПЕРЕЗАПИСЫВАЕТ (lang) —
	// иначе ре-импорт коллекции сбросил бы правки (грабля №19).
	if n, err := overrideCtl.ReapplyAfterImport(ctx); err != nil {
		logger.Warn("reapply metadata overrides after import failed", "err", err)
	} else if n > 0 {
		logger.Info("reapplied metadata overrides after import", "count", n)
	}
	// Издания, чьи авторы после импорта ни в чём не совпадают с якорем работы, —
	// в свои работы (#285); затронутые — в оба индекса поиска.
	if touched, err := metadata.SplitAlienEditions(ctx, pool); err != nil {
		logger.Warn("split alien editions after import failed", "err", err)
	} else if len(touched) > 0 {
		syncSplitWorks(ctx, imp, touched, logger)
		logger.Info("alien editions split after import", "works", len(touched))
	}
	// Название работы — за изданиями: импорт переписывает название издания, но не
	// работы (#285). Изменённые — пересчёт типа (мог держаться на названии) и
	// таргетный ресинк works-индекса (полный ресинк импорта был раньше).
	if changed, _, err := metadata.LocalizeWorkTitles(ctx, pool); err != nil {
		logger.Warn("sync work titles after import failed", "err", err)
	} else if len(changed) > 0 {
		if _, err := metadata.ReclassifyWorkKinds(ctx, pool, changed); err != nil {
			logger.Warn("reclassify kinds after title sync failed", "err", err)
		}
		if err := imp.UpsertWorksToIndex(ctx, changed); err != nil {
			logger.Warn("works index sync after title sync failed", "err", err)
		}
		logger.Info("work titles synced after import", "works", len(changed))
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
	// Пересчитать известность авторов (authors.renown — дефолтная сортировка
	// /authors): импорт мог привезти новые издания/сигналы. Идемпотентно,
	// advisory lock сериализует с runOnce-гейтом старта.
	if n, err := imp.RecomputeAuthorRenown(ctx); err != nil {
		logger.Warn("author renown recompute after import failed", "err", err)
	} else if n > 0 {
		logger.Info("author renown recomputed after import", "authors_updated", n)
	}
	// Число работ и рейтинг авторов (сортировки /authors, #302) — сразу, не
	// дожидаясь планового пересчёта.
	if n, err := catalog.RecomputeAuthorStats(ctx, pool); err != nil {
		logger.Warn("author stats recompute after import failed", "err", err)
	} else if n > 0 {
		logger.Info("author stats recomputed after import", "authors_updated", n)
	}
}

// authorStatsInterval — как часто пересчитывать authors.book_count/max_rating.
// Их меняют импорт (пересчёт сразу после него), группировка изданий, внешний
// рейтинг, классификация сборников и ручные правки; полный пересчёт ~1 с.
const authorStatsInterval = 30 * time.Minute

// runAuthorStatsLoop — пересчёт хранимых агрегатов авторов (#302) на старте и
// раз в authorStatsInterval: сортировки «по числу книг» и «по рейтингу» идут по
// ним, отставание — не больше интервала.
func runAuthorStatsLoop(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	recompute := func() {
		start := time.Now()
		n, err := catalog.RecomputeAuthorStats(ctx, pool)
		switch {
		case err != nil && ctx.Err() == nil:
			logger.Warn("author stats recompute failed", "err", err)
		case err == nil && n > 0:
			logger.Info("author stats recomputed", "authors_updated", n, "took", time.Since(start).Round(time.Millisecond))
		}
	}
	recompute()
	t := time.NewTicker(authorStatsInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			recompute()
		}
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
// service_authors_classified_vN: один раз на апгрейде; дальше новых метит
// after-import вызов. Зеркало runOnceWorkKindClassify. Расширил правило
// (authorkind.ServiceNamePatterns) — бампни версию, иначе старые записи
// разметятся только при следующем импорте (v2: «Категория | Автор неизвестен», #297).
func runOnceServiceAuthorClassify(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	const flag = "service_authors_classified_v2"
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

// runOnceAuthorRenown — разовый пересчёт authors.renown (дефолтная сортировка
// /authors «Сначала известные», миграция 0038). Гейт ВЕРСИОНИРОВАН формулой:
// меняешь computeAuthorRenown/веса (importer/author_renown.go) — бампни ключ
// (v1 → v2), иначе на стабильном деплое пересчёт по новой формуле не запустится
// (грабля «мёртвого popularity» 1.5.x). Дальше свежесть держат after-import и
// хук воркера «Известность».
func runOnceAuthorRenown(ctx context.Context, pool *pgxpool.Pool, imp *importer.Importer, logger *slog.Logger) {
	const flag = "author_renown_computed_v1"
	var done bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_settings WHERE key = $1)`, flag).Scan(&done); err != nil {
		logger.Warn("author renown: check flag failed — skip", "err", err)
		return
	}
	if done {
		return
	}
	n, err := imp.RecomputeAuthorRenown(ctx)
	if err != nil {
		logger.Warn("author renown recompute failed — will retry next start", "err", err)
		return
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app_settings (key, value, updated_at) VALUES ($1, 'true'::jsonb, now())
		 ON CONFLICT (key) DO NOTHING`, flag); err != nil {
		logger.Warn("author renown: set flag failed (will rerun next start, idempotent)", "err", err)
	}
	logger.Info("one-time author renown recompute done", "authors_updated", n)
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
	// Пересборка во временном индексе + swap, а не ресинк на месте: поиск
	// работает всё время, и индекс сразу с настройками новой схемы (см.
	// importer.RebuildWorksIndex — баг близости слов Meili при смене полей).
	n, err := imp.RebuildWorksIndex(ctx)
	if err != nil {
		logger.Warn("works index rebuild failed — will retry next start", "err", err)
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
	// Документы пересобраны — поиск можно переключать на поля новой схемы
	// (свёртка «ё», см. importer.foldedSearchReady).
	if err := imp.ConfigureWorksIndex(ctx); err != nil {
		logger.Warn("meili configure works index after resync failed", "err", err)
	}
	logger.Info("one-time works index resync done", "count", n, "flag", flag)
}

// runOnceWorkSeriesSync — разовый бэкфилл #275: до 1.15.2 импорт не переносил в
// работу серию, которую выпуск INPX впервые проставил её изданиям (librusec
// 2026-09 — серии у 113 тыс. книг), — у ~98 тыс. работ не было серии: /books её
// не показывал, фильтр и поиск по серии не находили. Дальше то же делает каждый
// импорт (importer.syncWorkSeries). Изменённые работы досинкиваются в индекс
// порциями — UpsertWorksToIndex грузит документы одним запросом.
func runOnceWorkSeriesSync(ctx context.Context, pool *pgxpool.Pool, imp *importer.Importer, logger *slog.Logger) {
	const flag = "work_series_synced_v1"
	var done bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_settings WHERE key = $1)`, flag).Scan(&done); err != nil {
		logger.Warn("work series sync: check flag failed — skip", "err", err)
		return
	}
	if done {
		return
	}
	ids, err := imp.SyncWorkSeries(ctx)
	if err != nil {
		logger.Warn("work series sync failed — will retry next start", "err", err)
		return
	}
	const batch = 5000
	for start := 0; start < len(ids); start += batch {
		end := min(start+batch, len(ids))
		if err := imp.UpsertWorksToIndex(ctx, ids[start:end]); err != nil {
			// Флаг не ставим: следующий старт пересчитает (PG уже согласован —
			// SyncWorkSeries вернёт пусто) — поэтому досинкиваем весь индекс.
			logger.Warn("work series sync: works index upsert failed — full resync next start", "err", err)
			if _, derr := pool.Exec(ctx, `DELETE FROM app_settings WHERE key = $1`, importer.WorksIndexSyncedFlagKey()); derr != nil {
				logger.Warn("work series sync: reset works index flag failed", "err", derr)
			}
			return
		}
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app_settings (key, value, updated_at) VALUES ($1, 'true'::jsonb, now())
		 ON CONFLICT (key) DO NOTHING`, flag); err != nil {
		logger.Warn("work series sync: set flag failed (will rerun next start, idempotent)", "err", err)
	}
	logger.Info("one-time work series sync done", "works", len(ids))
}

// runOnceWorkTitleLocalize — разовый backfill: локализует works.title на
// доминирующий язык библиотеки для работ, у которых есть издание в этом языке
// (см. metadata.LocalizeWorkTitles). Чинит «перевод+оригинал слиты, каноникой
// стало иноязычное издание» — карточка и works-поиск показывали английский
// заголовок при русских изданиях. Изменённые работы таргетно ресинкаются в
// works-индекс (поиск по локализованному названию начинает находить).
// Гейт app_settings.work_title_localized_vN: один раз на апгрейде, дальше no-op
// (новые такие работы локализует группировка в apply). Зовётся ПОСЛЕ
// runOnceWorksIndexSync — индекс уже сконфигурирован/наполнен. Сменил правило
// выбора названия — бампни версию (v2: самое частое название изданий, #306;
// v3: работа из одного издания на любом языке — его название, #285).
func runOnceWorkTitleLocalize(ctx context.Context, pool *pgxpool.Pool, imp *importer.Importer, logger *slog.Logger) {
	const flag = "work_title_localized_v3"
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
	// Эвристический тип (сборник/антология) мог держаться на прежнем названии.
	if len(changed) > 0 {
		if _, err := metadata.ReclassifyWorkKinds(ctx, pool, changed); err != nil {
			logger.Warn("work title localize: reclassify kinds failed", "err", err)
		}
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

// runOnceGenreAliases — разовое слияние жанров-алиасов с кодами нашего словаря
// (genres.MergeAliases: книги, избранное, скрытые жанры, ручные правки; adv_all →
// adventure, #286) + ресинк works-индекса затронутых работ. Новые записи импорт
// сводит сам (genres.CanonicalCodes). Гейт genre_aliases_merged_vN — бампать при
// пополнении aliases.json.
func runOnceGenreAliases(ctx context.Context, pool *pgxpool.Pool, imp *importer.Importer, logger *slog.Logger) {
	const flag = "genre_aliases_merged_v1"
	var done bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_settings WHERE key = $1)`, flag).Scan(&done); err != nil {
		logger.Warn("genre aliases: check flag failed — skip", "err", err)
		return
	}
	if done {
		return
	}
	works, merged, err := genres.MergeAliases(ctx, pool)
	if err != nil {
		logger.Warn("genre aliases merge failed — will retry next start", "err", err)
		return
	}
	if len(works) > 0 {
		if err := imp.UpsertWorksToIndex(ctx, works); err != nil {
			logger.Warn("genre aliases: works index resync failed — retry next start", "err", err)
			return
		}
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app_settings (key, value, updated_at) VALUES ($1, 'true'::jsonb, now())
		 ON CONFLICT (key) DO NOTHING`, flag); err != nil {
		logger.Warn("genre aliases: set flag failed (idempotent rerun)", "err", err)
	}
	logger.Info("one-time genre aliases merge done", "codes", merged, "works", len(works))
}

// runOnceForceReimport — сменились правила, которые применяет только импорт
// INPX (межавторские серии: порог доминирования 0,8 и служебные авторы, #298) —
// сбросить хэш коллекций, чтобы ближайший импорт прошёл полностью, а не
// пропустил неизменный файл. Импорт идемпотентен (~1 ч на 470 тыс. книг).
// Гейт reimport_series_rules_vN — бампать при следующей такой смене правил.
func runOnceForceReimport(ctx context.Context, pool *pgxpool.Pool, logger *slog.Logger) {
	const flag = "reimport_series_rules_v1"
	var done bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_settings WHERE key = $1)`, flag).Scan(&done); err != nil {
		logger.Warn("force reimport: check flag failed — skip", "err", err)
		return
	}
	if done {
		return
	}
	tag, err := pool.Exec(ctx, `UPDATE collections SET last_inpx_hash = NULL WHERE last_inpx_hash IS NOT NULL`)
	if err != nil {
		logger.Warn("force reimport: reset inpx hash failed — will retry next start", "err", err)
		return
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app_settings (key, value, updated_at) VALUES ($1, 'true'::jsonb, now())
		 ON CONFLICT (key) DO NOTHING`, flag); err != nil {
		logger.Warn("force reimport: set flag failed (idempotent rerun)", "err", err)
	}
	logger.Info("one-time full reimport scheduled (series rules changed)", "collections", tag.RowsAffected())
}

// runOnceRegroupTitleConflicts — разовый разбор работ, склеенных до гейтов
// Tier-2 #279 (разные названия одного языка без src-свидетельства, разные тома,
// заглушка «(no data for original title)»): сначала заглушки src_title в базе
// обнуляются, затем такие работы идут в RegroupWorks — неякорные издания в
// синглтоны, found-lookups сброшены, Tier-1 собирает законные склейки обратно.
// Гейт tier2_title_conflicts_regrouped_v1.
func runOnceRegroupTitleConflicts(ctx context.Context, pool *pgxpool.Pool, wg *metadata.WorkGroupController, logger *slog.Logger) {
	const flag = "tier2_title_conflicts_regrouped_v1"
	var done bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_settings WHERE key = $1)`, flag).Scan(&done); err != nil {
		logger.Warn("regroup title conflicts: check flag failed — skip", "err", err)
		return
	}
	if done || wg == nil {
		return
	}
	stubs, err := metadata.CleanStubSrcTitles(ctx, pool)
	if err != nil {
		logger.Warn("regroup title conflicts: clean stub src titles failed — will retry next start", "err", err)
		return
	}
	works, err := metadata.TitleConflictWorks(ctx, pool)
	if err != nil {
		logger.Warn("regroup title conflicts: find works failed — will retry next start", "err", err)
		return
	}
	const batch = 500
	split := 0
	for i := 0; i < len(works); i += batch {
		res, err := wg.RegroupWorks(ctx, works[i:min(i+batch, len(works))], false)
		if err != nil {
			logger.Warn("regroup title conflicts failed — will retry next start", "done", i, "total", len(works), "err", err)
			return
		}
		split += res.EditionsSplit
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app_settings (key, value, updated_at) VALUES ($1, 'true'::jsonb, now())
		 ON CONFLICT (key) DO NOTHING`, flag); err != nil {
		logger.Warn("regroup title conflicts: set flag failed (idempotent rerun)", "err", err)
	}
	logger.Info("one-time regroup of title-conflict works done",
		"stub_src_titles", stubs, "works", len(works), "editions_split", split)
}

// authorMetaRecheckKey — состояние перепроверки био и фото авторов в
// app_settings: {"since": начало, "done": завершена}. Бампнуть версию —
// запустить перепроверку заново (например, после новых гейтов матчинга).
// v2 (1.16.1): v1 очистила био у Дюма (страница неоднозначности), Херберта и
// Зузака (другая передача фамилии в Википедии). v3: после разбора ошибок
// (case study #280) — новый поиск статьи, латинское имя, политика приёма; идёт по
// ВСЕМ авторам с книгами (решение владельца 2026-10-02), ~140 тыс., ~3,5 суток.
// v4 (1.19.1): v3 на первых 300 авторах очистила Достоевского и заменила
// Тургеневу русскую био на английскую — рядом с основной статьёй стояли
// одноимённые с уточнением, и поиск уходил на строгий путь; заново и с начала.
const authorMetaRecheckKey = "author_meta_recheck_v4"

// runAuthorMetaRecheck — перепроверка биографий и фото авторов текущими гейтами
// матчинга (metadata.AuthorRechecker, #280): подтверждённое остаётся, чужое
// заменяется или очищается, изменения — в author_meta_recheck. Проходы
// повторяются, пока у кого-то сбоит источник (раз в 30 минут). Не идёт, если
// обогащение авторов выключено в админке.
func runAuthorMetaRecheck(ctx context.Context, pool *pgxpool.Pool, enricher *metadata.Enricher, rpm int,
	gates *settings.EnrichmentGateResolver, logger *slog.Logger) {
	var state struct {
		Since time.Time `json:"since"`
		Done  bool      `json:"done"`
	}
	var raw []byte
	err := pool.QueryRow(ctx, `SELECT value FROM app_settings WHERE key = $1`, authorMetaRecheckKey).Scan(&raw)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		state.Since = time.Now().UTC()
	case err != nil:
		logger.Warn("author recheck: read state failed — skip", "err", err)
		return
	default:
		if err := json.Unmarshal(raw, &state); err != nil {
			logger.Warn("author recheck: bad state — skip", "err", err)
			return
		}
	}
	if state.Done {
		return
	}
	save := func() {
		b, _ := json.Marshal(state)
		if _, err := pool.Exec(ctx, `
			INSERT INTO app_settings (key, value, updated_at) VALUES ($1, $2, now())
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`,
			authorMetaRecheckKey, b); err != nil {
			logger.Warn("author recheck: save state failed", "err", err)
		}
	}
	save()
	if rpm <= 0 {
		rpm = settings.DefaultBioAdaptationConfig().BiosRPM
	}
	// Два автора одновременно: один автор — несколько последовательных запросов к
	// Википедии и Wikidata (~7 с), в один поток 140 тыс. авторов шли бы недели;
	// темп держит RPM. Больше двух потоков Википедия отвечает 429 (сухой прогон).
	r := metadata.NewAuthorRechecker(pool, enricher, rpm, logger).WithAllAuthors().WithWorkers(2)
	logger.Info("author recheck: started", "since", state.Since, "rpm", rpm)
	for pass := 1; ; pass++ {
		if gates != nil && gates.Gates().AuthorDisabled {
			logger.Info("author recheck: author enrichment is disabled — paused")
		} else {
			st, err := r.Pass(ctx, state.Since)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				logger.Warn("author recheck: pass failed", "pass", pass, "err", err)
			}
			logger.Info("author recheck: pass done", "pass", pass, "checked", st.Checked, "deferred", st.Deferred,
				"bio_kept", st.BioKept, "bio_new", st.BioNew, "bio_cleared", st.BioClear,
				"photo_kept", st.PhotoKept, "photo_new", st.PhotoNew, "photo_cleared", st.PhotoClr)
			if err == nil && st.Deferred == 0 {
				state.Done = true
				save()
				logger.Info("author recheck: done")
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Minute):
		}
	}
}

// runOnceSplitAlienEditions — разовый вынос изданий, у которых нет общих авторов
// с якорем своей работы (metadata.SplitAlienEditions, #285; прод — 174 издания).
// Дальше то же делают шаги после импорта. Гейт alien_editions_split_v1.
func runOnceSplitAlienEditions(ctx context.Context, pool *pgxpool.Pool, imp *importer.Importer, logger *slog.Logger) {
	const flag = "alien_editions_split_v1"
	var done bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_settings WHERE key = $1)`, flag).Scan(&done); err != nil {
		logger.Warn("split alien editions: check flag failed — skip", "err", err)
		return
	}
	if done {
		return
	}
	touched, err := metadata.SplitAlienEditions(ctx, pool)
	if len(touched) > 0 {
		syncSplitWorks(ctx, imp, touched, logger)
	}
	if err != nil {
		logger.Warn("split alien editions failed — will retry next start", "err", err)
		return
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app_settings (key, value, updated_at) VALUES ($1, 'true'::jsonb, now())
		 ON CONFLICT (key) DO NOTHING`, flag); err != nil {
		logger.Warn("split alien editions: set flag failed (idempotent rerun)", "err", err)
	}
	logger.Info("one-time alien editions split done", "works", len(touched))
}

// syncSplitWorks — поиск после выноса изданий: works-индекс (старые и новые
// работы) и work_id изданий в books-индексе (OPDS схлопывает по нему).
func syncSplitWorks(ctx context.Context, imp *importer.Importer, works []int64, logger *slog.Logger) {
	if err := imp.UpsertWorksToIndex(ctx, works); err != nil {
		logger.Warn("works index sync after edition split failed", "err", err)
	}
	if _, err := imp.ResyncWorkIDsFor(ctx, works); err != nil {
		logger.Warn("work_id resync after edition split failed", "err", err)
	}
}

// runOnceSrcLangCanonical — разовая канонизация books.src_lang к ISO 639-1
// (metadata.CanonicalizeSrcLangs: spa→es, jp→ja, «английски»→en, мусор → NULL,
// #287) + таргетный ресинк works-индекса изменённых работ (фасет «Язык
// оригинала»). Гейт src_lang_canonical_v1; новые значения канонизирует запись
// (EnsureEditionMeta). Поменял таблицу langcode — бампни версию.
func runOnceSrcLangCanonical(ctx context.Context, pool *pgxpool.Pool, imp *importer.Importer, logger *slog.Logger) {
	const flag = "src_lang_canonical_v1"
	var done bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM app_settings WHERE key = $1)`, flag).Scan(&done); err != nil {
		logger.Warn("src_lang canonicalize: check flag failed — skip", "err", err)
		return
	}
	if done {
		return
	}
	works, err := metadata.CanonicalizeSrcLangs(ctx, pool)
	if err != nil {
		logger.Warn("src_lang canonicalize failed — will retry next start", "err", err)
		return
	}
	// Ресинк ДО флага: упадёт — на следующем старте значения уже канонические
	// (works пуст), и индекс досинкнётся полным ресинком как фолбэк.
	if len(works) > 0 {
		if err := imp.UpsertWorksToIndex(ctx, works); err != nil {
			logger.Warn("src_lang canonicalize: works index resync failed — full resync fallback", "err", err)
			if _, rerr := imp.ResyncWorksIndex(ctx); rerr != nil {
				logger.Warn("src_lang canonicalize: full works index resync failed — retry next start", "err", rerr)
				return
			}
		}
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app_settings (key, value, updated_at) VALUES ($1, 'true'::jsonb, now())
		 ON CONFLICT (key) DO NOTHING`, flag); err != nil {
		logger.Warn("src_lang canonicalize: set flag failed (idempotent rerun)", "err", err)
	}
	logger.Info("one-time src_lang canonicalization done", "works", len(works))
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

func newLogger(level, format string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	// ReplaceAttr: ключи API из URL в ошибках net/http не должны попадать в журнал.
	opts := &slog.HandlerOptions{Level: lvl, ReplaceAttr: logredact.ReplaceAttr}
	var h slog.Handler
	if format == "text" {
		h = slog.NewTextHandler(os.Stdout, opts)
	} else {
		h = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(shutdownQuietHandler{Handler: h, stopping: metadata.Stopping})
}

// shutdownQuietHandler — ожидаемые обрывы пишутся как INFO, а не WARN/ERROR,
// чтобы алерт на поток предупреждений не будил от штатной работы:
//   - «context canceled» — всегда: отмену делает сам процесс (остановка, пауза
//     воркера группировки под разбор — прод 1.15.5, клиент закрыл запрос);
//   - «closed pool» — только во время остановки (#270): в другое время это сбой.
type shutdownQuietHandler struct {
	slog.Handler
	stopping func() bool
}

func (h shutdownQuietHandler) Handle(ctx context.Context, r slog.Record) error {
	if r.Level > slog.LevelInfo && recordHasExpectedErr(r, h.stopping()) {
		r = r.Clone()
		r.Level = slog.LevelInfo
	}
	return h.Handler.Handle(ctx, r)
}

func (h shutdownQuietHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return shutdownQuietHandler{Handler: h.Handler.WithAttrs(attrs), stopping: h.stopping}
}

func (h shutdownQuietHandler) WithGroup(name string) slog.Handler {
	return shutdownQuietHandler{Handler: h.Handler.WithGroup(name), stopping: h.stopping}
}

func recordHasExpectedErr(r slog.Record, stopping bool) bool {
	found := false
	r.Attrs(func(a slog.Attr) bool {
		err, ok := a.Value.Any().(error)
		if !ok {
			return true
		}
		if errors.Is(err, context.Canceled) || (stopping && strings.Contains(err.Error(), "closed pool")) {
			found = true
			return false
		}
		return true
	})
	return found
}
