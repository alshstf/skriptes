package metadata

import (
	"archive/zip"
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// writeFB2Zip кладёт zip с одним fb2 (имя внутри — "book.fb2") в dir/zipName.
func writeFB2Zip(t *testing.T, dir, zipName string, fb2 []byte) {
	t.Helper()
	f, err := os.Create(filepath.Join(dir, zipName))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	zw := zip.NewWriter(f)
	w, err := zw.Create("book.fb2")
	require.NoError(t, err)
	_, err = w.Write(fb2)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
}

func fb2WithCover() []byte {
	enc := base64.StdEncoding.EncodeToString([]byte("JPEG-COVER-BYTES"))
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<FictionBook xmlns:l="http://www.w3.org/1999/xlink">
  <description><title-info>
    <coverpage><image l:href="#cover.jpg"/></coverpage>
  </title-info></description>
  <body><p>text</p></body>
  <binary id="cover.jpg" content-type="image/jpeg">` + enc + `</binary>
</FictionBook>`)
}

func fb2NoCover() []byte {
	return []byte(`<?xml version="1.0" encoding="UTF-8"?>
<FictionBook><description><title-info></title-info></description>
<body><p>no cover here</p></body></FictionBook>`)
}

// TestServeCoverByID_OnDemand — on-demand извлечение обложки из fb2 по id:
// первый вызов извлекает и кэширует (cover_path проставляется), второй —
// отдаёт из кэша. Книга без обложки в fb2 → not found.
func TestServeCoverByID_OnDemand(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPGForPrewarm(t, ctx)

	booksRoot := t.TempDir()
	writeFB2Zip(t, booksRoot, "withcover.zip", fb2WithCover())
	writeFB2Zip(t, booksRoot, "nocover.zip", fb2NoCover())

	var collID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	insBook := func(archive string) int64 {
		var aID, bID int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO archives (collection_id, filename) VALUES ($1,$2) RETURNING id`, collID, archive).Scan(&aID))
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title)
			 VALUES ($1,$2,$3,'book','fb2','T','t') RETURNING id`, collID, aID, archive).Scan(&bID))
		return bID
	}
	withCoverID := insBook("withcover.zip")
	noCoverID := insBook("nocover.zip")

	fb2p := NewFb2Provider()
	enricher, err := New(pool, t.TempDir(),
		[]CoverProvider{fb2p}, []AnnotationProvider{fb2p}, nil, nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)

	// Книга с обложкой: первый вызов извлекает on-demand.
	path, ok := enricher.ServeCoverByID(ctx, withCoverID, booksRoot)
	require.True(t, ok, "обложка должна извлечься из fb2")
	require.FileExists(t, path)

	// cover_path проставлен в БД.
	var cp *string
	require.NoError(t, pool.QueryRow(ctx, `SELECT cover_path FROM books WHERE id=$1`, withCoverID).Scan(&cp))
	require.NotNil(t, cp)
	require.NotEmpty(t, *cp)

	// Второй вызов — из кэша, тот же путь.
	path2, ok2 := enricher.ServeCoverByID(ctx, withCoverID, booksRoot)
	require.True(t, ok2)
	require.Equal(t, path, path2)

	// Книга без обложки в fb2 → not found.
	_, ok3 := enricher.ServeCoverByID(ctx, noCoverID, booksRoot)
	require.False(t, ok3, "у книги без coverpage обложки нет")
}
