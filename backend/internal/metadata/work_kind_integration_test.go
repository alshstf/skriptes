package metadata

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/skriptes/skriptes/backend/internal/testpg"
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
	pool := testpg.Pool(t, ctx)
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
	// Два сигнала: название «сборник» (collection) и серия «…Сборники»
	// (omnibus) — побеждает серия; раньше тип переписывался на каждом прогоне.
	both := seedGroupBook(t, ctx, pool, collID, archID, asprin, "S4",
		"Сборник фантастики", "сборник фантастики", "ru", "", "", "")
	putInSeries(t, ctx, pool, both, "шекли, роберт. сборники")
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

	changed, err := ClassifyWorkKinds(ctx, pool)
	require.NoError(t, err)
	require.NotEmpty(t, changed)

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

	k, _ = kindOf(t, ctx, pool, workIDOf(t, ctx, pool, both))
	require.Equal(t, "omnibus", k, "серия сильнее названия")

	k, _ = kindOf(t, ctx, pool, workIDOf(t, ctx, pool, story))
	require.Empty(t, k, "член серии-разворота «…(сборник)» (ед.ч.) — рассказ, не метится")

	k, _ = kindOf(t, ctx, pool, workIDOf(t, ctx, pool, multi))
	require.Equal(t, "anthology", k, "≥4 авторов → anthology")

	k, src = kindOf(t, ctx, pool, workIDOf(t, ctx, pool, flBook))
	require.Empty(t, k, "метку fantlab эвристика не перетирает")
	require.Equal(t, "fantlab", src)

	// Идемпотентность: повторный прогон не падает, не меняет классификацию и
	// ничего не переписывает — даже у работ с несколькими сигналами (#300).
	changed, err = ClassifyWorkKinds(ctx, pool)
	require.NoError(t, err)
	require.Empty(t, changed)
	k, _ = kindOf(t, ctx, pool, workIDOf(t, ctx, pool, coll))
	require.Equal(t, "collection", k)
}

// TestClassifyWorkKinds_ConcurrentNoDeadlock — регрессия на deadlock первого
// деплоя: runOnce-классификация и after-import классификация стартовали
// одновременно, полнотабличные UPDATE works в разном порядке строк → deadlock,
// один вызов падал. Advisory-lock сериализует их. Здесь бьём НЕСКОЛЬКИМИ
// параллельными вызовами — с локом ВСЕ должны завершиться без ошибки.
func TestClassifyWorkKinds_ConcurrentNoDeadlock(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	collID, archID := seedTitleFixture(t, ctx, pool)
	auth := seedGroupAuthor(t, ctx, pool, "Шекли", "шекли роберт")
	// Достаточно строк, чтобы UPDATE'ы реально трогали работы (иначе лок не под
	// нагрузкой). Половина — сборники по названию, чтобы был ненулевой UPDATE.
	for i := 0; i < 40; i++ {
		title := "Роман номер " + string(rune('A'+i%26))
		if i%2 == 0 {
			title = "Сборник рассказов " + string(rune('A'+i%26))
		}
		seedGroupBook(t, ctx, pool, collID, archID, auth,
			"CC"+string(rune('A'+i%26))+string(rune('0'+i/26)),
			title, title, "ru", "", "", "")
	}

	const workers = 6
	errs := make(chan error, workers)
	start := make(chan struct{})
	for w := 0; w < workers; w++ {
		go func() {
			<-start // синхронный старт — максимизируем перекрытие UPDATE'ов
			_, err := ClassifyWorkKinds(ctx, pool)
			errs <- err
		}()
	}
	close(start)
	for w := 0; w < workers; w++ {
		require.NoError(t, <-errs, "конкурентная классификация не должна ловить deadlock")
	}
}

