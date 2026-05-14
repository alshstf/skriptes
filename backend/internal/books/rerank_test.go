package books_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/skriptes/skriptes/backend/internal/books"
	"github.com/skriptes/skriptes/backend/internal/history"
	"github.com/skriptes/skriptes/backend/internal/importer"
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

	pool := startPostgres(t, ctx)
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

	pool := startPostgres(t, ctx)
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

	baseline, err := svc.Suggest(ctx, query, 5, 0)
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

	personal, err := svc.Suggest(ctx, query, 5, userID)
	require.NoError(t, err)
	require.NotEmpty(t, personal)
	require.Contains(t, personal[0].AuthorIDs, targetAuthorID,
		"Suggest должен поднять книгу любимого автора в палитре поиска")
}

func containsID(s []int64, x int64) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}
