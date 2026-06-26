package metadata

import (
	"context"
	"encoding/json"
	"fmt"
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

// TestOverrides_LangReapply_Integration — lang (PR4): нормализация, ре-апплай после
// импорта (lang перетирается импортом), откат к свежеимпортированному значению.
func TestOverrides_LangReapply_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPGForPrewarm(t, ctx)
	collID, archID := seedTitleFixture(t, ctx, pool)
	author := seedGroupAuthor(t, ctx, pool, "Ланг", "ланг тест")
	bookID := seedGroupBook(t, ctx, pool, collID, archID, author, "L1", "Книга", "книга", "ru", "", "", "")
	ctl := NewOverrideController(pool, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// set lang ru→EN; нормализуется в lower.
	require.NoError(t, ctl.SetOverride(ctx, "book", bookID, "lang", json.RawMessage(`{"v":"EN"}`), 0))
	require.Equal(t, "en", ovText(t, ctx, pool, bookID, "lang"))

	// симулируем ре-импорт: импорт перетирает lang обратно на ru.
	_, err := pool.Exec(ctx, `UPDATE books SET lang='ru' WHERE id=$1`, bookID)
	require.NoError(t, err)
	// ReapplyAfterImport → lang снова en, original ← свежий ru.
	n, err := ctl.ReapplyAfterImport(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, "en", ovText(t, ctx, pool, bookID, "lang"))
	require.JSONEq(t, `{"v": "ru"}`, ovLedgerOriginal(t, ctx, pool, "book", bookID, "lang"))

	// откат → свежеимпортированный ru.
	require.NoError(t, ctl.RevertOverride(ctx, "book", bookID, "lang"))
	require.Equal(t, "ru", ovText(t, ctx, pool, bookID, "lang"))
	require.False(t, ovLedgerExists(t, ctx, pool, "book", bookID, "lang"))
}

// TestOverrides_WorkGenres_Integration — genres M:N (PR5): материализация набора
// жанров на все живые издания работы, per-edition снапшот original, ре-апплай после
// импорта (replaceBookGenres перетирает), откат к свежеимпортированному набору.
func TestOverrides_WorkGenres_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPGForPrewarm(t, ctx)
	collID, archID := seedTitleFixture(t, ctx, pool)
	author := seedGroupAuthor(t, ctx, pool, "Жанр", "жанр тест")
	bookID := seedGroupBook(t, ctx, pool, collID, archID, author, "G1", "Книга", "книга", "ru", "", "", "")
	var workID int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT work_id FROM books WHERE id=$1`, bookID).Scan(&workID))
	seedBookGenres(t, ctx, pool, bookID, "sf")
	require.Equal(t, []string{"sf"}, ovGenres(t, ctx, pool, bookID))

	ctl := NewOverrideController(pool, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// override набора жанров работы → {adv, det}; материализуется на издание.
	require.NoError(t, ctl.SetOverride(ctx, "work", workID, "genres", json.RawMessage(`{"codes":["det","adv"]}`), 0))
	require.Equal(t, []string{"adv", "det"}, ovGenres(t, ctx, pool, bookID))

	// симулируем ре-импорт: replaceBookGenres перетирает на {sf}.
	seedBookGenres(t, ctx, pool, bookID, "sf")
	require.Equal(t, []string{"sf"}, ovGenres(t, ctx, pool, bookID))
	// ReapplyAfterImport → снова {adv, det}, original ← свежий снапшот {sf}.
	n, err := ctl.ReapplyAfterImport(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, 1)
	require.Equal(t, []string{"adv", "det"}, ovGenres(t, ctx, pool, bookID))
	require.JSONEq(t, fmt.Sprintf(`{"editions":{"%d":["sf"]}}`, bookID),
		ovLedgerOriginal(t, ctx, pool, "work", workID, "genres"))

	// откат → свежеимпортированный {sf}.
	require.NoError(t, ctl.RevertOverride(ctx, "work", workID, "genres"))
	require.Equal(t, []string{"sf"}, ovGenres(t, ctx, pool, bookID))
	require.False(t, ovLedgerExists(t, ctx, pool, "work", workID, "genres"))
}

// seedBookGenres переписывает book_genres издания на codes (создаёт жанры по коду).
func seedBookGenres(t *testing.T, ctx context.Context, pool *pgxpool.Pool, bookID int64, codes ...string) {
	t.Helper()
	_, err := pool.Exec(ctx, `DELETE FROM book_genres WHERE book_id=$1`, bookID)
	require.NoError(t, err)
	for _, c := range codes {
		var gid int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO genres (fb2_code) VALUES ($1) ON CONFLICT (fb2_code) DO UPDATE SET fb2_code=EXCLUDED.fb2_code RETURNING id`,
			c).Scan(&gid))
		_, err := pool.Exec(ctx, `INSERT INTO book_genres (book_id, genre_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, bookID, gid)
		require.NoError(t, err)
	}
}

// ovGenres — fb2-коды жанров издания (отсортированы).
func ovGenres(t *testing.T, ctx context.Context, pool *pgxpool.Pool, bookID int64) []string {
	t.Helper()
	var codes []string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT COALESCE(array_agg(g.fb2_code ORDER BY g.fb2_code), '{}')
		FROM book_genres bg JOIN genres g ON g.id=bg.genre_id WHERE bg.book_id=$1`, bookID).Scan(&codes))
	return codes
}

