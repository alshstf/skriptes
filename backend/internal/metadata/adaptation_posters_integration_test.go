package metadata

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeAdaptationProvider — фикс-срез адаптаций для EnsureAdaptations.
type fakeAdaptationProvider struct{ items []Adaptation }

func (f *fakeAdaptationProvider) Name() string { return "fake" }
func (f *fakeAdaptationProvider) FetchAdaptations(_ context.Context, _ BookQuery) ([]Adaptation, error) {
	return f.items, nil
}

// Транзиентный провал скачивания постера НЕ должен ставить adaptations_fetched_at
// (иначе один сбой в момент единственного фетча терял постеры навсегда —
// single-shot: воркер берёт только NULL-маркер). Строки адаптаций при этом
// пишутся; следующий заход дозаливает постер через ON CONFLICT ... COALESCE.
// Плюс: ClearPosterCache сбрасывает маркер (кнопка = «очистить и перекачать»),
// RefillMissingPosters отправляет на перепроход только книги без постеров.
func TestEnsureAdaptations_PosterLifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPGForPrewarm(t, ctx)
	collID, archID := seedTitleFixture(t, ctx, pool)
	author := seedGroupAuthor(t, ctx, pool, "Остин", "остин джейн")
	bookID := seedGroupBook(t, ctx, pool, collID, archID, author, "A1", "Разум и чувства", "разум и чувства", "ru", "", "", "")

	// Постер-сервер: 500 пока fail=true, потом честный JPEG.
	var fail atomic.Bool
	fail.Store(true)
	posterSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write([]byte("jpeg-bytes"))
	}))
	defer posterSrv.Close()

	provider := &fakeAdaptationProvider{items: []Adaptation{{
		Provider:  "wikidata",
		ExtID:     "Q643263",
		Title:     "Разум и чувства",
		Kind:      "film",
		PosterURL: posterSrv.URL + "/p.jpg",
	}}}
	enricher, err := New(pool, filepath.Join(t.TempDir(), "covers"),
		nil, nil, nil, nil, []AdaptationProvider{provider},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)

	fetchedAt := func() *time.Time {
		var v *time.Time
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT adaptations_fetched_at FROM books WHERE id = $1`, bookID).Scan(&v))
		return v
	}
	posterPath := func() *string {
		var v *string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT poster_path FROM book_adaptations WHERE book_id = $1 AND ext_id = 'Q643263'`, bookID).Scan(&v))
		return v
	}

	// 1) Скачивание постера падает → строка записана, постера нет, маркер НЕ
	// стоит (книга остаётся pending — перепройдётся).
	enricher.EnsureAdaptations(ctx, BookQuery{ID: bookID})
	require.Nil(t, posterPath(), "постер не скачался")
	require.Nil(t, fetchedAt(), "маркер не ставится при провале всех скачиваний")

	// 2) Сервер ожил → повторный заход дозаливает постер в СУЩЕСТВУЮЩУЮ строку
	// (COALESCE) и ставит маркер.
	fail.Store(false)
	enricher.EnsureAdaptations(ctx, BookQuery{ID: bookID})
	require.NotNil(t, posterPath(), "постер дозалит при повторном заходе")
	require.NotNil(t, fetchedAt(), "успех → маркер стоит")

	// 3) «Очистить постеры» — файлы удалены, poster_path NULL и маркер сброшен
	// (иначе перекачать было бы некому: воркер берёт только NULL-маркер).
	removed, err := enricher.ClearPosterCache(ctx)
	require.NoError(t, err)
	require.Positive(t, removed)
	require.Nil(t, posterPath())
	require.Nil(t, fetchedAt(), "очистка = «очистить и перекачать»")

	// 4) RefillMissingPosters: трогает ТОЛЬКО книги с постер-дырами.
	// Восстановим состояние «всё скачано»…
	enricher.EnsureAdaptations(ctx, BookQuery{ID: bookID})
	require.NotNil(t, posterPath())
	require.NotNil(t, fetchedAt())
	n, err := enricher.RefillMissingPosters(ctx)
	require.NoError(t, err)
	require.Zero(t, n, "постеры на месте — перепроход не нужен")
	// …и пробьём дыру: постер одной адаптации потерян.
	_, err = pool.Exec(ctx, `UPDATE book_adaptations SET poster_path = NULL WHERE book_id = $1`, bookID)
	require.NoError(t, err)
	n, err = enricher.RefillMissingPosters(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n, "книга с постер-дырой отправлена на перепроход")
	require.Nil(t, fetchedAt())
}

