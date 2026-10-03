package settings

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// Режим «только выбранные языки» (#310): видны только показываемые, остальные —
// и новые языки следующих INPX — скрыты.
func TestContentLanguageAllowlist(t *testing.T) {
	cfg := ContentConfig{LanguageMode: LanguageModeOnly, ShownLanguages: []string{"en", "ru", "ru", "ru-RU", ""}}
	cfg.normalize()
	require.Equal(t, []string{"en", "ru"}, cfg.ShownLanguages, "дубли и ненормализованные коды отброшены")
	require.False(t, cfg.Hides(nil, "ru"))
	require.True(t, cfg.Hides(nil, "de"), "не выбранный — скрыт")
	require.True(t, cfg.Hides(nil, "xx"), "новый язык следующего INPX — скрыт")
	require.False(t, cfg.Hides(nil, ""), "язык неизвестен — не скрываем")

	other := ContentConfig{LanguageMode: "weird", ShownLanguages: []string{"ru"}, HiddenLanguages: []string{"de"}}
	other.normalize()
	require.Empty(t, other.LanguageMode, "незнакомый режим — обычный чёрный список")
	require.Nil(t, other.ShownLanguages)
	require.True(t, other.Hides(nil, "de"))
	require.False(t, other.Hides(nil, "fr"))

	r := &ContentResolver{}
	r.admin.Store(&cfg)
	calls := 0
	r.SetLanguageUniverse(func(context.Context) ([]string, error) {
		calls++
		return []string{"ru", "en", "de", "fr"}, nil
	})
	require.Equal(t, []string{"de", "fr"}, r.AdminHiddenLanguages(context.Background()))
	require.Equal(t, []string{"de", "fr"}, r.AdminHiddenLanguages(context.Background()))
	require.Equal(t, 1, calls, "множество языков кэшируется")

	// Сбой источника — прежнее значение, а не пусто (иначе всё стало бы видно).
	r.uniFetched = r.uniFetched.Add(-2 * langUniverseTTL)
	r.SetLanguageUniverse(func(context.Context) ([]string, error) { return nil, errors.New("db down") })
	require.Equal(t, []string{"de", "fr"}, r.AdminHiddenLanguages(context.Background()))
}
