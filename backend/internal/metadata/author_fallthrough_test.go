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

// fixedBioProvider — источник с одним ответом на всех авторов.
type fixedBioProvider struct {
	name  string
	text  string
	err   error
	calls int
}

func (f *fixedBioProvider) Name() string { return f.name }
func (f *fixedBioProvider) FetchAuthorBio(context.Context, AuthorQuery) (string, error) {
	f.calls++
	return f.text, f.err
}

type fixedPhotoProvider struct {
	name  string
	img   string
	err   error
	calls int
}

func (f *fixedPhotoProvider) Name() string { return f.name }
func (f *fixedPhotoProvider) FetchAuthorPhoto(context.Context, AuthorQuery) (*CoverImage, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	if f.img == "" {
		return nil, ErrNotFound
	}
	return &CoverImage{Reader: io.NopCloser(strings.NewReader(f.img)), Mime: "image/jpeg"}, nil
}

// Сбой Википедии (429) не отдаёт решение следующему источнику: перепроверка
// иначе меняла верную ru-био на англоязычную из OpenLibrary (#347).
func TestFetchAuthor_TransientStopsChain(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	q := AuthorQuery{LastName: "Гончаров", FirstName: "Иван"}

	wiki := &fixedBioProvider{name: "wikipedia", err: ErrUpstream}
	ol := &fixedBioProvider{name: "openlibrary", text: "Russian novelist"}
	wikiPhoto := &fixedPhotoProvider{name: "wikipedia", err: ErrUpstream}
	olPhoto := &fixedPhotoProvider{name: "openlibrary", img: "ol-photo"}
	e, err := New(nil, t.TempDir(), nil, nil,
		[]AuthorPhotoProvider{wikiPhoto, olPhoto}, []AuthorBioProvider{wiki, ol}, nil, quiet)
	require.NoError(t, err)

	bio, transient := e.fetchAuthorBio(context.Background(), q)
	require.True(t, transient, "сбой верхнего источника — решение откладывается")
	require.Empty(t, bio)
	require.Zero(t, ol.calls, "нижний источник при сбое верхнего не спрашиваем")

	photo, transient := e.fetchAuthorPhoto(context.Background(), q)
	require.True(t, transient)
	require.Empty(t, photo)
	require.Zero(t, olPhoto.calls)

	// Верхний честно не нашёл — нижний отвечает как раньше.
	wiki.err, wikiPhoto.err = ErrNotFound, nil
	bio, transient = e.fetchAuthorBio(context.Background(), q)
	require.False(t, transient)
	require.Equal(t, "Russian novelist", bio)
	photo, transient = e.fetchAuthorPhoto(context.Background(), q)
	require.False(t, transient)
	require.NotEmpty(t, photo)
}

// Ленивый путь (карточка автора, фоновый воркер): то же правило, ничего не пишем.
func TestEnsureAuthor_TransientStopsChain(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))

	wiki := &fixedBioProvider{name: "wikipedia", err: ErrUpstream}
	ol := &fixedBioProvider{name: "openlibrary", text: "Russian novelist"}
	wikiPhoto := &fixedPhotoProvider{name: "wikipedia", err: ErrUpstream}
	olPhoto := &fixedPhotoProvider{name: "openlibrary", img: "ol-photo"}
	e, err := New(pool, t.TempDir(), nil, nil,
		[]AuthorPhotoProvider{wikiPhoto, olPhoto}, []AuthorBioProvider{wiki, ol}, nil, quiet)
	require.NoError(t, err)

	var id int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO authors (last_name, first_name, normalized_name) VALUES ('Гончаров', 'Иван', 'гончаров иван') RETURNING id`).Scan(&id))
	q := AuthorQuery{ID: id, LastName: "Гончаров", FirstName: "Иван", FullName: "Гончаров Иван"}

	require.True(t, e.EnsureAuthorBio(ctx, q))
	require.True(t, e.EnsureAuthorPhoto(ctx, q))
	require.Zero(t, ol.calls+olPhoto.calls, "нижний источник при сбое верхнего не спрашиваем")
	var bio, photo *string
	var fetched *time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT bio, photo_path, metadata_fetched_at FROM authors WHERE id = $1`, id).
		Scan(&bio, &photo, &fetched))
	require.Nil(t, bio)
	require.Nil(t, photo)
	require.Nil(t, fetched, "попытка не помечена — повторится")
}
