package settings_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/db"
	"github.com/skriptes/skriptes/backend/internal/settings"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// TestSettings_CoverRoundTrip — пустая БД → дефолты; после SetCover →
// сохранённые значения; повторный SetCover перезаписывает (upsert).
func TestSettings_CoverRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startSettingsPG(t, ctx)
	store := settings.New(pool)

	// Нет оверрайда → дефолты.
	got, err := store.Cover(ctx)
	require.NoError(t, err)
	require.Equal(t, settings.DefaultCoverConfig(), got)

	// Сохранили — читается обратно.
	want := settings.CoverConfig{CacheMaxMB: 4096, CacheMinFreeMB: 512, Prewarm: true}
	require.NoError(t, store.SetCover(ctx, want))
	got, err = store.Cover(ctx)
	require.NoError(t, err)
	require.Equal(t, want, got)

	// Upsert — перезапись.
	want2 := settings.CoverConfig{CacheMaxMB: 16384, CacheMinFreeMB: 2048, Prewarm: false}
	require.NoError(t, store.SetCover(ctx, want2))
	got, err = store.Cover(ctx)
	require.NoError(t, err)
	require.Equal(t, want2, got)
}

func TestCoverConfig_Intensity(t *testing.T) {
	require.Equal(t, 1, settings.CoverConfig{Intensity: settings.IntensityLow}.IntensityWorkers())
	require.Equal(t, 2, settings.CoverConfig{Intensity: settings.IntensityMedium}.IntensityWorkers())
	require.Equal(t, 6, settings.CoverConfig{Intensity: settings.IntensityHigh}.IntensityWorkers())
	require.Equal(t, 2, settings.CoverConfig{Intensity: "bogus"}.IntensityWorkers(), "неизвестное → medium")

	require.Equal(t, 250*time.Millisecond, settings.CoverConfig{Intensity: settings.IntensityLow}.IntensityDelay())
	require.Equal(t, time.Duration(0), settings.CoverConfig{Intensity: settings.IntensityHigh}.IntensityDelay())
	require.Equal(t, time.Duration(0), settings.CoverConfig{Intensity: settings.IntensityMedium}.IntensityDelay())
}

func TestSettings_YearEnrichmentRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startSettingsPG(t, ctx)
	store := settings.New(pool)

	// Нет оверрайда → дефолты (воркер выключен — opt-in).
	got, err := store.YearEnrichment(ctx)
	require.NoError(t, err)
	require.Equal(t, settings.DefaultYearEnrichmentConfig(), got)
	require.False(t, got.Enabled, "по умолчанию воркер выключен")

	// Сохранили — читается обратно (upsert).
	want := settings.YearEnrichmentConfig{
		Enabled: true, OpenLibrary: true, Wikidata: false,
		OpenLibraryRPM: 30, WikidataRPM: 10, NotFoundRetryDays: 30, ErrorRetryHours: 6,
	}
	require.NoError(t, store.SetYearEnrichment(ctx, want))
	got, err = store.YearEnrichment(ctx)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestSettings_CoverEnrichmentRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startSettingsPG(t, ctx)
	store := settings.New(pool)

	// Нет оверрайда → дефолты (воркер выключен — opt-in).
	got, err := store.CoverEnrichment(ctx)
	require.NoError(t, err)
	require.Equal(t, settings.DefaultCoverEnrichmentConfig(), got)
	require.False(t, got.Enabled, "по умолчанию воркер выключен")
	require.False(t, got.WholeCollection, "по умолчанию режим фолбэка")

	// Сохранили — читается обратно (upsert).
	want := settings.CoverEnrichmentConfig{
		Enabled: true, OpenLibrary: true, GoogleBooks: false, WholeCollection: true,
		OpenLibraryRPM: 30, GoogleBooksRPM: 15, NotFoundRetryDays: 30, ErrorRetryHours: 6,
	}
	require.NoError(t, store.SetCoverEnrichment(ctx, want))
	got, err = store.CoverEnrichment(ctx)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestSettings_ExternalRatingRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startSettingsPG(t, ctx)
	store := settings.New(pool)

	// Нет оверрайда → дефолты (воркер выключен — opt-in, оба источника, фолбэк).
	got, err := store.ExternalRating(ctx)
	require.NoError(t, err)
	require.Equal(t, settings.DefaultExternalRatingConfig(), got)
	require.False(t, got.Enabled, "по умолчанию воркер выключен")
	require.False(t, got.WholeCollection, "по умолчанию только пробелы")

	// Сохранили выбор «только Google Books» + вся коллекция — читается обратно.
	want := settings.ExternalRatingConfig{
		Enabled: true, GoogleBooks: true, OpenLibrary: false, WholeCollection: true,
		GoogleBooksRPM: 30, OpenLibraryRPM: 0, NotFoundRetryDays: 30, ErrorRetryHours: 6,
	}
	require.NoError(t, store.SetExternalRating(ctx, want))
	got, err = store.ExternalRating(ctx)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

