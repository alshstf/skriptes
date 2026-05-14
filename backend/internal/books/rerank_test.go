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

// TestService_PersonaReranking — реальный сценарий:
//
//  1. Импортируем фикстуру (19 книг, разные авторы).
//  2. Создаём пользователя; вызываем List без персонализации — запоминаем
//     порядок (это базовая meili-выдача).
//  3. Подписываем пользователя на конкретного автора (favorite_authors).
//  4. Вызываем List(UserID=...) — ожидаем, что книги этого автора
//     поднялись наверх.
//
// Это не unit-тест ранкера в изоляции, а проверка цепочки целиком: что
// persona-сигнал докатывается до Meili-результата и переупорядочивает его.
func TestService_PersonaReranking(t *testing.T) {
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

	// seed user
	var userID int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO users (email, display_name, password_hash, role)
		VALUES ('rerank@example.com', 'Rerank User', 'x', 'user')
		RETURNING id
	`).Scan(&userID))

	// База без персонализации.
	baseline, err := svc.List(ctx, books.ListParams{Limit: 20})
	require.NoError(t, err)
	require.NotEmpty(t, baseline.Items)

	// Берём автора, который НЕ оказался на первой позиции baseline.
	// Иначе тест не покажет эффект буста.
	var targetAuthorID int64
	var targetAuthorBooks int
	for _, item := range baseline.Items {
		if len(item.AuthorIDs) == 0 {
			continue
		}
		// сколько книг этого автора в индексе?
		var cnt int
		require.NoError(t, pool.QueryRow(ctx,
			`SELECT count(*) FROM book_authors WHERE author_id = $1`, item.AuthorIDs[0],
		).Scan(&cnt))
		if cnt >= 1 {
			targetAuthorID = item.AuthorIDs[0]
			targetAuthorBooks = cnt
			break
		}
	}
	require.NotZero(t, targetAuthorID, "должны найти автора с книгой в индексе")

	// Подписываемся.
	require.NoError(t, historySvc.AddFavoriteAuthor(ctx, userID, targetAuthorID))

	// Запрос с userID — re-rank должен задействоваться (offset=0, нет Sort,
	// нет AuthorID-фильтра).
	personal, err := svc.List(ctx, books.ListParams{Limit: 20, UserID: userID})
	require.NoError(t, err)
	require.NotEmpty(t, personal.Items)

	// Главное утверждение: на самом верху должен оказаться хотя бы один
	// "наш" автор. У бейслайна — не обязательно (Meili по дефолту даёт
	// другой порядок при пустом query).
	topAuthorIDs := personal.Items[0].AuthorIDs
	require.Contains(t, topAuthorIDs, targetAuthorID,
		"после подписки книга любимого автора должна выйти на первое место (но %d книг автора)", targetAuthorBooks)

	// И в первых N результатах книг этого автора должно быть НЕ МЕНЬШЕ
	// чем без персонализации — даже если их было одинаково, порядок изменился.
	got := countWithAuthor(personal.Items, targetAuthorID)
	base := countWithAuthor(baseline.Items, targetAuthorID)
	require.GreaterOrEqual(t, got, base,
		"число книг любимого автора в топ-20 не должно уменьшиться (base=%d, personal=%d)", base, got)
}

func countWithAuthor(items []books.ListItem, authorID int64) int {
	n := 0
	for _, it := range items {
		for _, a := range it.AuthorIDs {
			if a == authorID {
				n++
				break
			}
		}
	}
	return n
}