// TestOverrides_WorkAuthors_Integration — authors M:N (PR6): материализация
// упорядоченного набора авторов на все живые издания + works.primary_author_id,
// ре-апплай после импорта (replaceBookAuthors перетирает), откат.
func TestOverrides_WorkAuthors_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPGForPrewarm(t, ctx)
	collID, archID := seedTitleFixture(t, ctx, pool)
	authorA := seedGroupAuthor(t, ctx, pool, "Алфа", "алфа автор")
	authorB := seedGroupAuthor(t, ctx, pool, "Бета", "бета автор")
	bookID := seedGroupBook(t, ctx, pool, collID, archID, authorA, "A1", "Книга", "книга", "ru", "", "", "")
	var workID int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT work_id FROM books WHERE id=$1`, bookID).Scan(&workID))
	_, err := pool.Exec(ctx, `UPDATE works SET primary_author_id=$1 WHERE id=$2`, authorA, workID)
	require.NoError(t, err)
	require.Equal(t, []int64{authorA}, ovAuthors(t, ctx, pool, bookID))

	ctl := NewOverrideController(pool, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// override авторов → [B]; материализуется на издание + primary=B.
	require.NoError(t, ctl.SetOverride(ctx, "work", workID, "authors",
		json.RawMessage(fmt.Sprintf(`{"author_ids":[%d]}`, authorB)), 0))
	require.Equal(t, []int64{authorB}, ovAuthors(t, ctx, pool, bookID))
	require.Equal(t, authorB, ovPrimaryAuthor(t, ctx, pool, workID))

	// симулируем ре-импорт: replaceBookAuthors → [A].
	seedBookAuthors(t, ctx, pool, bookID, authorA)
	require.Equal(t, []int64{authorA}, ovAuthors(t, ctx, pool, bookID))
	// ReapplyAfterImport → снова [B], primary=B.
	n, err := ctl.ReapplyAfterImport(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, 1)
	require.Equal(t, []int64{authorB}, ovAuthors(t, ctx, pool, bookID))
	require.Equal(t, authorB, ovPrimaryAuthor(t, ctx, pool, workID))

	// откат → [A], primary=A.
	require.NoError(t, ctl.RevertOverride(ctx, "work", workID, "authors"))
	require.Equal(t, []int64{authorA}, ovAuthors(t, ctx, pool, bookID))
	require.Equal(t, authorA, ovPrimaryAuthor(t, ctx, pool, workID))
	require.False(t, ovLedgerExists(t, ctx, pool, "work", workID, "authors"))
}

// seedBookAuthors переписывает book_authors издания на ids (position = индекс).
func seedBookAuthors(t *testing.T, ctx context.Context, pool *pgxpool.Pool, bookID int64, ids ...int64) {
	t.Helper()
	_, err := pool.Exec(ctx, `DELETE FROM book_authors WHERE book_id=$1`, bookID)
	require.NoError(t, err)
	for i, id := range ids {
		_, err := pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,$3)`, bookID, id, i)
		require.NoError(t, err)
	}
}

// ovAuthors — author_id издания (по position).
func ovAuthors(t *testing.T, ctx context.Context, pool *pgxpool.Pool, bookID int64) []int64 {
	t.Helper()
	var ids []int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COALESCE(array_agg(author_id ORDER BY position), '{}') FROM book_authors WHERE book_id=$1`,
		bookID).Scan(&ids))
	return ids
}

// ovPrimaryAuthor — works.primary_author_id (0, если NULL).
func ovPrimaryAuthor(t *testing.T, ctx context.Context, pool *pgxpool.Pool, workID int64) int64 {
	t.Helper()
	var id int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT COALESCE(primary_author_id,0) FROM works WHERE id=$1`, workID).Scan(&id))
	return id
}

