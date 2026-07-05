package importer

import "testing"

// Ключ гейта обязан следовать за версией схемы workDoc: бамп
// WorksIndexSchemaVersion = новый ключ app_settings = форс полного
// ResyncWorksIndex на ближайшем старте (см. main.go::runOnceWorksIndexSync).
// Меняешь схему — инкрементируй константу и обнови ожидание здесь.
func TestWorksIndexSyncedFlagKey(t *testing.T) {
	// v7 — orig_lang (эффективный язык оригинала = src_lang ?? lang; фасет фильтра).
	if got, want := WorksIndexSyncedFlagKey(), "works_index_synced_v7"; got != want {
		t.Fatalf("WorksIndexSyncedFlagKey() = %q, want %q", got, want)
	}
}
