package metadata

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

type mapBioProvider struct {
	bio map[string]string
	err map[string]error
}

func (m *mapBioProvider) Name() string { return "wikipedia" }
func (m *mapBioProvider) FetchAuthorBio(ctx context.Context, q AuthorQuery) (string, error) {
	if err := m.err[q.LastName]; err != nil {
		return "", err
	}
	if b, ok := m.bio[q.LastName]; ok {
		traceStep(ctx, TraceStep{Source: "wikipedia", Lang: "ru", Stage: "accept", Outcome: TracePass, Value: q.LastName})
		return b, nil
	}
	traceStep(ctx, TraceStep{Source: "wikipedia", Lang: "ru", Stage: "name_gate", Outcome: TraceReject, Value: "Однофамилец"})
	return "", ErrNotFound
}

type mapPhotoProvider struct{ img map[string]string }

func (m *mapPhotoProvider) Name() string { return "wikipedia" }
func (m *mapPhotoProvider) FetchAuthorPhoto(_ context.Context, q AuthorQuery) (*CoverImage, error) {
	if s, ok := m.img[q.LastName]; ok {
		return &CoverImage{Reader: io.NopCloser(strings.NewReader(s)), Mime: "image/jpeg"}, nil
	}
	return nil, ErrNotFound
}