// TestClassifyWorkKinds_Rules284 — правила #284: серия-«сборники» красит работу,
// только если в ней все издания; «Миры X» и «Собрание сочинений» как серии — не
// сигнал; ≥4 авторов не для нон-фикшна; известный роман по серии и авторам не
// красится; переклассификация снимает тип, который правила больше не дают.
func TestClassifyWorkKinds_Rules284(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := testpg.Pool(t, ctx)
	collID, archID := seedTitleFixture(t, ctx, pool)
	bulg := seedGroupAuthor(t, ctx, pool, "Булгаков", "булгаков михаил")

	// Роман: одно издание в серии сборников, другое — нет.
	mm1 := seedGroupBook(t, ctx, pool, collID, archID, bulg, "MM1", "Мастер и Маргарита", "мастер и маргарита", "ru", "", "", "")
	mm2 := seedGroupBook(t, ctx, pool, collID, archID, bulg, "MM2", "Мастер и Маргарита", "мастер и маргарита", "ru", "", "", "")
	_, err := pool.Exec(ctx, `UPDATE books SET work_id = $1 WHERE id = $2`, workIDOf(t, ctx, pool, mm1), mm2)
	require.NoError(t, err)
	putInSeries(t, ctx, pool, mm1, "булгаков, михаил. сборники")
	mm := workIDOf(t, ctx, pool, mm1)

	// «Миры X» и «Собрание сочинений» — серии романов.
	worlds := seedGroupBook(t, ctx, pool, collID, archID, bulg, "W1", "Двенадцать стульев", "двенадцать стульев", "ru", "", "", "")
	putInSeries(t, ctx, pool, worlds, "миры ильфа и петрова")
	sobr := seedGroupBook(t, ctx, pool, collID, archID, bulg, "D1", "Три мушкетёра", "три мушкетёра", "ru", "", "", "")
	putInSeries(t, ctx, pool, sobr, "собрание сочинений")

	// Известный роман в серии сборников.
	famous := seedGroupBook(t, ctx, pool, collID, archID, bulg, "T1", "Трудно быть богом", "трудно быть богом", "ru", "", "", "")
	putInSeries(t, ctx, pool, famous, "стругацкие. сборники")
	_, err = pool.Exec(ctx, `UPDATE works SET fantlab_marks = 9000 WHERE id = $1`, workIDOf(t, ctx, pool, famous))
	require.NoError(t, err)

	// Нон-фикшн с четырьмя соавторами — не антология.
	var sciGenre int64
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO genres (fb2_code, name_ru) VALUES ('sci_history', 'История') ON CONFLICT (fb2_code) DO UPDATE SET name_ru = EXCLUDED.name_ru RETURNING id`).Scan(&sciGenre))
	sci := seedGroupBook(t, ctx, pool, collID, archID, bulg, "H1", "История Москвы", "история москвы", "ru", "", "", "")
	_, err = pool.Exec(ctx, `INSERT INTO book_genres (book_id, genre_id) VALUES ($1, $2)`, sci, sciGenre)
	require.NoError(t, err)
	for i, nm := range []string{"иванов", "петров", "сидоров"} {
		aid := seedGroupAuthor(t, ctx, pool, nm, nm)
		_, err := pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,$3)`, sci, aid, i+1)
		require.NoError(t, err)
	}

	// Помечены прежними правилами (как на проде до #284).
	for _, w := range []int64{mm, workIDOf(t, ctx, pool, worlds), workIDOf(t, ctx, pool, sobr), workIDOf(t, ctx, pool, famous), workIDOf(t, ctx, pool, sci)} {
		_, err := pool.Exec(ctx, `UPDATE works SET kind = 'omnibus', kind_source = 'heuristic' WHERE id = $1`, w)
		require.NoError(t, err)
	}
	// Настоящий сборник: единственное издание в серии сборников — остаётся.
	real := seedGroupBook(t, ctx, pool, collID, archID, bulg, "R1", "Дьяволиада", "дьяволиада", "ru", "", "", "")
	putInSeries(t, ctx, pool, real, "булгаков, михаил. сборники")

	changed, err := ReclassifyAllHeuristic(ctx, pool)
	require.NoError(t, err)
	for _, tc := range []struct {
		book int64
		why  string
	}{
		{mm1, "одно издание в серии сборников не красит роман"},
		{worlds, "«Миры X» — не сигнал"},
		{sobr, "«Собрание сочинений» как серия — не сигнал"},
		{famous, "известный роман по серии не красится"},
		{sci, "нон-фикшн с соавторами — не антология"},
	} {
		k, src := kindOf(t, ctx, pool, workIDOf(t, ctx, pool, tc.book))
		require.Empty(t, k, tc.why)
		require.Empty(t, src, tc.why)
		require.Contains(t, changed, workIDOf(t, ctx, pool, tc.book), "снятый тип — в изменённых: "+tc.why)
	}
	k, _ := kindOf(t, ctx, pool, workIDOf(t, ctx, pool, real))
	require.Equal(t, "omnibus", k, "все издания в серии сборников — сборник")
	require.Contains(t, changed, workIDOf(t, ctx, pool, real))

	again, err := ReclassifyAllHeuristic(ctx, pool)
	require.NoError(t, err)
	require.Empty(t, again, "повторная переклассификация ничего не меняет")
}
