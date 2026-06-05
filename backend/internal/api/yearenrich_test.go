package api

import (
	"testing"

	"github.com/skriptes/skriptes/backend/internal/settings"
)

// TestYearLazyWanted — гейты ленивого дозаполнения года: локальное требует
// мастера обработки + под-тумблера года, внешнее — opt-in воркера.
func TestYearLazyWanted(t *testing.T) {
	cases := []struct {
		name         string
		cover        settings.CoverConfig
		year         settings.YearEnrichmentConfig
		wantLocal    bool
		wantExternal bool
	}{
		{"всё выключено", settings.CoverConfig{}, settings.YearEnrichmentConfig{}, false, false},
		{"только локально", settings.CoverConfig{Prewarm: true, SyncYears: true}, settings.YearEnrichmentConfig{}, true, false},
		{"prewarm off → локально off", settings.CoverConfig{Prewarm: false, SyncYears: true}, settings.YearEnrichmentConfig{}, false, false},
		{"sync_years off → локально off", settings.CoverConfig{Prewarm: true, SyncYears: false}, settings.YearEnrichmentConfig{}, false, false},
		{"только внешне", settings.CoverConfig{}, settings.YearEnrichmentConfig{Enabled: true}, false, true},
		{"оба", settings.CoverConfig{Prewarm: true, SyncYears: true}, settings.YearEnrichmentConfig{Enabled: true}, true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			local, external := yearLazyWanted(c.cover, c.year)
			if local != c.wantLocal || external != c.wantExternal {
				t.Fatalf("yearLazyWanted = (%v,%v), want (%v,%v)", local, external, c.wantLocal, c.wantExternal)
			}
		})
	}
}
