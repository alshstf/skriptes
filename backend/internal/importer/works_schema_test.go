package importer

import "testing"

// Ключ гейта обязан следовать за версией схемы workDoc: бамп
// WorksIndexSchemaVersion = новый ключ app_settings = форс полного
// ResyncWorksIndex на ближайшем старте (см. main.go::runOnceWorksIndexSync).
// Меняешь схему — инкрементируй константу и обнови ожидание здесь.
func TestWorksIndexSyncedFlagKey(t *testing.T) {
	if got, want := WorksIndexSyncedFlagKey(), "works_index_synced_v3"; got != want {
		t.Fatalf("WorksIndexSyncedFlagKey() = %q, want %q", got, want)
	}
}
