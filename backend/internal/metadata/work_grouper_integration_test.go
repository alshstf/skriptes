package metadata

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// seedGroupAuthor создаёт автора (нормализованное имя обязано быть уникальным).
func seedGroupAuthor(t *testing.T, ctx context.Context, pool *pgxpool.Pool, last, norm string) int64 {
	t.Helper()
	var id int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO authors (last_name, normalized_name) VALUES ($1,$2) RETURNING id`, last, norm).Scan(&id))
	return id
}

// seedGroupBook создаёт издание + свою singleton-работу + связь с автором,
// имитируя состояние после импорта (work_scanned_at NULL, edition_meta прошёл).
func seedGroupBook(t *testing.T, ctx context.Context, pool *pgxpool.Pool, collID, archID, authorID int64,
	lib, title, normTitle, lang, srcTitle, srcLang, srcAuthor string) int64 {
	t.Helper()
	var workID, bookID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO works (title, normalized_title, primary_author_id) VALUES ($1,$2,$3) RETURNING id`,
		title, normTitle, authorID).Scan(&workID))
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title,
		                   lang, src_title, src_lang, src_author_normalized, work_id, edition_meta_scanned_at)
		VALUES ($1,$2,$3,'f','fb2',$4,$5,$6, NULLIF($7,''), NULLIF($8,''), NULLIF($9,'')::citext, $10, now())
		RETURNING id`,
		collID, archID, lib, title, normTitle, lang, srcTitle, srcLang, srcAuthor, workID).Scan(&bookID))
	_, err := pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,0)`, bookID, authorID)
	require.NoError(t, err)
	return bookID
}

func workIDOf(t *testing.T, ctx context.Context, pool *pgxpool.Pool, bookID int64) int64 {
	t.Helper()
	var w int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT work_id FROM books WHERE id=$1`, bookID).Scan(&w))
	return w
}

func TestWorkGrouper_Tier1_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPGForPrewarm(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	var collID, archID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))

	tolkien := seedGroupAuthor(t, ctx, pool, "Толкин", "толкин")
	other := seedGroupAuthor(t, ctx, pool, "Другой", "другой автор")

	// Оригинал + два перевода (через src-title-info) — должны слиться в одну работу.
	orig := seedGroupBook(t, ctx, pool, collID, archID, tolkien, "L1", "The Hobbit", "the hobbit", "en", "", "", "")
	tr1 := seedGroupBook(t, ctx, pool, collID, archID, tolkien, "L2", "Хоббит", "хоббит", "ru", "The Hobbit", "en", "толкин")
	tr2 := seedGroupBook(t, ctx, pool, collID, archID, tolkien, "L3", "Хоббит, туда и обратно", "хоббит, туда и обратно", "ru", "The Hobbit", "en", "толкин")
	// Дубль одного языка (точная копия названия) — тоже в ту же работу.
	dupEn := seedGroupBook(t, ctx, pool, collID, archID, tolkien, "L4", "The Hobbit", "the hobbit", "en", "", "", "")
	// Другая книга того же автора — отдельная работа.
	lotr := seedGroupBook(t, ctx, pool, collID, archID, tolkien, "L5", "The Lord of the Rings", "the lord of the rings", "en", "", "", "")
	// Одноимённая книга ДРУГОГО автора — НЕ должна слиться с хоббитами.
	foreign := seedGroupBook(t, ctx, pool, collID, archID, other, "L6", "The Hobbit", "the hobbit", "en", "", "", "")

	g := NewWorkGrouper(pool, nil, nil, WorkGroupConfig{}, quiet) // Tier-1 only
	g.drain(ctx)

	// orig + tr1 + tr2 + dupEn → одна работа.
	wOrig := workIDOf(t, ctx, pool, orig)
	require.Equal(t, wOrig, workIDOf(t, ctx, pool, tr1), "перевод 1 слит с оригиналом")
	require.Equal(t, wOrig, workIDOf(t, ctx, pool, tr2), "перевод 2 слит с оригиналом")
	require.Equal(t, wOrig, workIDOf(t, ctx, pool, dupEn), "англ. дубль слит")
	require.NotEqual(t, wOrig, workIDOf(t, ctx, pool, lotr), "другая книга — отдельная работа")
	require.NotEqual(t, wOrig, workIDOf(t, ctx, pool, foreign), "другой автор НЕ слит")

	var editions int
	require.NoError(t, pool.QueryRow(ctx, `SELECT edition_count FROM works WHERE id=$1`, wOrig).Scan(&editions))
	require.Equal(t, 4, editions, "edition_count канонической работы = 4")

	// Работ у Толкина ровно 2 (Хоббит-кластер + Властелин колец).
	var tolkienWorks int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM works WHERE primary_author_id=$1`, tolkien).Scan(&tolkienWorks))
	require.Equal(t, 2, tolkienWorks, "опустевшие singleton-работы GC'нуты")

	// Все обработанные книги помечены work_scanned_at.
	var unscanned int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM books WHERE work_scanned_at IS NULL AND deleted=false`).Scan(&unscanned))
	require.Equal(t, 0, unscanned, "все кандидаты помечены scanned")

	// Идемпотентность: повторный проход кандидатов не находит, ничего не ломает.
	g.drain(ctx)
	require.Equal(t, wOrig, workIDOf(t, ctx, pool, tr1))
}

// fakeWorkResolver — внешний резолвер для Tier-2-теста.
type fakeWorkResolver struct {
	name string
	key  string
	err  error
}

func (f *fakeWorkResolver) Name() string { return f.name }
func (f *fakeWorkResolver) ResolveWorkKey(context.Context, WorkQuery) (string, error) {
	return f.key, f.err
}

func TestWorkGrouper_Tier2_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPGForPrewarm(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	var collID, archID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))

	author := seedGroupAuthor(t, ctx, pool, "Кинг", "кинг стивен")
	// Разные названия, без src → Tier-1 их НЕ свяжет. Внешний резолвер вернёт
	// одинаковый work_key → должны слиться через Tier-2.
	b1 := seedGroupBook(t, ctx, pool, collID, archID, author, "K1", "Оно", "оно", "ru", "", "", "")
	b2 := seedGroupBook(t, ctx, pool, collID, archID, author, "K2", "It", "it", "en", "", "", "")

	fake := &fakeWorkResolver{name: "openlibrary", key: "OL777W"}
	g := NewWorkGrouper(pool, fake, nil, WorkGroupConfig{
		OpenLibrary: true, OpenLibraryRPM: 0, NotFoundRetryDays: 90, ErrorRetryHours: 24,
	}, quiet)
	g.drain(ctx)

	require.Equal(t, workIDOf(t, ctx, pool, b1), workIDOf(t, ctx, pool, b2), "слиты по внешнему work_key")

	// book_work_lookups: found с work_key у обеих книг.
	var found int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM book_work_lookups WHERE source='openlibrary' AND outcome='found' AND work_key='OL777W'`).Scan(&found))
	require.Equal(t, 2, found)

	// ext_ids канонической работы содержит ol_work.
	w := workIDOf(t, ctx, pool, b1)
	var olWork *string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT ext_ids->>'ol_work' FROM works WHERE id=$1`, w).Scan(&olWork))
	require.NotNil(t, olWork)
	require.Equal(t, "OL777W", *olWork)
}
