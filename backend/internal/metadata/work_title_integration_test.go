package metadata

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// seedTitleFixture поднимает чистую БД с коллекцией/архивом и возвращает их id.
func seedTitleFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (collID, archID int64) {
	t.Helper()
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	return collID, archID
}

// TestWorkGrouper_LocalizesTitle_Integration: после слияния «оригинал (en) +
// перевод (ru)» каноническое works.title локализуется на доминирующий язык
// библиотеки (ru), а не остаётся английским оригиналом — фикс рассинхрона
// карточки (works.title) и списка (b.title представителя), а также поиска.
func TestWorkGrouper_LocalizesTitle_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPGForPrewarm(t, ctx)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	collID, archID := seedTitleFixture(t, ctx, pool)

	asprin := seedGroupAuthor(t, ctx, pool, "Асприн", "асприн роберт")

	// Пара «оригинал (en) + перевод (ru)» — Tier-1 сольёт через src-title-info.
	en := seedGroupBook(t, ctx, pool, collID, archID, asprin, "A1",
		"Another Fine Myth", "another fine myth", "en", "", "", "")
	ru := seedGroupBook(t, ctx, pool, collID, archID, asprin, "A2",
		"Еще один великолепный МИФ", "еще один великолепный миф", "ru", "Another Fine Myth", "en", "асприн роберт")
	// Ещё ru-книги, чтобы русский стал доминирующим языком (ru=3 > en=1).
	seedGroupBook(t, ctx, pool, collID, archID, asprin, "A3", "Мифо-указания", "мифо-указания", "ru", "", "", "")
	seedGroupBook(t, ctx, pool, collID, archID, asprin, "A4", "Удача или миф", "удача или миф", "ru", "", "", "")

	g := NewWorkGrouper(pool, nil, nil, WorkGroupConfig{}, nil, quiet) // Tier-1 only
	g.drainAll(ctx)

	// Оригинал и перевод слиты в одну работу.
	wid := workIDOf(t, ctx, pool, en)
	require.Equal(t, wid, workIDOf(t, ctx, pool, ru), "перевод слит с оригиналом")

	// Каноническое название локализовано на русское издание.
	var title, ntitle string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT title, normalized_title FROM works WHERE id=$1`, wid).Scan(&title, &ntitle))
	require.Equal(t, "Еще один великолепный МИФ", title, "works.title локализован на ru-издание (не английский оригинал)")
	require.Equal(t, "еще один великолепный миф", ntitle, "normalized_title тоже переехал на ru")
}

// TestRecomputeWorkTitles_Behavior: recomputeWorkTitles меняет title только для
// работ, у которых ЕСТЬ издание в доминирующем языке; иноязычную работу без
// такого издания не трогает; возвращает id только реально изменённых.
func TestRecomputeWorkTitles_Behavior(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPGForPrewarm(t, ctx)
	collID, archID := seedTitleFixture(t, ctx, pool)
	author := seedGroupAuthor(t, ctx, pool, "Тест", "тест автор")

	// Работа A: en-оригинал + ru-перевод в одной работе, канон — английский.
	enA := seedGroupBook(t, ctx, pool, collID, archID, author, "B1",
		"Foreign Title", "foreign title", "en", "", "", "")
	ruA := seedGroupBook(t, ctx, pool, collID, archID, author, "B2",
		"Русский перевод", "русский перевод", "ru", "Foreign Title", "en", "тест автор")
	widA := workIDOf(t, ctx, pool, enA)
	_, err := pool.Exec(ctx, `UPDATE books SET work_id=$1 WHERE id=$2`, widA, ruA)
	require.NoError(t, err)
	// Работа B: только en-издание, русского перевода НЕТ — трогать нельзя.
	enB := seedGroupBook(t, ctx, pool, collID, archID, author, "B3",
		"English Only", "english only", "en", "", "", "")
	widB := workIDOf(t, ctx, pool, enB)

	changed, err := recomputeWorkTitles(ctx, pool, "ru", []int64{widA, widB})
	require.NoError(t, err)
	require.ElementsMatch(t, []int64{widA}, changed, "изменилась только работа с ru-изданием")

	var titleA, titleB string
	require.NoError(t, pool.QueryRow(ctx, `SELECT title FROM works WHERE id=$1`, widA).Scan(&titleA))
	require.NoError(t, pool.QueryRow(ctx, `SELECT title FROM works WHERE id=$1`, widB).Scan(&titleB))
	require.Equal(t, "Русский перевод", titleA, "работа с ru-изданием локализована")
	require.Equal(t, "English Only", titleB, "работа без ru-издания не тронута")

	// Идемпотентность: повторный вызов ничего не меняет.
	changed2, err := recomputeWorkTitles(ctx, pool, "ru", []int64{widA, widB})
	require.NoError(t, err)
	require.Empty(t, changed2, "повторный пересчёт — без изменений")
}
