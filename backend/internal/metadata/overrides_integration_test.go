package metadata

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/require"
)

// TestOverrides_EditionScalar_Integration — фундамент оверрайдов (PR1): материализация
// edition-скаляра в books.*, захват/держание оригинала, откат, OverridesForWork.
func TestOverrides_EditionScalar_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPGForPrewarm(t, ctx)
	collID, archID := seedTitleFixture(t, ctx, pool)
	author := seedGroupAuthor(t, ctx, pool, "Чарушин", "чарушин евгений")
	bookID := seedGroupBook(t, ctx, pool, collID, archID, author, "C1", "Рассказы", "рассказы", "ru", "", "", "")
	// Кейс-мотиватор: edition_year=1000 (явная ошибка в fb2).
	_, err := pool.Exec(ctx, `UPDATE books SET edition_year=1000, isbn=NULL WHERE id=$1`, bookID)
	require.NoError(t, err)

	ctl := NewOverrideController(pool, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 1) Правка 1000 → 2018; original захвачен = 1000.
	require.NoError(t, ctl.SetOverride(ctx, "book", bookID, "edition_year", json.RawMessage(`{"v":2018}`), 0))
	require.Equal(t, 2018, ovEditionYear(t, ctx, pool, bookID))
	require.JSONEq(t, `{"v": 1000}`, ovLedgerOriginal(t, ctx, pool, "book", bookID, "edition_year"))

	// 2) Повторная правка → 1949; original ДЕРЖИТСЯ = 1000 (откат к истинному).
	require.NoError(t, ctl.SetOverride(ctx, "book", bookID, "edition_year", json.RawMessage(`{"v":1949}`), 0))
	require.Equal(t, 1949, ovEditionYear(t, ctx, pool, bookID))
	require.JSONEq(t, `{"v": 1000}`, ovLedgerOriginal(t, ctx, pool, "book", bookID, "edition_year"))

	// 3) Откат → 1000; запись леджера удалена.
	require.NoError(t, ctl.RevertOverride(ctx, "book", bookID, "edition_year"))
	require.Equal(t, 1000, ovEditionYear(t, ctx, pool, bookID))
	require.False(t, ovLedgerExists(t, ctx, pool, "book", bookID, "edition_year"))

	// 4) Text-поле: NULL → значение → откат восстанавливает NULL.
	require.NoError(t, ctl.SetOverride(ctx, "book", bookID, "isbn", json.RawMessage(`{"v":"978-5-00000-000-0"}`), 0))
	require.Equal(t, "978-5-00000-000-0", ovText(t, ctx, pool, bookID, "isbn"))
	require.NoError(t, ctl.RevertOverride(ctx, "book", bookID, "isbn"))
	require.Equal(t, "", ovText(t, ctx, pool, bookID, "isbn")) // NULL → ""

	// 5) OverridesForWork отдаёт оверрайднутые поля издания.
	require.NoError(t, ctl.SetOverride(ctx, "book", bookID, "publisher", json.RawMessage(`{"v":"Детгиз"}`), 0))
	workID := workIDOf(t, ctx, pool, bookID)
	perBook, workFields, err := ctl.OverridesForWork(ctx, workID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"publisher"}, perBook[bookID])
	require.Empty(t, workFields)

	// 6) Неизвестное поле → ErrUnknownOverrideField; несуществующая книга → ErrOverrideTargetNotFound.
	require.ErrorIs(t, ctl.SetOverride(ctx, "book", bookID, "nope", json.RawMessage(`{"v":1}`), 0), ErrUnknownOverrideField)
	require.ErrorIs(t, ctl.SetOverride(ctx, "book", 999999, "isbn", json.RawMessage(`{"v":"x"}`), 0), ErrOverrideTargetNotFound)
}

