package books_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/history"
	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/skriptes/skriptes/backend/internal/testpg"
	"github.com/stretchr/testify/require"
)

// TestService_RerankOnlyOnQuery — два сценария в одном тесте:
//
//  1. Пустой query (главная страница /books): re-rank НЕ применяется.
//     Желание пользователя — стабильный порядок на главной, одинаковый
//     у всех пользователей. Проверяем что результат с UserID совпадает
//     с результатом без UserID.
//  2. Текстовый запрос (search): re-rank применяется, и книга подписанного
//     автора поднимается на первое место выдачи.
func TestService_RerankOnlyOnQuery(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := testpg.Pool(t, ctx)
	mgr := startMeilisearch(t, ctx)

	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr})
	abs, _ := filepath.Abs(fixtureINPX)
	stats, err := imp.Run(ctx, abs)
	require.NoError(t, err)
	require.Greater(t, stats.BooksIndexed, 5)

	historySvc := history.New(pool)
	svc := books.New(pool, mgr, historySvc)

	var userID int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO users (email, display_name, password_hash, role)
		VALUES ('rerank@example.com', 'Rerank User', 'x', 'user')
		RETURNING id
	`).Scan(&userID))

	// "Алек" в фикстуре матчит сразу несколько книг (Алексеев Евгений
	// и Алексеева Адель) — Meili ищет по searchableAttributes
	// title + authors + series, поэтому фамилия автора найдётся.
	const query = "Алек"

	// Берём первого автора, чьи книги попали в search-выдачу,
	// и который НЕ совпадает с автором, который сейчас на первом месте.
	baseline, err := svc.List(ctx, books.ListParams{Query: query, Limit: 10})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(baseline.Items), 2,
		"для теста нужны как минимум 2 книги — иначе re-rank ничего не поменяет")

	topAuthorIDs := baseline.Items[0].AuthorIDs
	require.NotEmpty(t, topAuthorIDs)

	var targetAuthorID int64
	for _, item := range baseline.Items[1:] {
		for _, aid := range item.AuthorIDs {
			if !containsID(topAuthorIDs, aid) {
				targetAuthorID = aid
				break
			}
		}
		if targetAuthorID != 0 {
			break
		}
	}
	require.NotZero(t, targetAuthorID,
		"нужен автор не из верхушки baseline — иначе тест не покажет эффект буста")

	require.NoError(t, historySvc.AddFavoriteAuthor(ctx, userID, targetAuthorID))

	// --- Сценарий 1: search с UserID → должен сработать re-rank.
	personal, err := svc.List(ctx, books.ListParams{Query: query, Limit: 10, UserID: userID})
	require.NoError(t, err)
	require.NotEmpty(t, personal.Items)
	require.Contains(t, personal.Items[0].AuthorIDs, targetAuthorID,
		"после подписки книга любимого автора должна быть на первой позиции при текстовом поиске")

	// --- Сценарий 2: пустой query с UserID → re-rank НЕ применяется.
	// Берём top-3 без UserID и с UserID — порядок должен совпадать.
	plain, err := svc.List(ctx, books.ListParams{Limit: 10})
	require.NoError(t, err)
	withUser, err := svc.List(ctx, books.ListParams{Limit: 10, UserID: userID})
	require.NoError(t, err)
	require.Equal(t, len(plain.Items), len(withUser.Items))
	for i := range plain.Items {
		require.Equal(t, plain.Items[i].ID, withUser.Items[i].ID,
			"на пустом query /books должен быть стабильный список — не должен меняться при наличии UserID (i=%d)", i)
	}
}

// TestService_SuggestRerank — palette typeahead тоже должен учитывать
// подписки: книги любимого автора всплывают наверх top-5.
func TestService_SuggestRerank(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := testpg.Pool(t, ctx)
	mgr := startMeilisearch(t, ctx)

	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr})
	abs, _ := filepath.Abs(fixtureINPX)
	_, err := imp.Run(ctx, abs)
	require.NoError(t, err)

	historySvc := history.New(pool)
	svc := books.New(pool, mgr, historySvc)

	var userID int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO users (email, display_name, password_hash, role)
		VALUES ('palette@example.com', 'Palette User', 'x', 'user')
		RETURNING id
	`).Scan(&userID))

	const query = "Алек"

	baseline, err := svc.Suggest(ctx, query, 5, 0, nil, nil)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(baseline), 2)

	// Цель — автор не из верхушки.
	topAuthorIDs := baseline[0].AuthorIDs
	var targetAuthorID int64
	for _, item := range baseline[1:] {
		for _, aid := range item.AuthorIDs {
			if !containsID(topAuthorIDs, aid) {
				targetAuthorID = aid
				break
			}
		}
		if targetAuthorID != 0 {
			break
		}
	}
	require.NotZero(t, targetAuthorID)

	require.NoError(t, historySvc.AddFavoriteAuthor(ctx, userID, targetAuthorID))

	personal, err := svc.Suggest(ctx, query, 5, userID, nil, nil)
	require.NoError(t, err)
	require.NotEmpty(t, personal)
	require.Contains(t, personal[0].AuthorIDs, targetAuthorID,
		"Suggest должен поднять книгу любимого автора в палитре поиска")
}

