package settings

import (
	"encoding/json"
	"testing"
)

// Back-compat тумблера TMDB: конфиги, сохранённые ДО появления поля
// tmdb_posters, при чтении поверх дефолта остаются с TMDB=вкл (лоадер
// Store.BioAdaptation анмаршалит в DefaultBioAdaptationConfig()).
func TestBioAdaptationConfig_TMDBBackCompat(t *testing.T) {
	cfg := DefaultBioAdaptationConfig()
	if !cfg.TMDBPosters {
		t.Fatal("дефолт TMDBPosters должен быть true")
	}
	// Старый сохранённый JSON без поля — дефолт не перетирается.
	if err := json.Unmarshal([]byte(`{"bios":true,"adaptations":true,"bios_rpm":30,"adaptations_rpm":20}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if !cfg.TMDBPosters {
		t.Fatal("отсутствие поля в старом конфиге не должно выключать TMDB")
	}
	// Явное false — уважается.
	if err := json.Unmarshal([]byte(`{"tmdb_posters":false}`), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.TMDBPosters {
		t.Fatal("явное tmdb_posters=false должно выключать")
	}
}
