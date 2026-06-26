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
	Collections CollectionsDeps
	Download    DownloadDeps
	History     HistoryDeps
	Metadata    MetadataDeps
	Kindle      KindleDeps
	Adaptations AdaptationsDeps
	Settings    SettingsDeps
	Content     ContentDeps
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
				// bookGate — глобальный hard-block скрытого контента на
				// маршрутах «по id книги» (404 даже по прямой ссылке).
				// Бесплатен, если админ ничего не скрыл (дефолт).
				bookGate := requireBookVisible(d.Content, d.Books)
				r.Get("/auth/me", handleMe(d.Auth))
				// Self-management. /api/me — обновление своего профиля
				// (display_name, email), /api/me/password — смена своего
				// пароля с верификацией текущего.
				r.Patch("/me", handleUpdateMe(d.Auth))
				r.Patch("/me/password", handleChangeMyPassword(d.Auth))
				if d.Books.Service != nil {
					r.Get("/books", handleListBooks(d.Books, d.History, d.Content))
					r.With(bookGate).Get("/books/{id}", handleGetBook(d.Books, d.History, d.Metadata))
					// Карточка логической книги по works.id. Видимость скрытого
					// контента обрабатывает GetWork (404, если все издания скрыты) —
					// отдельный bookGate не нужен (он по book_id).
					r.Get("/works/{id}", handleGetWork(d.Books, d.History, d.Metadata, d.Content))
				}
				if d.Adaptations.Service != nil {
					r.With(bookGate).Get("/books/{id}/adaptations", handleListAdaptations(d.Adaptations, d.Books, d.Metadata))
				}
				if d.Metadata.Service != nil {
					r.Get("/covers/{name}", handleCover(d.Metadata))
					// On-demand обложка по id книги (извлечение из fb2 на лету).
					r.With(bookGate).Get("/covers/book/{id}", handleCoverByID(d.Metadata))
				}
				if d.Books.Service != nil || d.Catalog.Service != nil {
					r.Get("/search/suggest", handleSuggest(d.Books, d.Catalog, d.History, d.Content))
				}
				if d.Download.Books != nil && d.Download.Converter != nil {
					r.With(bookGate).Get("/books/{id}/download", handleDownload(d.Download, d.History))
				}
				if d.Catalog.Service != nil {
					// /authors — список авторов с фильтрами (раздел «Авторы»);
					// /authors/{id} — карточка одного автора. Разные chi-маршруты,
					// сосуществуют (статический путь vs. path-параметр).
					r.Get("/authors", handleListAuthors(d.Catalog, d.Content))
					r.Get("/authors/{id}", handleGetAuthor(d.Catalog, d.History, d.Metadata, d.Content))
					r.Get("/authors/{id}/series", handleAuthorSeries(d.Catalog)) // серии автора (пикер переноса)
					r.Get("/series/{id}", handleGetSeries(d.Catalog, d.History, d.Content, d.Metadata))
					r.Get("/genres", handleListGenres(d.Catalog))
					r.Get("/languages", handleListLanguages(d.Catalog))
				}
				// Раздел «Контент» (персональные скрытые жанры/языки) +
				// объединённый effective-набор для панели фильтров.
				if d.Content.Resolver != nil {
					r.Get("/me/content", handleGetMeContent(d.Content))
					r.Put("/me/content", handleUpdateMeContent(d.Content))
					r.Get("/content/effective", handleEffectiveContent(d.Content))
				}
				// Раздел «Внешний вид» — персональные визуальные настройки.
				if d.Settings.Store != nil {
					r.Get("/me/appearance", handleGetMeAppearance(d.Settings))
					r.Put("/me/appearance", handleUpdateMeAppearance(d.Settings))
					// Профиль: настройки отложенных запросов оценки.
					r.Get("/me/rating-prompts", handleGetMeRatingPrompts(d.Settings))
					r.Put("/me/rating-prompts", handleUpdateMeRatingPrompts(d.Settings))
				}
				if d.History.Service != nil {
					r.Post("/books/{id}/favorite", handleAddFavorite(d.History))
					r.Delete("/books/{id}/favorite", handleRemoveFavorite(d.History))
					r.Post("/authors/{id}/favorite", handleAddFavoriteAuthor(d.History))
					r.Delete("/authors/{id}/favorite", handleRemoveFavoriteAuthor(d.History))
					r.Post("/series/{id}/favorite", handleAddFavoriteSeries(d.History))
					r.Delete("/series/{id}/favorite", handleRemoveFavoriteSeries(d.History))
					// Избранные жанры — раздел «Жанры» (закрепление сверху).
					r.Post("/genres/{id}/favorite", handleAddFavoriteGenre(d.History))
					r.Delete("/genres/{id}/favorite", handleRemoveFavoriteGenre(d.History))
					// Пользовательские оценки книг (work-level, шкала 1–5).
					r.Put("/works/{id}/rating", handleSetRating(d.History))
					r.Delete("/works/{id}/rating", handleRemoveRating(d.History))
					// Отложенные запросы оценки («Оцените прочитанное»).
					r.Get("/me/rating-prompts/feed", handleRateablePrompts(d.History, d.Settings))
					r.Post("/works/{id}/rating-prompt/dismiss", handleDismissRatingPrompt(d.History))
					r.Post("/works/{id}/rating-prompt/snooze", handleSnoozeRatingPrompt(d.History, d.Settings))
					r.Get("/me/favorites", handleListFavorites(d.History))
					r.Get("/me/recent", handleRecentViews(d.History))
					// Главная: «Продолжить чтение» + «Новинки по подпискам».
					r.Get("/me/continue-reading", handleContinueReading(d.History))
					r.Get("/me/feed/subscriptions", handleSubscriptionFeed(d.History))
					// Скрыть работу из ленты новинок («не интересно»).
					r.Post("/me/feed/dismiss", handleDismissFeedItem(d.History))
					// Явная отметка «прочитано» (кнопка на карточке книги +
					// auto-mark из ридера при дочитывании). Основной сигнал
					// для read_count в статистике автора/серии.
					r.Post("/books/{id}/read", handleMarkRead(d.History))
					r.Delete("/books/{id}/read", handleUnmarkRead(d.History))
					// Позиция чтения для in-browser ридера (epub-cfi).
					r.Get("/books/{id}/position", handleGetPosition(d.History))
					r.Put("/books/{id}/position", handleSavePosition(d.History))
				}
				// Личные полки (коллекции) — раздел «Жанры». CRUD полки +
				// членство книг. Все ручки гейтят владение по userID.
				if d.Collections.Service != nil {
					r.Get("/me/collections", handleListCollections(d.Collections))
					r.Post("/me/collections", handleCreateCollection(d.Collections))
					r.Patch("/me/collections/{id}", handleRenameCollection(d.Collections))
					r.Delete("/me/collections/{id}", handleDeleteCollection(d.Collections))
					r.Get("/me/collections/{id}", handleListCollectionBooks(d.Collections, d.History))
					r.Post("/me/collections/{id}/books/{bookId}", handleAddBookToCollection(d.Collections))
					r.Delete("/me/collections/{id}/books/{bookId}", handleRemoveBookFromCollection(d.Collections))
					// Членство книги в полках — для индикации на карточке.
					r.Get("/books/{id}/collections", handleCollectionsForBook(d.Collections))
				}
				// In-browser ридер: тот же путь конвертации что и /download,
				// но без Content-Disposition: attachment и без записи в reads.
				if d.Download.Books != nil && d.Download.Converter != nil {
					r.With(bookGate).Get("/books/{id}/epub", handleEpub(d.Download))
				}
				if d.Kindle.Service != nil {
					r.Get("/me/kindle-targets", handleListKindleTargets(d.Kindle))
					r.Post("/me/kindle-targets", handleAddKindleTarget(d.Kindle))
					r.Patch("/me/kindle-targets/{id}", handleUpdateKindleTarget(d.Kindle))
					r.Delete("/me/kindle-targets/{id}", handleDeleteKindleTarget(d.Kindle))
					if d.Kindle.Books != nil && d.Kindle.Converter != nil {
						r.With(bookGate).Post("/books/{id}/send-to-kindle", handleSendToKindle(d.Kindle))
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
				// Раздел «Кэш обложек»: рантайм-настройки + статистика + очистка.
				if d.Settings.Store != nil {
					r.Get("/admin/cover-cache", handleGetCoverSettings(d.Settings))
					r.Put("/admin/cover-cache", handleUpdateCoverSettings(d.Settings))
					r.Post("/admin/cover-cache/clear", handleClearCoverCache(d.Settings))
					r.Post("/admin/cover-cache/clear-posters", handleClearPosterCache(d.Settings))
					r.Post("/admin/cover-cache/clear-photos", handleClearPhotoCache(d.Settings))
					r.Post("/admin/cover-cache/prewarm", handlePrewarmNow(d.Settings))
					r.Post("/admin/cover-cache/prewarm/stop", handlePrewarmStop(d.Settings))
					// Раздел «Год издания»: дозаполнение written_year из внешних
					// источников (OpenLibrary/Wikidata) — настройки + воркер.
					r.Get("/admin/year-enrichment", handleGetYearEnrichment(d.Settings))
					r.Put("/admin/year-enrichment", handleUpdateYearEnrichment(d.Settings))
					r.Post("/admin/year-enrichment/run", handleYearBackfillNow(d.Settings))
					r.Post("/admin/year-enrichment/stop", handleYearBackfillStop(d.Settings))
					r.Post("/admin/year-enrichment/reset-failed", handleResetYearLookups(d.Settings))
					// Раздел «Обложки — внешние»: дозаполнение cover_path из
					// OpenLibrary/Google Books — настройки + воркер.
					r.Get("/admin/cover-enrichment", handleGetCoverEnrichment(d.Settings))
					r.Put("/admin/cover-enrichment", handleUpdateCoverEnrichment(d.Settings))
					r.Post("/admin/cover-enrichment/run", handleCoverBackfillNow(d.Settings))
					r.Post("/admin/cover-enrichment/stop", handleCoverBackfillStop(d.Settings))
					r.Post("/admin/cover-enrichment/reset-failed", handleResetCoverLookups(d.Settings))
					// Внешний рейтинг (Google Books/OpenLibrary) — фоновый воркер
					// дозаполнения books.external_rating.
					r.Get("/admin/external-rating", handleGetExternalRating(d.Settings))
					r.Put("/admin/external-rating", handleUpdateExternalRating(d.Settings))
					r.Post("/admin/external-rating/run", handleExternalRatingNow(d.Settings))
					r.Post("/admin/external-rating/stop", handleExternalRatingStop(d.Settings))
					r.Post("/admin/external-rating/reset-failed", handleResetRatingLookups(d.Settings))
					// Раздел «Биографии + Экранизации — внешние»: фоновые воркеры
					// био/фото авторов (Wikipedia/OL) и экранизаций книг (Wikidata).
					r.Get("/admin/bio-adaptation-enrichment", handleGetBioAdaptation(d.Settings))
					r.Put("/admin/bio-adaptation-enrichment", handleUpdateBioAdaptation(d.Settings))
					r.Post("/admin/bio-adaptation-enrichment/bios/run", handleBioBackfillNow(d.Settings))
					r.Post("/admin/bio-adaptation-enrichment/bios/stop", handleBioBackfillStop(d.Settings))
					r.Post("/admin/bio-adaptation-enrichment/adaptations/run", handleAdaptationBackfillNow(d.Settings))
					r.Post("/admin/bio-adaptation-enrichment/adaptations/stop", handleAdaptationBackfillStop(d.Settings))
					// Раздел «Группировка изданий»: слияние fb2-файлов в логические
					// книги (works) — Tier-1 локально + Tier-2 внешние Work ID +
					// ручной split/merge.
					r.Get("/admin/work-grouping", handleGetWorkGrouping(d.Settings))
					r.Put("/admin/work-grouping", handleUpdateWorkGrouping(d.Settings))
					r.Post("/admin/work-grouping/run", handleWorkGroupingNow(d.Settings))
					r.Post("/admin/work-grouping/stop", handleWorkGroupingStop(d.Settings))
					r.Post("/admin/works/split", handleWorkSplit(d.Settings))
					r.Post("/admin/works/merge", handleWorkMerge(d.Settings))
					// Локальные оверрайды метаданных (ручная корректура каталога).
					r.Get("/admin/overrides", handleListOverrides(d.Settings))
					r.Post("/admin/overrides", handleSetOverride(d.Settings))
					r.Delete("/admin/overrides", handleRevertOverride(d.Settings))
					r.Post("/admin/overrides/revert-all", handleRevertAllOverrides(d.Settings))
				}
				// Раздел «Контент»: глобально скрытые жанры/языки (для всех
				// пользователей сервера).
				if d.Content.Resolver != nil {
					r.Get("/admin/content", handleGetAdminContent(d.Content))
					r.Put("/admin/content", handleUpdateAdminContent(d.Content))
				}
				// «Выключатели» lazy-обогащения по типам (режим «Выкл» на
				// странице «Фоновые операции»).
				if d.Metadata.Gates != nil {
					r.Get("/admin/enrichment-gates", handleGetEnrichmentGates(d.Metadata.Gates))
					r.Put("/admin/enrichment-gates", handleUpdateEnrichmentGates(d.Metadata.Gates))
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