func TestSettings_BioAdaptationRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startSettingsPG(t, ctx)
	store := settings.New(pool)

	// Нет оверрайда → дефолты (оба воркера выключены — opt-in).
	got, err := store.BioAdaptation(ctx)
	require.NoError(t, err)
	require.Equal(t, settings.DefaultBioAdaptationConfig(), got)
	require.False(t, got.Bios)
	require.False(t, got.Adaptations)

	// Сохранили — читается обратно (upsert).
	want := settings.BioAdaptationConfig{
		Bios: true, Adaptations: false, BiosRPM: 15, AdaptationsRPM: 5,
	}
	require.NoError(t, store.SetBioAdaptation(ctx, want))
	got, err = store.BioAdaptation(ctx)
	require.NoError(t, err)
	require.Equal(t, want, got)
}

// TestSettings_ContentRoundTrip — глобальные и персональные настройки
// видимости: дефолты на пустой БД, upsert+нормализация (дедуп/сортировка),
// объединение admin ∪ user через ContentResolver.
func TestSettings_ContentRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startSettingsPG(t, ctx)
	store := settings.New(pool)

	// Глобальный: нет оверрайда → дефолт (ничего не скрыто).
	got, err := store.Content(ctx)
	require.NoError(t, err)
	require.Equal(t, settings.DefaultContentConfig(), got)

	// Сохранили с дублями/пустыми — normalize чистит и сортирует.
	require.NoError(t, store.SetContent(ctx, settings.ContentConfig{
		HiddenGenres:    []string{"erotica", "", "erotica", "porno"},
		HiddenLanguages: []string{"bg"},
	}))
	got, err = store.Content(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"erotica", "porno"}, got.HiddenGenres)
	require.Equal(t, []string{"bg"}, got.HiddenLanguages)

	// Персональные настройки требуют пользователя (FK user_settings.user_id).
	var uid int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO users (email, display_name, password_hash, role)
		VALUES ('u@example.test', 'U', 'x', 'user') RETURNING id`).Scan(&uid))

	ug, err := store.UserContent(ctx, uid)
	require.NoError(t, err)
	require.Equal(t, settings.DefaultContentConfig(), ug)

	require.NoError(t, store.SetUserContent(ctx, uid, settings.ContentConfig{HiddenGenres: []string{"detective"}}))
	ug, err = store.UserContent(ctx, uid)
	require.NoError(t, err)
	require.Equal(t, []string{"detective"}, ug.HiddenGenres)
	require.Equal(t, []string{}, ug.HiddenLanguages)

	// Resolver: Exclusions = admin ∪ user; AdminHides — только глобальные.
	r := settings.NewContentResolver(store)
	require.NoError(t, r.Load(ctx))
	g, l := r.Exclusions(ctx, uid)
	require.ElementsMatch(t, []string{"erotica", "porno", "detective"}, g)
	require.ElementsMatch(t, []string{"bg"}, l)
	require.True(t, r.AdminHides([]string{"erotica"}, "en"))
	require.False(t, r.AdminHides([]string{"detective"}, "en"), "персональный жанр не должен блокироваться глобально")

	// SetAdmin живо обновляет кэш.
	require.NoError(t, r.SetAdmin(ctx, settings.ContentConfig{HiddenLanguages: []string{"uk"}}))
	require.True(t, r.AdminHides(nil, "uk"))
	require.False(t, r.AdminHides([]string{"erotica"}, "en"), "после перезаписи старые скрытые сняты")
}

// TestSettings_AppearanceRoundTrip — дефолт (soft) на пустой БД, upsert,
// нормализация мусорных значений в soft.
func TestSettings_AppearanceRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startSettingsPG(t, ctx)
	store := settings.New(pool)

	var uid int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO users (email, display_name, password_hash, role)
		VALUES ('appearance@example.test', 'A', 'x', 'user') RETURNING id`).Scan(&uid))

	// Нет оверрайда → дефолт (soft).
	got, err := store.UserAppearance(ctx, uid)
	require.NoError(t, err)
	require.Equal(t, settings.DefaultAppearanceConfig(), got)
	require.Equal(t, "soft", got.GenreChipStyle)

	// Сохранили classic — читается обратно.
	require.NoError(t, store.SetUserAppearance(ctx, uid, settings.AppearanceConfig{GenreChipStyle: "classic"}))
	got, err = store.UserAppearance(ctx, uid)
	require.NoError(t, err)
	require.Equal(t, "classic", got.GenreChipStyle)

	// Мусорное значение нормализуется в soft.
	require.NoError(t, store.SetUserAppearance(ctx, uid, settings.AppearanceConfig{GenreChipStyle: "garbage"}))
	got, err = store.UserAppearance(ctx, uid)
	require.NoError(t, err)
	require.Equal(t, "soft", got.GenreChipStyle)
}

