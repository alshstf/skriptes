package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/settings"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// PUT настроек био/экранизаций ложится поверх сохранённого конфига: клиент,
// который не знает о tmdb_posters, не выключает TMDB.
func TestUpdateBioAdaptation_KeepsUnsentFields(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	store := settings.New(testpg.Pool(t, ctx))
	d := SettingsDeps{Store: store}

	put := func(body string) {
		t.Helper()
		rec := httptest.NewRecorder()
		handleUpdateBioAdaptation(d)(rec, httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body)))
		require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	}

	put(`{"bios":true,"adaptations":false,"bios_rpm":30,"adaptations_rpm":20,"tmdb_posters":false}`)
	cfg, err := store.BioAdaptation(ctx)
	require.NoError(t, err)
	require.False(t, cfg.TMDBPosters)

	// Старый клиент без поля — выключенный TMDB так и остаётся выключенным.
	put(`{"bios":false,"adaptations":true,"bios_rpm":10,"adaptations_rpm":5}`)
	cfg, err = store.BioAdaptation(ctx)
	require.NoError(t, err)
	require.False(t, cfg.TMDBPosters)
	require.True(t, cfg.Adaptations)
	require.Equal(t, 10, cfg.BiosRPM)

	put(`{"tmdb_posters":true}`)
	cfg, err = store.BioAdaptation(ctx)
	require.NoError(t, err)
	require.True(t, cfg.TMDBPosters)
	require.True(t, cfg.Adaptations, "неотправленные поля сохраняются")
}