// TestOverrides_WorkSeries_Integration — перенос между сериями (PR7): материализация
// в works.series_id/ser_no + ВСЕ издания books.series_id/ser_no, ре-апплай после
// импорта (перетирает издания), откат (works выводится из восстановленных изданий).
func TestOverrides_WorkSeries_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPGForPrewarm(t, ctx)
	collID, archID := seedTitleFixture(t, ctx, pool)
	author := seedGroupAuthor(t, ctx, pool, "Серия", "серия тест")
	seriesA := seedSeries(t, ctx, pool, "Серия А", author)
	seriesB := seedSeries(t, ctx, pool, "Серия Б", author)
	bookID := seedGroupBook(t, ctx, pool, collID, archID, author, "S1", "Книга", "книга", "ru", "", "", "")
	var workID int64
	require.NoError(t, pool.QueryRow(ctx, `SELECT work_id FROM books WHERE id=$1`, bookID).Scan(&workID))
	_, err := pool.Exec(ctx, `UPDATE books SET series_id=$1, ser_no=1 WHERE id=$2`, seriesA, bookID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `UPDATE works SET series_id=$1, ser_no=1 WHERE id=$2`, seriesA, workID)
	require.NoError(t, err)

	ctl := NewOverrideController(pool, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// перенос в серию Б, номер 2 → издание + работа.
	require.NoError(t, ctl.SetOverride(ctx, "work", workID, "series",
		json.RawMessage(fmt.Sprintf(`{"series_id":%d,"ser_no":2}`, seriesB)), 0))
	bsid, bsn := ovBookSeries(t, ctx, pool, bookID)
	require.Equal(t, seriesB, bsid)
	require.Equal(t, 2, bsn)
	wsid, wsn := ovWorkSeries(t, ctx, pool, workID)
	require.Equal(t, seriesB, wsid)
	require.Equal(t, 2, wsn)

	// симулируем ре-импорт: издание обратно в серию А, номер 1.
	_, err = pool.Exec(ctx, `UPDATE books SET series_id=$1, ser_no=1 WHERE id=$2`, seriesA, bookID)
	require.NoError(t, err)
	// ReapplyAfterImport → снова Б/2.
	n, err := ctl.ReapplyAfterImport(ctx)
	require.NoError(t, err)
	require.GreaterOrEqual(t, n, 1)
	bsid, bsn = ovBookSeries(t, ctx, pool, bookID)
	require.Equal(t, seriesB, bsid)
	require.Equal(t, 2, bsn)

	// откат → А/1 (works.series_id/ser_no выводится из восстановленных изданий).
	require.NoError(t, ctl.RevertOverride(ctx, "work", workID, "series"))
	bsid, bsn = ovBookSeries(t, ctx, pool, bookID)
	require.Equal(t, seriesA, bsid)
	require.Equal(t, 1, bsn)
	wsid, wsn = ovWorkSeries(t, ctx, pool, workID)
	require.Equal(t, seriesA, wsid)
	require.Equal(t, 1, wsn)
	require.False(t, ovLedgerExists(t, ctx, pool, "work", workID, "series"))
}

// seedSeries создаёт серию (normalized_title = lower(title)).
func seedSeries(t *testing.T, ctx context.Context, pool *pgxpool.Pool, title string, authorID int64) int64 {
	t.Helper()
	var id int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO series (title, normalized_title, author_id) VALUES ($1, lower($1), $2) RETURNING id`,
		title, authorID).Scan(&id))
	return id
}

// ovBookSeries — (series_id, ser_no) издания (0, если NULL).
func ovBookSeries(t *testing.T, ctx context.Context, pool *pgxpool.Pool, bookID int64) (int64, int) {
	t.Helper()
	var sid int64
	var sn int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COALESCE(series_id,0), COALESCE(ser_no,0) FROM books WHERE id=$1`, bookID).Scan(&sid, &sn))
	return sid, sn
}

// ovWorkSeries — (series_id, ser_no) работы (0, если NULL).
func ovWorkSeries(t *testing.T, ctx context.Context, pool *pgxpool.Pool, workID int64) (int64, int) {
	t.Helper()
	var sid int64
	var sn int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COALESCE(series_id,0), COALESCE(ser_no,0) FROM works WHERE id=$1`, workID).Scan(&sid, &sn))
	return sid, sn
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
