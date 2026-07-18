package metadata

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// TestClassifyServiceAuthors_Integration — консервативные паттерны на кейсах,
// снятых с прод-аудита («Коллектив авторов» 805, «Народные сказки» 741,
// «Газета Завтра» 737, «неизвестный автор» 504); люди-однофамильцы не
// задеваются; ручные метки (manual) эвристика не перетирает.
func TestClassifyServiceAuthors_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPGForPrewarm(t, ctx)

	mk := func(last, first, norm string) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO authors (last_name, first_name, normalized_name) VALUES ($1,$2,$3) RETURNING id`,
			last, first, norm).Scan(&id))
		return id
	}
	svcOf := func(id int64) (bool, string) {
		var s bool
		var src *string
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT is_service, COALESCE(is_service_source,'') FROM authors WHERE id=$1`, id).Scan(&s, &src))
		return s, *src
	}

	// Позитивные (прод-кейсы аудита).
	collective := mk("Коллектив авторов", "", "коллектив авторов")
	tales := mk("Народные сказки", "", "народные сказки")
	gazeta := mk("Газета Завтра", "", "газета завтра")
	unknown := mk("неизвестный", "Автор", "неизвестный автор")
	zhurnal := mk("Журнал «Если»", "", "журнал «если»")

	// Негативные: люди, похожие на паттерны только частично.
	gardner := mk("Гарднер", "Лиза", "гарднер лиза")
	gazetov := mk("Газета", "", "газета") // одиночное слово без продолжения — НЕ метим

	// Manual-защита: имя МАТЧИТСЯ паттерном («газета …»), но админ явно снял
	// метку — эвристика решение не перетирает.
	manualOff := mk("Газета Правда", "", "газета правда")
	_, err := pool.Exec(ctx,
		`UPDATE authors SET is_service=false, is_service_source='manual' WHERE id=$1`, manualOff)
	require.NoError(t, err)

	n, err := ClassifyServiceAuthors(ctx, pool)
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, int64(5))

	for _, id := range []int64{collective, tales, gazeta, unknown, zhurnal} {
		s, src := svcOf(id)
		require.True(t, s, "агрегат-псевдоавтор должен быть помечен (id=%d)", id)
		require.Equal(t, "heuristic", src)
	}
	for _, id := range []int64{gardner, gazetov} {
		s, _ := svcOf(id)
		require.False(t, s, "человек/одиночное слово не метится (id=%d)", id)
	}
	s, src := svcOf(manualOff)
	require.False(t, s, "manual-снятие эвристика не перетирает")
	require.Equal(t, "manual", src)

	// Идемпотентность: повторный прогон не падает и ничего не меняет.
	_, err = ClassifyServiceAuthors(ctx, pool)
	require.NoError(t, err)
	s, _ = svcOf(collective)
	require.True(t, s)
}

// Конкурентный прогон (runOnce старта + after-import) не ловит deadlock —
// advisory-lock сериализует (зеркало TestClassifyWorkKinds_ConcurrentNoDeadlock).
func TestClassifyServiceAuthors_ConcurrentNoDeadlock(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPGForPrewarm(t, ctx)
	seedServiceAuthors(t, ctx, pool, 30)

	const workers = 4
	errs := make(chan error, workers)
	start := make(chan struct{})
	for w := 0; w < workers; w++ {
		go func() {
			<-start
			_, err := ClassifyServiceAuthors(ctx, pool)
			errs <- err
		}()
	}
	close(start)
	for w := 0; w < workers; w++ {
		require.NoError(t, <-errs)
	}
}

func seedServiceAuthors(t *testing.T, ctx context.Context, pool *pgxpool.Pool, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		suffix := string(rune('a' + i%26))
		norm := "журнал тест " + suffix + string(rune('0'+i/26))
		_, err := pool.Exec(ctx,
			`INSERT INTO authors (last_name, normalized_name) VALUES ($1,$2)`, norm, norm)
		require.NoError(t, err)
	}
}
