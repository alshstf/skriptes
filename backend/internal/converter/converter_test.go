package converter_test

import (
	"testing"

	"github.com/skriptes/skriptes/backend/internal/converter"
	"github.com/stretchr/testify/require"
)

func TestParseFormat_Valid(t *testing.T) {
	for _, in := range []string{"epub3", "EPUB3", "fb2", "kepub", "azw8", "kfx", "epub2"} {
		f, err := converter.ParseFormat(in)
		require.NoError(t, err, "in=%s", in)
		require.NotEmpty(t, f)
	}
}

func TestParseFormat_DefaultsToEpub3(t *testing.T) {
	f, err := converter.ParseFormat("")
	require.NoError(t, err)
	require.Equal(t, converter.FormatEpub3, f)
}

func TestParseFormat_Unknown(t *testing.T) {
	_, err := converter.ParseFormat("mobi")
	require.ErrorIs(t, err, converter.ErrUnknownFormat)
	_, err = converter.ParseFormat("../bin/sh")
	require.ErrorIs(t, err, converter.ErrUnknownFormat)
}

func TestAvailableFormats_StableOrder(t *testing.T) {
	got := converter.AvailableFormats()
	require.NotEmpty(t, got)
	require.Contains(t, got, converter.FormatFB2)
	require.Contains(t, got, converter.FormatEpub3)
	// FormatFB2 должен быть первым — это passthrough, всегда доступен и быстр.
	require.Equal(t, converter.FormatFB2, got[0])
}

func TestNew_RejectsEmptyPaths(t *testing.T) {
	_, err := converter.New("", "/tmp/cache", "fbc")
	require.Error(t, err)
	_, err = converter.New("/tmp/books", "", "fbc")
	require.Error(t, err)
}
