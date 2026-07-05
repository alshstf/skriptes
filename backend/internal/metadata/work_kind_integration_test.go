package metadata

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// kindOf — текущий (kind, kind_source) работы; NULL → "".
func kindOf(t *testing.T, ctx context.Context, pool *pgxpool.Pool, workID int64) (kind, source string) {
	t.Helper()
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COALESCE(kind,''), COALESCE(kind_source,'') FROM works WHERE id=$1`, workID).Scan(&kind, &source))
	return kind, source
}

// putInSeries — привязать издание к серии (создав её при необходимости).
func putInSeries(t *testing.T, ctx context.Context, pool *pgxpool.Pool, bookID int64, seriesTitle string) {
	t.Helper()
	var sid int64
	err := pool.QueryRow(ctx, `SELECT id FROM series WHERE normalized_title=$1 AND author_id IS NULL`,
		seriesTitle).Scan(&sid)
	if err != nil {
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO series (title, normalized_title) VALUES ($1,$2) RETURNING id`,
			seriesTitle, seriesTitle).Scan(&sid))
	}
	_, err = pool.Exec(ctx, `UPDATE books SET series_id=$2 WHERE id=$1`, bookID, sid)
	require.NoError(t, err)
}

// TestClassifyWorkKinds_Integration — эвристики типизации на кейсах, снятых с
// прод-данных (Асприн/Толкин/Шекли): title-паттерн, серия-паразит,
// многоавторность; обычные романы не задеваются; fantlab/override не перетираются.
func TestClassifyWorkKinds_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPGForPrewarm(t, ctx)
	collID, archID := seedTitleFixture(t, ctx, pool)
	asprin := seedGroupAuthor(t, ctx, pool, "Асприн", "асприн роберт")

	// 1. Обычный роман — НЕ должен пометиться.
	novel := seedGroupBook(t, ctx, pool, collID, archID, asprin, "N1",
		"Ещё один великолепный МИФ", "ещё один великолепный миф", "ru", "", "", "")

	// «Том N» без слов сборника — половина романа, НЕ сборник (анти-кейс «Тихий Дон. Том 1»).
	tome := seedGroupBook(t, ctx, pool, collID, archID, asprin, "N2",
		"Тихий Дон. Том 1", "тихий дон том 1", "ru", "", "", "")

	// 2. Title-паттерны.
	coll := seedGroupBook(t, ctx, pool, collID, archID, asprin, "C1",
		"Шуттовская рота (сборник)", "шуттовская рота сборник", "ru", "", "", "")
	omni := seedGroupBook(t, ctx, pool, collID, archID, asprin, "C2",
		"Избранные произведения. Том II", "избранные произведения том ii", "ru", "", "", "")
	anthTitle := seedGroupBook(t, ctx, pool, collID, archID, asprin, "C3",
		"Антология мировой фантастики", "антология мировой фантастики", "ru", "", "", "")

	// 3. Серия-паразит: обычное название, но серия «Асприн, Роберт. Сборники».
	serCol := seedGroupBook(t, ctx, pool, collID, archID, asprin, "S1",
		"Мир воров", "мир воров", "ru", "", "", "")
	putInSeries(t, ctx, pool, serCol, "асприн, роберт. сборники")
	// …и серия «Антология фантастики» → anthology.
	serAnth := seedGroupBook(t, ctx, pool, collID, archID, asprin, "S2",
		"Мастера фэнтези 2005", "мастера фэнтези 2005", "ru", "", "", "")
	putInSeries(t, ctx, pool, serAnth, "антология фантастики")
	// Анти-кейс: серия «…(сборник)» (ед.ч.) — librusec-разворот одного сборника
	// на отдельные РАССКАЗЫ; членов метить нельзя.
	story := seedGroupBook(t, ctx, pool, collID, archID, asprin, "S3",
		"Серебряное зеркало", "серебряное зеркало", "ru", "", "", "")
	putInSeries(t, ctx, pool, story, "тринадцать загадочных случаев (сборник)")

	// 4. Многоавторность: обычное название, 4 автора → anthology.
	multi := seedGroupBook(t, ctx, pool, collID, archID, asprin, "M1",
		"Психолавка", "психолавка", "ru", "", "", "")
	for i, nm := range []string{"желязны", "шекли", "гаррисон"} {
		aid := seedGroupAuthor(t, ctx, pool, nm, nm)
		_, err := pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,$3)`,
			multi, aid, i+1)
		require.NoError(t, err)
	}

	// 5. Метки fantlab/override эвристика НЕ перетирает.
	flBook := seedGroupBook(t, ctx, pool, collID, archID, asprin, "F1",
		"Личный сборник рассказов", "личный сборник рассказов", "ru", "", "", "")
	_, err := pool.Exec(ctx, `UPDATE works SET kind=NULL, kind_source='fantlab' WHERE id=$1`,
		workIDOf(t, ctx, pool, flBook))
	require.NoError(t, err)

	n, err := ClassifyWorkKinds(ctx, pool)
	require.NoError(t, err)
	require.Positive(t, n)

	k, src := kindOf(t, ctx, pool, workIDOf(t, ctx, pool, novel))
	require.Empty(t, k, "обычный роман не помечается")
	require.Empty(t, src)

	k, _ = kindOf(t, ctx, pool, workIDOf(t, ctx, pool, tome))
	require.Empty(t, k, "«Том N» без слов сборника — не сборник")

	k, src = kindOf(t, ctx, pool, workIDOf(t, ctx, pool, coll))
	require.Equal(t, "collection", k)
	require.Equal(t, "heuristic", src)

	k, _ = kindOf(t, ctx, pool, workIDOf(t, ctx, pool, omni))
	require.Equal(t, "omnibus", k, "«Избранные произведения» → omnibus")

	k, _ = kindOf(t, ctx, pool, workIDOf(t, ctx, pool, anthTitle))
	require.Equal(t, "anthology", k, "«Антология…» в названии → anthology")

	k, _ = kindOf(t, ctx, pool, workIDOf(t, ctx, pool, serCol))
	require.Equal(t, "omnibus", k, "серия «…Сборники» метит работу")

	k, _ = kindOf(t, ctx, pool, workIDOf(t, ctx, pool, serAnth))
	require.Equal(t, "anthology", k, "серия «Антология…» → anthology")

	k, _ = kindOf(t, ctx, pool, workIDOf(t, ctx, pool, story))
	require.Empty(t, k, "член серии-разворота «…(сборник)» (ед.ч.) — рассказ, не метится")

	k, _ = kindOf(t, ctx, pool, workIDOf(t, ctx, pool, multi))
	require.Equal(t, "anthology", k, "≥4 авторов → anthology")

	k, src = kindOf(t, ctx, pool, workIDOf(t, ctx, pool, flBook))
	require.Empty(t, k, "метку fantlab эвристика не перетирает")
	require.Equal(t, "fantlab", src)

	// Идемпотентность: повторный прогон не падает и не меняет классификацию.
	_, err = ClassifyWorkKinds(ctx, pool)
	require.NoError(t, err)
	k, _ = kindOf(t, ctx, pool, workIDOf(t, ctx, pool, coll))
	require.Equal(t, "collection", k)
}