// TestSettings_EnrichmentGatesRoundTrip — дефолт (всё включено) на пустой БД,
// upsert, и живое обновление кэша через EnrichmentGateResolver.
func TestSettings_EnrichmentGatesRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startSettingsPG(t, ctx)
	store := settings.New(pool)

	// Нет оверрайда → дефолт (ничего не выключено).
	got, err := store.EnrichmentGates(ctx)
	require.NoError(t, err)
	require.Equal(t, settings.DefaultEnrichmentGates(), got)
	require.False(t, got.CoverDisabled)
	require.False(t, got.AuthorDisabled)

	// Сохранили — читается обратно (upsert).
	want := settings.EnrichmentGates{
		CoverDisabled: true, AnnotationDisabled: false,
		AuthorDisabled: true, AdaptationDisabled: true,
	}
	require.NoError(t, store.SetEnrichmentGates(ctx, want))
	got, err = store.EnrichmentGates(ctx)
	require.NoError(t, err)
	require.Equal(t, want, got)

	// Resolver: Load читает из БД, SetGates живо обновляет кэш.
	r := settings.NewEnrichmentGateResolver(store)
	require.NoError(t, r.Load(ctx))
	require.Equal(t, want, r.Gates())

	require.NoError(t, r.SetGates(ctx, settings.EnrichmentGates{AnnotationDisabled: true}))
	require.True(t, r.Gates().AnnotationDisabled)
	require.False(t, r.Gates().CoverDisabled, "после перезаписи старые флаги сняты")
	// И персистентно.
	got, err = store.EnrichmentGates(ctx)
	require.NoError(t, err)
	require.True(t, got.AnnotationDisabled)
	require.False(t, got.CoverDisabled)
}

// TestEnrichmentGateResolver_NilSafe — Gates() на nil-резолвере возвращает
// дефолт (ничего не выключено), без паники. Так триггеры-хелперы зовут его
// как метод указателя при отсутствии deps (unit-тесты без БД).
func TestEnrichmentGateResolver_NilSafe(t *testing.T) {
	var r *settings.EnrichmentGateResolver
	require.Equal(t, settings.DefaultEnrichmentGates(), r.Gates())
	require.False(t, r.Gates().CoverDisabled)
}

func startSettingsPG(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	pgC, err := postgres.Run(ctx,
		"postgres:17-alpine",
		postgres.WithDatabase("skriptes_test"),
		postgres.WithUsername("skriptes"),
		postgres.WithPassword("skriptes"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pgC.Terminate(context.Background()) })
	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, db.Migrate(dsn))
	pool, err := db.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

// TestCoverConfig_EffectiveLimits — чистый юнит (без docker): прогрев ON
// → лимит кэша 0 (full-store, без эвикции); OFF → заданный бюджет.
func TestCoverConfig_EffectiveLimits(t *testing.T) {
	off := settings.CoverConfig{CacheMaxMB: 8192, CacheMinFreeMB: 1024, Prewarm: false}
	require.Equal(t, int64(8192)<<20, off.EffectiveCacheMaxBytes(), "без прогрева — заданный бюджет")
	require.Equal(t, int64(1024)<<20, off.MinFreeBytes())

	on := settings.CoverConfig{CacheMaxMB: 8192, CacheMinFreeMB: 1024, Prewarm: true}
	require.Equal(t, int64(0), on.EffectiveCacheMaxBytes(), "с прогревом — без лимита (full-store)")
	require.Equal(t, int64(1024)<<20, on.MinFreeBytes())
}
