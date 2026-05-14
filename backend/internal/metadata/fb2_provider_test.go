package metadata

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// makeFB2Archive создаёт мини-zip с одним fb2-файлом для теста.
// payload — содержимое fb2 (XML).
func makeFB2Archive(t *testing.T, fb2Name string, payload []byte) string {
	t.Helper()
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "test.zip")
	f, err := os.Create(zipPath)
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	zw := zip.NewWriter(f)
	w, err := zw.Create(fb2Name)
	require.NoError(t, err)
	_, err = w.Write(payload)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	return zipPath
}

func TestFb2Provider_ExtractsCoverpageBinary(t *testing.T) {
	// Минимальный фейковый JPEG: байты "JPEG-COVER".
	rawJPEG := []byte("JPEG-COVER")
	encoded := base64.StdEncoding.EncodeToString(rawJPEG)

	fb2 := []byte(`<?xml version="1.0"?>
<FictionBook xmlns:l="http://www.w3.org/1999/xlink">
  <description>
    <title-info>
      <coverpage>
        <image l:href="#cover.jpg"/>
      </coverpage>
    </title-info>
  </description>
  <body><p>text</p></body>
  <binary id="cover.jpg" content-type="image/jpeg">` + encoded + `</binary>
</FictionBook>`)

	zipPath := makeFB2Archive(t, "book.fb2", fb2)

	p := NewFb2Provider()
	img, err := p.FetchCover(context.Background(), BookQuery{
		ArchivePath: zipPath, FB2Name: "book.fb2",
	})
	require.NoError(t, err)
	require.NotNil(t, img)
	defer func() { _ = img.Reader.Close() }()

	got, err := io.ReadAll(img.Reader)
	require.NoError(t, err)
	require.True(t, bytes.Equal(rawJPEG, got), "decoded cover bytes mismatch")
	require.Equal(t, "image/jpeg", img.Mime)
	require.Equal(t, "fb2:cover.jpg", img.SourceID)
}

func TestFb2Provider_FallbackToFirstImageBinary(t *testing.T) {
	// Нет coverpage, но есть binary с image/* — должны взять его.
	raw := []byte("PNG-FALLBACK")
	encoded := base64.StdEncoding.EncodeToString(raw)
	fb2 := []byte(`<?xml version="1.0"?>
<FictionBook>
  <description><title-info></title-info></description>
  <body/>
  <binary id="img-1.png" content-type="image/png">` + encoded + `</binary>
</FictionBook>`)
	zipPath := makeFB2Archive(t, "book.fb2", fb2)

	p := NewFb2Provider()
	img, err := p.FetchCover(context.Background(), BookQuery{
		ArchivePath: zipPath, FB2Name: "book.fb2",
	})
	require.NoError(t, err)
	defer func() { _ = img.Reader.Close() }()
	got, err := io.ReadAll(img.Reader)
	require.NoError(t, err)
	require.True(t, bytes.Equal(raw, got))
	require.Equal(t, "image/png", img.Mime)
}

func TestFb2Provider_NoBinaries(t *testing.T) {
	fb2 := []byte(`<?xml version="1.0"?><FictionBook><body/></FictionBook>`)
	zipPath := makeFB2Archive(t, "book.fb2", fb2)
	p := NewFb2Provider()
	_, err := p.FetchCover(context.Background(), BookQuery{
		ArchivePath: zipPath, FB2Name: "book.fb2",
	})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestFb2Provider_MissingArchiveOrName(t *testing.T) {
	p := NewFb2Provider()
	_, err := p.FetchCover(context.Background(), BookQuery{})
	require.ErrorIs(t, err, ErrNotFound)
}