// TestAuthorRechecker — #280: подтверждённое остаётся, чужое заменяется или
// очищается (с журналом), при сбое источника автор не трогается и повторяется.
func TestAuthorRechecker(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	bios := &mapBioProvider{
		bio: map[string]string{"Кепт": "Своя биография", "Замен": "Правильная биография"},
		err: map[string]error{"Сбой": ErrUpstream},
	}
	photos := &mapPhotoProvider{img: map[string]string{"Замен": "new-photo-bytes"}}
	enricher, err := New(pool, t.TempDir(), nil, nil, []AuthorPhotoProvider{photos}, []AuthorBioProvider{bios}, nil, quiet)
	require.NoError(t, err)

	mk := func(last, bio, photo string, renown int) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO authors (last_name, first_name, normalized_name, bio, photo_path, metadata_fetched_at, renown)
			VALUES ($1, 'Имя', lower($1) || ' имя', NULLIF($2, ''), NULLIF($3, ''), now() - interval '90 days', $4) RETURNING id`,
			last, bio, photo, renown).Scan(&id))
		return id
	}
	kept := mk("Кепт", "Своя биография", "", 10)
	replaced := mk("Замен", "Чужая биография", "old.jpg", 5)
	cleared := mk("Очист", "Биография однофамильца", "c.jpg", 3)
	flaky := mk("Сбой", "Какая-то биография", "", 1)
	empty := mk("Пусто", "", "", 100)

	read := func(id int64) (string, string) {
		var bio, photo string
		require.NoError(t, pool.QueryRow(ctx, `SELECT COALESCE(bio,''), COALESCE(photo_path,'') FROM authors WHERE id = $1`, id).Scan(&bio, &photo))
		return bio, photo
	}
	// since — по часам базы: metadata_fetched_at ставит её now(), и при расхождении
	// часов процесса и контейнера в миллисекунды только что проверенный автор
	// выглядел бы непроверенным (флак второго прохода).
	var since time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT now()`).Scan(&since))
	r := NewAuthorRechecker(pool, enricher, 0, quiet)
	st, err := r.Pass(ctx, since)
	require.NoError(t, err)
	require.Equal(t, 3, st.Checked)
	require.Equal(t, 1, st.Deferred)
	require.Equal(t, 1, st.BioKept)
	require.Equal(t, 1, st.BioNew)
	require.Equal(t, 1, st.BioClear)

	bio, _ := read(kept)
	require.Equal(t, "Своя биография", bio)
	bio, photo := read(replaced)
	require.Equal(t, "Правильная биография", bio)
	require.NotEqual(t, "old.jpg", photo)
	require.NotEmpty(t, photo)
	bio, photo = read(cleared)
	require.Empty(t, bio)
	require.Empty(t, photo)
	bio, _ = read(flaky)
	require.Equal(t, "Какая-то биография", bio, "сбой источника — не трогаем")
	bio, _ = read(empty)
	require.Empty(t, bio)

	var journal int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM author_meta_recheck`).Scan(&journal))
	require.Equal(t, 4, journal, "замена био, замена фото, очистка био, очистка фото")
	var oldBio string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT old_value FROM author_meta_recheck WHERE author_id = $1 AND field = 'bio'`, cleared).Scan(&oldBio))
	require.Equal(t, "Биография однофамильца", oldBio, "журнал хранит прежнее для отката")
	reason := func(id int64, field string) string {
		var r *string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT reason FROM author_meta_recheck WHERE author_id = $1 AND field = $2`, id, field).Scan(&r))
		if r == nil {
			return ""
		}
		return *r
	}
	require.Equal(t, "wikipedia/ru: reject name_gate «Однофамилец»", reason(cleared, "bio"), "журнал хранит причину решения")
	require.Equal(t, "wikipedia/ru: pass accept «Замен»", reason(replaced, "bio"))
	require.Empty(t, reason(cleared, "photo"), "провайдер без трассы — причины нет (NULL)")

	// Источник ожил — следующий проход добирает отложенного и только его.
	delete(bios.err, "Сбой")
	st, err = r.Pass(ctx, since)
	require.NoError(t, err)
	require.Equal(t, 1, st.Checked)
	require.Zero(t, st.Deferred)
	bio, _ = read(flaky)
	require.Empty(t, bio, "гейты не нашли — очищено")

	st, err = r.Pass(ctx, since)
	require.NoError(t, err)
	require.Zero(t, st.Checked, "всё перепроверено")
}

// v3 (#280): все авторы с книгами, в том числе без био и фото; без книг и
// служебные — нет.
func TestAuthorRechecker_AllAuthors(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	bios := &mapBioProvider{bio: map[string]string{"Безбио": "Найденная биография", "Безкниг": "x", "Служебный": "y"}}
	enricher, err := New(pool, t.TempDir(), nil, nil, nil, []AuthorBioProvider{bios}, nil, quiet)
	require.NoError(t, err)

	var coll, arch int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&coll))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, coll).Scan(&arch))
	mk := func(last string, service, withBook bool) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO authors (last_name, first_name, normalized_name, is_service, metadata_fetched_at)
			VALUES ($1, 'Имя', lower($1) || ' имя', $2, now() - interval '90 days') RETURNING id`, last, service).Scan(&id))
		if withBook {
			var b int64
			require.NoError(t, pool.QueryRow(ctx, `
				INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title)
				VALUES ($1, $2, $3, $3, 'fb2', 'Книга', 'книга') RETURNING id`, coll, arch, last).Scan(&b))
			_, err := pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id) VALUES ($1, $2)`, b, id)
			require.NoError(t, err)
		}
		return id
	}
	plain := mk("Безбио", false, true)
	noBooks := mk("Безкниг", false, false)
	service := mk("Служебный", true, true)

	var since time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT now()`).Scan(&since))
	st, err := NewAuthorRechecker(pool, enricher, 0, quiet).WithAllAuthors().WithWorkers(3).Pass(ctx, since)
	require.NoError(t, err)
	require.Equal(t, 1, st.Checked)
	require.Equal(t, 1, st.BioNew)

	bio := func(id int64) string {
		var b string
		require.NoError(t, pool.QueryRow(ctx, `SELECT COALESCE(bio,'') FROM authors WHERE id = $1`, id).Scan(&b))
		return b
	}
	require.Equal(t, "Найденная биография", bio(plain))
	require.Empty(t, bio(noBooks), "без книг — не автор каталога")
	require.Empty(t, bio(service), "служебные не перепроверяем")
	var action string
	require.NoError(t, pool.QueryRow(ctx, `SELECT action FROM author_meta_recheck WHERE author_id = $1`, plain).Scan(&action))
	require.Equal(t, "added", action)
}
