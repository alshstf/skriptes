package importer

import "testing"

// Ключ гейта обязан следовать за версией схемы workDoc: бамп
// WorksIndexSchemaVersion = новый ключ app_settings = форс полного
// ResyncWorksIndex на ближайшем старте (см. main.go::runOnceWorksIndexSync).
// Меняешь схему — инкрементируй константу и обнови ожидание здесь.
func TestWorksIndexSyncedFlagKey(t *testing.T) {
	// v8 — orig_lang стал work-level: union непустых src_lang изданий, фолбэк —
	// union языков изданий (перевод-сирота без src_lang — больше не «натив»).
	if got, want := WorksIndexSyncedFlagKey(), "works_index_synced_v8"; got != want {
		t.Fatalf("WorksIndexSyncedFlagKey() = %q, want %q", got, want)
	}
}
