package metadata_test

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/history"
	"github.com/skriptes/skriptes/backend/internal/metadata"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

const dublinersFB2 = `<?xml version="1.0" encoding="utf-8"?>
<FictionBook xmlns="http://www.gribuser.ru/xml/fictionbook/2.0">
<description><title-info><book-title>Дублинцы</book-title></title-info></description>
<body>
  <section><title><p>Сёстры</p></title><p>…</p></section>
  <section><title><p>Аравия</p></title><p>…</p></section>
  <section><title><p>Мёртвые</p></title><p>…</p></section>
</body>
</FictionBook>`

// TestContentsScanner — состав сборника из оглавления fb2 (#388): разбор и связка,
// «Состав» и «Входит в сборники» на карточке (со скрытием), «прочитано в
// сборнике», повторная связка после появления новой работы.
func TestContentsScanner(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)

	root := t.TempDir()
	f, err := os.Create(filepath.Join(root, "c.zip"))
	require.NoError(t, err)
	zw := zip.NewWriter(f)
	wr, err := zw.Create("1.fb2")
	require.NoError(t, err)
	_, err = io.WriteString(wr, dublinersFB2)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	require.NoError(t, f.Close())

	var collID, archID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('c','c.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'c.zip') RETURNING id`, collID).Scan(&archID))
	author := func(last, first string) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `INSERT INTO authors (last_name, first_name, normalized_name)
			VALUES ($1,$2,lower($1||' '||$2)) RETURNING id`, last, first).Scan(&id))
		return id
	}
	joyce, other := author("Джойс", "Джеймс"), author("Другой", "Автор")
	n := 0
	work := func(title, kind, lang string, aid int64) (int64, int64) {
		n++
		fileName := fmt.Sprintf("f%d", n)
		if kind != "" {
			fileName = "1" // оглавление сборника — 1.fb2 в c.zip
		}
		var wid, bid int64
		require.NoError(t, pool.QueryRow(ctx, `INSERT INTO works (title, normalized_title, primary_author_id, kind)
			VALUES ($1, lower($1), $2, NULLIF($3,'')) RETURNING id`, title, aid, kind).Scan(&wid))
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id, lang)
			VALUES ($1,$2,$3,$4,'fb2',$5,lower($5),$6,$7) RETURNING id`,
			collID, archID, fmt.Sprintf("l%d", n), fileName, title, wid, lang).Scan(&bid))
		_, err := pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,0)`, bid, aid)
		require.NoError(t, err)
		return wid, bid
	}
	comp, compBook := work("Дублинцы", "collection", "ru", joyce)
	sisters, _ := work("Сёстры", "", "en", joyce)
	dead, _ := work("Мертвые", "", "ru", joyce)
	_, _ = work("Аравия", "", "ru", other) // чужой автор — не связываем

	sc := metadata.NewContentsScanner(pool, root, slog.New(slog.NewTextHandler(io.Discard, nil)))
	scanned, linked, err := sc.ScanPending(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, scanned)
	require.Equal(t, 2, linked)

	bs := books.New(pool, nil, nil)
	contents, err := bs.WorkContents(ctx, comp, nil, nil)
	require.NoError(t, err)
	require.Len(t, contents, 3)
	require.Equal(t, "Сёстры", contents[0].Title)
	require.NotNil(t, contents[0].WorkID)
	require.Equal(t, sisters, *contents[0].WorkID)
	require.Nil(t, contents[1].WorkID, "«Аравия» другого автора — без ссылки")
	require.NotNil(t, contents[2].WorkID)
	require.Equal(t, dead, *contents[2].WorkID, "Мёртвые = Мертвые (ё/е)")

	hidden, err := bs.WorkContents(ctx, comp, nil, []string{"en"})
	require.NoError(t, err)
	require.Nil(t, hidden[0].WorkID, "работа на скрытом языке — строка без ссылки")
	require.Equal(t, "Сёстры", hidden[0].Title)

	comps, err := bs.WorkCompilations(ctx, dead, nil, nil)
	require.NoError(t, err)
	require.Len(t, comps, 1)
	require.Equal(t, comp, comps[0].WorkID)
	require.Equal(t, "collection", comps[0].Kind)
	require.Equal(t, "Джойс Джеймс", comps[0].Author)
	none, err := bs.WorkCompilations(ctx, dead, nil, []string{"ru"})
	require.NoError(t, err)
	require.Empty(t, none, "сборник на скрытом языке не показываем")

	// Прочитан сборник — у рассказа «прочитано в сборнике».
	hs := history.New(pool)
	var userID int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (email, display_name, password_hash, role)
		VALUES ('c@e.com','C','x','user') RETURNING id`).Scan(&userID))
	rc, err := hs.ReadInCompilation(ctx, userID, dead)
	require.NoError(t, err)
	require.Nil(t, rc)
	_, err = pool.Exec(ctx, `INSERT INTO reads (user_id, book_id, completed_at) VALUES ($1,$2,now())`, userID, compBook)
	require.NoError(t, err)
	rc, err = hs.ReadInCompilation(ctx, userID, dead)
	require.NoError(t, err)
	require.NotNil(t, rc)
	require.Equal(t, comp, rc.WorkID)
	require.Equal(t, "Дублинцы", rc.Title)

	// Новая работа Джойса «Аравия» — повторная связка после импорта находит её.
	arabyJoyce, _ := work("Аравия", "", "ru", joyce)
	relinked, err := sc.RelinkContents(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, relinked)
	contents, err = bs.WorkContents(ctx, comp, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, contents[1].WorkID)
	require.Equal(t, arabyJoyce, *contents[1].WorkID)

	// Разобранный сборник второй раз не читается.
	scanned, _, err = sc.ScanPending(ctx)
	require.NoError(t, err)
	require.Zero(t, scanned)

	// Сборник перестал быть сборником — состава и «входит в сборники» нет.
	_, err = pool.Exec(ctx, `UPDATE works SET kind = NULL WHERE id = $1`, comp)
	require.NoError(t, err)
	contents, err = bs.WorkContents(ctx, comp, nil, nil)
	require.NoError(t, err)
	require.Empty(t, contents)
	comps, err = bs.WorkCompilations(ctx, dead, nil, nil)
	require.NoError(t, err)
	require.Empty(t, comps)
}