// Авто-перепроверка постер-дыр (RecheckPosterHoles): у совсем нового фильма
// постера на TMDB в момент первого фетча ещё нет (честный ErrNotFound → маркер
// книги ставится), но он появляется позже — фаза воркера дозаливает его САМА
// по TTL poster_checked_at, без ручной кнопки. TMDB-id персистится из SPARQL
// (миграция 0037), перепроверка — чистый TMDB-вызов без SPARQL.
func TestRecheckPosterHoles_NewFilmPosterAppearsLater(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPGForPrewarm(t, ctx)
	collID, archID := seedTitleFixture(t, ctx, pool)
	author := seedGroupAuthor(t, ctx, pool, "Остин", "остин джейн")
	bookID := seedGroupBook(t, ctx, pool, collID, archID, author, "A2", "Чувство и чувствительность", "чувство и чувствительность", "ru", "", "", "")

	// Один сервер на два амплуа: TMDB API (/3/movie/…) и image-CDN (/t/p/…).
	var posterAvailable atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/3/movie/"):
			if posterAvailable.Load() {
				_, _ = w.Write([]byte(`{"poster_path":"/new-film.jpg"}`))
			} else {
				_, _ = w.Write([]byte(`{"poster_path":null}`))
			}
		case strings.HasPrefix(r.URL.Path, "/t/p/"):
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write([]byte("jpeg-bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	provider := &fakeAdaptationProvider{items: []Adaptation{{
		Provider:    "wikidata",
		ExtID:       "Q135436961",
		Title:       "Чувство и чувствительность",
		Kind:        "film",
		TMDBMovieID: "999001", // P4947 есть, а постера на TMDB пока нет
	}}}
	enricher, err := New(pool, filepath.Join(t.TempDir(), "covers"),
		nil, nil, nil, nil, []AdaptationProvider{provider},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	require.NoError(t, err)
	enricher.WithTMDBPosters(NewTMDBPosterProvider("k").WithBaseURLs(srv.URL, srv.URL))

	row := func() (poster *string, tmdbID *string, checkedAt *time.Time) {
		require.NoError(t, pool.QueryRow(ctx, `
			SELECT poster_path, tmdb_movie_id, poster_checked_at
			FROM book_adaptations WHERE book_id = $1`, bookID).Scan(&poster, &tmdbID, &checkedAt))
		return
	}

	// 1) Первый фетч: постера честно нет (не транзиент!) → строка с TMDB-id,
	// постер NULL, маркер книги СТОИТ (книга больше не pending), checked_at есть.
	enricher.EnsureAdaptations(ctx, BookQuery{ID: bookID})
	poster, tmdbID, checkedAt := row()
	require.Nil(t, poster)
	require.NotNil(t, tmdbID)
	require.Equal(t, "999001", *tmdbID, "TMDB-id персистится из SPARQL-ответа")
	require.NotNil(t, checkedAt)
	var marker *time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT adaptations_fetched_at FROM books WHERE id = $1`, bookID).Scan(&marker))
	require.NotNil(t, marker, "честное отсутствие постера — не транзиент, маркер ставится")

	// 2) TTL не истёк → дыра не трогается.
	checked, filled, err := enricher.RecheckPosterHoles(ctx, 10)
	require.NoError(t, err)
	require.Zero(t, checked, "свежепроверенная дыра ждёт TTL")

	// 3) TTL истёк, постера всё ещё нет → checked_at отодвинут, постера нет.
	_, err = pool.Exec(ctx,
		`UPDATE book_adaptations SET poster_checked_at = now() - interval '8 days' WHERE book_id = $1`, bookID)
	require.NoError(t, err)
	checked, filled, err = enricher.RecheckPosterHoles(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 1, checked)
	require.Zero(t, filled)
	poster, _, checkedAt = row()
	require.Nil(t, poster)
	require.WithinDuration(t, time.Now(), *checkedAt, time.Minute, "следующая проверка отодвинута на TTL")

	// 4) Постер появился на TMDB → следующая перепроверка дозаливает его.
	posterAvailable.Store(true)
	_, err = pool.Exec(ctx,
		`UPDATE book_adaptations SET poster_checked_at = now() - interval '8 days' WHERE book_id = $1`, bookID)
	require.NoError(t, err)
	checked, filled, err = enricher.RecheckPosterHoles(ctx, 10)
	require.NoError(t, err)
	require.Equal(t, 1, checked)
	require.Equal(t, 1, filled, "постер нового фильма дозалит автоматически")
	poster, _, _ = row()
	require.NotNil(t, poster)
}