// TestService_BookLevelRerank — два самых ожидаемых пользователем сигнала:
//
//  1. Просмотр конкретной книги поднимает ИМЕННО её на повторном поиске.
//  2. Добавление книги в избранное поднимает её ещё выше (favorite > view).
//
// Без этих сигналов баг из ревью PR4: "посмотрел книгу — порядок не
// изменился; добавил в избранное — снова без эффекта".
func TestService_BookLevelRerank(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := testpg.Pool(t, ctx)
	mgr := startMeilisearch(t, ctx)

	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr})
	abs, _ := filepath.Abs(fixtureINPX)
	_, err := imp.Run(ctx, abs)
	require.NoError(t, err)

	historySvc := history.New(pool)
	svc := books.New(pool, mgr, historySvc)

	var userID int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO users (email, display_name, password_hash, role)
		VALUES ('book-rerank@example.com', 'Book Rerank', 'x', 'user')
		RETURNING id
	`).Scan(&userID))

	const query = "Алек"

	baseline, err := svc.List(ctx, books.ListParams{Query: query, Limit: 10})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(baseline.Items), 2)

	// Берём НЕ-первую книгу из baseline — её и будем "просматривать".
	target := baseline.Items[1]
	require.NotZero(t, target.ID)

	// Записываем view.
	require.NoError(t, historySvc.RecordView(ctx, userID, target.ID))

	afterView, err := svc.List(ctx, books.ListParams{Query: query, Limit: 10, UserID: userID})
	require.NoError(t, err)
	require.NotEmpty(t, afterView.Items)
	require.Equal(t, target.ID, afterView.Items[0].ID,
		"после view книга должна подняться на первое место (она получает book-level буст)")

	// Добавляем favorite — буст ещё сильнее.
	require.NoError(t, historySvc.AddFavorite(ctx, userID, target.ID))
	afterFav, err := svc.List(ctx, books.ListParams{Query: query, Limit: 10, UserID: userID})
	require.NoError(t, err)
	require.Equal(t, target.ID, afterFav.Items[0].ID,
		"после favorite та же книга остаётся на первом месте (favorite_book доминирует)")
}

func containsID(s []int64, x int64) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}

// TestService_RerankKeepsPageComposition — пересортировка первой страницы не
// меняет её СОСТАВ: страницы 1 и 2 вместе дают ровно те же работы, что Meili без
// пересортировки, без потерь и повторов (#277). С прежним окном 3×limit работа
// любимого автора с 3–4-й позиции уходила на первую страницу, а вытесненная с
// первой не попадала никуда.
func TestService_RerankKeepsPageComposition(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := testpg.Pool(t, ctx)
	mgr := startMeilisearch(t, ctx)
	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr})
	abs, _ := filepath.Abs(fixtureINPX)
	_, err := imp.Run(ctx, abs)
	require.NoError(t, err)
	historySvc := history.New(pool)
	svc := books.New(pool, mgr, historySvc)

	var userID int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO users (email, display_name, password_hash, role)
		VALUES ('pages@example.com', 'Pages User', 'x', 'user') RETURNING id`).Scan(&userID))

	// Запрос с ≥4 работами в фикстуре (префиксный поиск по одной букве).
	var query string
	var plain books.ListResponse
	for _, q := range []string{"Алек", "а", "о", "с", "и", "м"} {
		res, err := svc.ListWorks(ctx, books.ListParams{Query: q, Limit: 20})
		require.NoError(t, err)
		if len(res.Items) >= 4 {
			query, plain = q, res
			break
		}
	}
	require.NotEmpty(t, query, "для теста нужен запрос с ≥4 работами")

	// Любимый автор — у работы с 3–4-й позиции, которого нет в первых двух.
	first := map[int64]bool{}
	for _, it := range plain.Items[:2] {
		for _, a := range it.AuthorIDs {
			first[a] = true
		}
	}
	var fav int64
	for _, it := range plain.Items[2:4] {
		for _, a := range it.AuthorIDs {
			if !first[a] {
				fav = a
			}
		}
	}
	require.NotZero(t, fav)
	require.NoError(t, historySvc.AddFavoriteAuthor(ctx, userID, fav))

	p1, err := svc.ListWorks(ctx, books.ListParams{Query: query, Limit: 2, UserID: userID})
	require.NoError(t, err)
	p2, err := svc.ListWorks(ctx, books.ListParams{Query: query, Limit: 2, Offset: 2, UserID: userID})
	require.NoError(t, err)

	ids := func(items []books.ListItem) []int64 {
		out := make([]int64, 0, len(items))
		for _, it := range items {
			out = append(out, it.ID)
		}
		return out
	}
	require.ElementsMatch(t, ids(plain.Items[:2]), ids(p1.Items), "состав первой страницы = Meili")
	require.ElementsMatch(t, ids(plain.Items[2:4]), ids(p2.Items), "вторая страница продолжает первую")
}