// TestOverrides_WorkFields_Integration — work-поля (PR2): материализация в works.*,
// гейт recompute (правка ВЫЖИВАЕТ при группировке/локализации), откат.
func TestOverrides_WorkFields_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPGForPrewarm(t, ctx)
	collID, archID := seedTitleFixture(t, ctx, pool)
	author := seedGroupAuthor(t, ctx, pool, "Тест", "тест ворк")
	bookID := seedGroupBook(t, ctx, pool, collID, archID, author, "W1", "Старое", "старое", "ru", "", "", "")
	workID := workIDOf(t, ctx, pool, bookID)
	_, err := pool.Exec(ctx,
		`UPDATE works SET written_year=1990, written_year_source='fb2_title' WHERE id=$1`, workID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE books SET written_year=1990 WHERE id=$1`, bookID)
	require.NoError(t, err)

	ctl := NewOverrideController(pool, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// title: материализация в works.title + normalized_title.
	require.NoError(t, ctl.SetOverride(ctx, "work", workID, "title", json.RawMessage(`{"v":"Новое название"}`), 0))
	require.Equal(t, "Новое название", ovWorkText(t, ctx, pool, workID, "title"))
	require.Equal(t, "новое название", ovWorkText(t, ctx, pool, workID, "normalized_title"))

	// recompute-survival: recomputeWorkTitles(ru) НЕ перетирает title-оверрайд.
	changed, err := recomputeWorkTitles(ctx, pool, "ru", []int64{workID})
	require.NoError(t, err)
	require.NotContains(t, changed, workID)
	require.Equal(t, "Новое название", ovWorkText(t, ctx, pool, workID, "title"))

	// written_year: материализация + source='override'.
	require.NoError(t, ctl.SetOverride(ctx, "work", workID, "written_year", json.RawMessage(`{"v":1949}`), 0))
	require.Equal(t, 1949, ovWorkInt(t, ctx, pool, workID, "written_year"))
	require.Equal(t, "override", ovWorkText(t, ctx, pool, workID, "written_year_source"))

	// recompute-survival: recomputeWorkAggregates НЕ перетирает (книга=1990, но гейт).
	require.NoError(t, recomputeWorkAggregates(ctx, pool, []int64{workID}))
	require.Equal(t, 1949, ovWorkInt(t, ctx, pool, workID, "written_year"))

	// откат title → оригинал.
	require.NoError(t, ctl.RevertOverride(ctx, "work", workID, "title"))
	require.Equal(t, "Старое", ovWorkText(t, ctx, pool, workID, "title"))
	require.False(t, ovLedgerExists(t, ctx, pool, "work", workID, "title"))

	// откат written_year → 1990/fb2_title.
	require.NoError(t, ctl.RevertOverride(ctx, "work", workID, "written_year"))
	require.Equal(t, 1990, ovWorkInt(t, ctx, pool, workID, "written_year"))
	require.Equal(t, "fb2_title", ovWorkText(t, ctx, pool, workID, "written_year_source"))

	// ser_no: материализация в works.ser_no + гейт series-recompute (книга без
	// серии → recompute занулил бы, но оверрайд выживает).
	_, err = pool.Exec(ctx, `UPDATE works SET ser_no=5 WHERE id=$1`, workID)
	require.NoError(t, err)
	require.NoError(t, ctl.SetOverride(ctx, "work", workID, "ser_no", json.RawMessage(`{"v":1}`), 0))
	require.Equal(t, 1, ovWorkInt(t, ctx, pool, workID, "ser_no"))
	require.NoError(t, recomputeWorkAggregates(ctx, pool, []int64{workID}))
	require.Equal(t, 1, ovWorkInt(t, ctx, pool, workID, "ser_no")) // гейт series UPDATE
	require.NoError(t, ctl.RevertOverride(ctx, "work", workID, "ser_no"))
	require.Equal(t, 5, ovWorkInt(t, ctx, pool, workID, "ser_no"))
}

func ovWorkText(t *testing.T, ctx context.Context, pool *pgxpool.Pool, workID int64, col string) string {
	t.Helper()
	var s *string
	require.NoError(t, pool.QueryRow(ctx, `SELECT `+col+`::text FROM works WHERE id=$1`, workID).Scan(&s))
	if s == nil {
		return ""
	}
	return *s
}

func ovWorkInt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, workID int64, col string) int {
	t.Helper()
	var n *int
	require.NoError(t, pool.QueryRow(ctx, `SELECT `+col+` FROM works WHERE id=$1`, workID).Scan(&n))
	if n == nil {
		return 0
	}
	return *n
}

func ovEditionYear(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int64) int {
	t.Helper()
	var y *int
	require.NoError(t, pool.QueryRow(ctx, `SELECT edition_year FROM books WHERE id=$1`, id).Scan(&y))
	if y == nil {
		return 0
	}
	return *y
}

func ovText(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int64, col string) string {
	t.Helper()
	var s *string
	require.NoError(t, pool.QueryRow(ctx, `SELECT `+col+` FROM books WHERE id=$1`, id).Scan(&s))
	if s == nil {
		return ""
	}
	return *s
}

func ovLedgerOriginal(t *testing.T, ctx context.Context, pool *pgxpool.Pool, kind string, id int64, field string) string {
	t.Helper()
	var raw string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT original_value::text FROM metadata_overrides WHERE target_kind=$1 AND target_id=$2 AND field=$3`,
		kind, id, field).Scan(&raw))
	return raw
}

func ovLedgerExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, kind string, id int64, field string) bool {
	t.Helper()
	var ex bool
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM metadata_overrides WHERE target_kind=$1 AND target_id=$2 AND field=$3)`,
		kind, id, field).Scan(&ex))
	return ex
}
