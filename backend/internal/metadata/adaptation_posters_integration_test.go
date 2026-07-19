package metadata

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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