// TestService_RerankNegatives — негативы персоны (план recsys/03, задача 3.5):
// блок-лист автора опускает его книгу в поиске, но мягко — не ниже чем на
// maxMalusDrop (3) позиций от места без малуса, и состав страницы прежний;
// «не интересно» из ленты — так же для работы в выдаче по индексу works.
func TestService_RerankNegatives(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	pool := testpg.Pool(t, ctx)
	mgr := startMeilisearch(t, ctx)
	imp := importer.New(importer.Deps{Pool: pool, Meili: mgr})
	abs, _ := filepath.Abs(fixtureINPX)
	_, err := imp.Run(ctx, abs)
	require.NoError(t, err)
	historySvc := history.New(pool)
	svc := books.New(pool, mgr, historySvc)

	var userID int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO users (email, display_name, password_hash, role)
		VALUES ('negatives@example.com', 'Negatives User', 'x', 'user') RETURNING id`).Scan(&userID))

	const maxDrop = 3 // books.maxMalusDrop

	// ── Блок-лист автора, индекс изданий (List / OPDS) ──
	const query = "Алек"
	baseline, err := svc.List(ctx, books.ListParams{Query: query, Limit: 10})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(baseline.Items), 2)
	top := baseline.Items[0]
	require.NotEmpty(t, top.AuthorIDs)
	require.NoError(t, historySvc.BlockAuthor(ctx, userID, top.AuthorIDs[0]))

	personal, err := svc.List(ctx, books.ListParams{Query: query, Limit: 10, UserID: userID})
	require.NoError(t, err)
	require.ElementsMatch(t, idsOf(baseline.Items), idsOf(personal.Items), "состав страницы не меняется")
	pos := indexOf(personal.Items, top.ID)
	require.Greater(t, pos, 0, "книга заблокированного автора опустилась")
	require.LessOrEqual(t, pos, maxDrop, "но не ниже предела")
	for _, it := range personal.Items[:pos] {
		require.NotContains(t, it.AuthorIDs, top.AuthorIDs[0], "выше неё — книги других авторов")
	}

	// ── «Не интересно» из ленты, индекс works (ListWorks) ──
	var wq string
	var plain books.ListResponse
	for _, q := range []string{"а", "о", "с", "и", "м"} {
		res, err := svc.ListWorks(ctx, books.ListParams{Query: q, Limit: 20})
		require.NoError(t, err)
		if len(res.Items) >= 4 {
			wq, plain = q, res
			break
		}
	}
	require.NotEmpty(t, wq, "нужен запрос с ≥ 4 работами")
	target := plain.Items[0]
	_, err = pool.Exec(ctx, `INSERT INTO feed_dismissals (user_id, work_id, reason) VALUES ($1, $2, 'not_interested')`, userID, target.ID)
	require.NoError(t, err)
	// Блок автора из первой части не должен мешать: снимем его.
	require.NoError(t, historySvc.UnblockAuthor(ctx, userID, top.AuthorIDs[0]))

	withUser, err := svc.ListWorks(ctx, books.ListParams{Query: wq, Limit: 20, UserID: userID})
	require.NoError(t, err)
	require.ElementsMatch(t, idsOf(plain.Items), idsOf(withUser.Items))
	// Место без малуса — по известности (ListWorks с UserID сортирует по ней);
	// считаем его отдельно: тот же пользователь без негативов.
	_, err = pool.Exec(ctx, `DELETE FROM feed_dismissals WHERE user_id = $1`, userID)
	require.NoError(t, err)
	require.NoError(t, historySvc.RecordView(ctx, userID, plain.Items[1].ID)) // непустой профиль без негативов
	ref, err := svc.ListWorks(ctx, books.ListParams{Query: wq, Limit: 20, UserID: userID})
	require.NoError(t, err)
	refPos := indexOf(ref.Items, target.ID)
	_, err = pool.Exec(ctx, `INSERT INTO feed_dismissals (user_id, work_id, reason) VALUES ($1, $2, 'not_interested')`, userID, target.ID)
	require.NoError(t, err)
	withUser, err = svc.ListWorks(ctx, books.ListParams{Query: wq, Limit: 20, UserID: userID})
	require.NoError(t, err)
	wpos := indexOf(withUser.Items, target.ID)
	require.GreaterOrEqual(t, wpos, refPos, "малус не поднимает")
	require.LessOrEqual(t, wpos, refPos+maxDrop, "и опускает не ниже предела")

	// «Уже читал» — не негатив: порядок как без скрытия.
	_, err = pool.Exec(ctx, `UPDATE feed_dismissals SET reason = 'already_read' WHERE user_id = $1`, userID)
	require.NoError(t, err)
	again, err := svc.ListWorks(ctx, books.ListParams{Query: wq, Limit: 20, UserID: userID})
	require.NoError(t, err)
	require.Equal(t, idsOf(ref.Items), idsOf(again.Items))
}

func idsOf(items []books.ListItem) []int64 {
	out := make([]int64, 0, len(items))
	for _, it := range items {
		out = append(out, it.ID)
	}
	return out
}

func indexOf(items []books.ListItem, id int64) int {
	for i, it := range items {
		if it.ID == id {
			return i
		}
	}
	return -1
}
