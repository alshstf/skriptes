package history_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	meili "github.com/meilisearch/meilisearch-go"
	"github.com/skriptes/skriptes/backend/internal/db"
	"github.com/skriptes/skriptes/backend/internal/history"
	"github.com/skriptes/skriptes/backend/internal/importer"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcmeili "github.com/testcontainers/testcontainers-go/modules/meilisearch"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const fixtureINPX = "../inpx/testdata/test.inpx"

// TestService_HistoryFlow — реальный PG + Meili через testcontainers.
// Сценарий:
//  1. импортируем фикстуру → есть >=1 живой книга и хотя бы 1 пользователь;
//  2. создаём seed-пользователя (admin), вызываем все методы Service.
func TestService_HistoryFlow(t *testing.T) {
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

	// seed user
	var userID int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO users (email, display_name, password_hash, role)
		VALUES ('test@example.com', 'Test User', 'x', 'user')
		RETURNING id
	`).Scan(&userID))

	// возьмём какую-нибудь живую книгу из фикстуры
	var bookID int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id FROM books WHERE deleted = false ORDER BY id LIMIT 1`,
	).Scan(&bookID))

	svc := history.New(pool)

	// view → recent должен показать ровно одну запись
	require.NoError(t, svc.RecordView(ctx, userID, bookID))
	recent, err := svc.RecentViews(ctx, userID, 10)
	require.NoError(t, err)
	require.Len(t, recent, 1)
	require.Equal(t, bookID, recent[0].ID)

	// read (upsert): два вызова не должны добавить строки
	require.NoError(t, svc.RecordRead(ctx, userID, bookID))
	require.NoError(t, svc.RecordRead(ctx, userID, bookID))
	var readsCount int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM reads WHERE user_id = $1 AND book_id = $2`,
		userID, bookID,
	).Scan(&readsCount))
	require.Equal(t, 1, readsCount)

	// IsRead: RecordRead не должен ставить completed_at → false.
	isRead, err := svc.IsRead(ctx, userID, bookID)
	require.NoError(t, err)
	require.False(t, isRead, "RecordRead не считается прочитыванием — только download/access")

	// MarkRead — явная отметка прочитанным. Идемпотентна.
	require.NoError(t, svc.MarkRead(ctx, userID, bookID))
	require.NoError(t, svc.MarkRead(ctx, userID, bookID))
	isRead, err = svc.IsRead(ctx, userID, bookID)
	require.NoError(t, err)
	require.True(t, isRead)

	// UnmarkRead — снимает флаг, но строку оставляет (для re-ranking-сигналов).
	require.NoError(t, svc.UnmarkRead(ctx, userID, bookID))
	isRead, err = svc.IsRead(ctx, userID, bookID)
	require.NoError(t, err)
	require.False(t, isRead)
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM reads WHERE user_id = $1 AND book_id = $2`,
		userID, bookID,
	).Scan(&readsCount))
	require.Equal(t, 1, readsCount, "UnmarkRead не должен удалять строку")

	// SavePosition / GetPosition: epub-cfi (TEXT) + fraction.
	const cfi = "epubcfi(/6/4!/4/2,/1:0,/4/3:120)"
	frac := 0.37
	require.NoError(t, svc.SavePosition(ctx, userID, bookID, cfi, &frac))
	gotPos, err := svc.GetPosition(ctx, userID, bookID)
	require.NoError(t, err)
	require.Equal(t, cfi, gotPos)

	// ReadStatus после SavePosition: книга НЕ прочитана (была UnmarkRead
	// выше), но fraction сохранён.
	rs, ca, fr, err := svc.ReadStatus(ctx, userID, bookID)
	require.NoError(t, err)
	require.False(t, rs)
	require.Nil(t, ca)
	require.NotNil(t, fr)
	require.InDelta(t, 0.37, *fr, 0.001)

	// fraction зажимается в [0,1] — мусор отбрасывается.
	out := 5.0
	require.NoError(t, svc.SavePosition(ctx, userID, bookID, cfi, &out))
	_, _, fr, err = svc.ReadStatus(ctx, userID, bookID)
	require.NoError(t, err)
	require.NotNil(t, fr)
	require.InDelta(t, 1.0, *fr, 0.001, "fraction clamped to 1.0")

	neg := -0.5
	require.NoError(t, svc.SavePosition(ctx, userID, bookID, cfi, &neg))
	_, _, fr, err = svc.ReadStatus(ctx, userID, bookID)
	require.NoError(t, err)
	require.NotNil(t, fr)
	require.InDelta(t, 0.0, *fr, 0.001, "fraction clamped to 0.0")

	// fraction=nil НЕ перетирает прежнее значение (COALESCE в SQL).
	require.NoError(t, svc.SavePosition(ctx, userID, bookID, cfi, nil))
	_, _, fr, err = svc.ReadStatus(ctx, userID, bookID)
	require.NoError(t, err)
	require.NotNil(t, fr)
	require.InDelta(t, 0.0, *fr, 0.001, "nil fraction не должен перетирать прежнее значение (0.0)")

	// Пустая строка pos — сбрасывает позицию (last_pos → NULL).
	require.NoError(t, svc.SavePosition(ctx, userID, bookID, "", nil))
	gotPos, err = svc.GetPosition(ctx, userID, bookID)
	require.NoError(t, err)
	require.Empty(t, gotPos)

	// GetPosition / ReadStatus для книги без записи в reads — пустые значения, не ошибка.
	gotPos, err = svc.GetPosition(ctx, userID, 999999)
	require.NoError(t, err)
	require.Empty(t, gotPos)
	rs, ca, fr, err = svc.ReadStatus(ctx, userID, 999999)
	require.NoError(t, err)
	require.False(t, rs)
	require.Nil(t, ca)
	require.Nil(t, fr)

	// ReadStatus после MarkRead — completed_at не nil.
	require.NoError(t, svc.MarkRead(ctx, userID, bookID))
	rs, ca, _, err = svc.ReadStatus(ctx, userID, bookID)
	require.NoError(t, err)
	require.True(t, rs)
	require.NotNil(t, ca)

	// MarkRead для новой пары (user, book) — создаёт строку даже без
	// предварительного RecordRead. Это сценарий «пользователь читает на
	// Kindle, потом отмечает прочитанным в UI, не скачивая через сайт».
	var freshBookID int64
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title)
		SELECT collection_id, archive_id, 'L-MARK-FRESH', 'fmark', 'fb2', 'Mark Fresh', 'mark fresh'
		FROM books WHERE id = $1
		RETURNING id
	`, bookID).Scan(&freshBookID))
	require.NoError(t, svc.MarkRead(ctx, userID, freshBookID))
	isRead, err = svc.IsRead(ctx, userID, freshBookID)
	require.NoError(t, err)
	require.True(t, isRead)

	// favorites: add — повторный no-op — IsFavorite=true — List вернёт книгу
	require.NoError(t, svc.AddFavorite(ctx, userID, bookID))
	require.NoError(t, svc.AddFavorite(ctx, userID, bookID))
	fav, err := svc.IsFavorite(ctx, userID, bookID)
	require.NoError(t, err)
	require.True(t, fav)

	list, err := svc.ListFavorites(ctx, userID, 50, 0)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, bookID, list[0].ID)

	cnt, err := svc.FavoritesCount(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, 1, cnt)

	// remove → idempotent
	require.NoError(t, svc.RemoveFavorite(ctx, userID, bookID))
	require.NoError(t, svc.RemoveFavorite(ctx, userID, bookID))
	fav, err = svc.IsFavorite(ctx, userID, bookID)
	require.NoError(t, err)
	require.False(t, fav)
	cnt, err = svc.FavoritesCount(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, 0, cnt)

	// ── favorites: авторы и серии ───────────────────────────────
	// Берём какого-нибудь автора и серию из фикстуры.
	var authorID, seriesID int64
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT a.id FROM authors a
		 JOIN book_authors ba ON ba.author_id = a.id
		 JOIN books b ON b.id = ba.book_id AND b.deleted = false
		 LIMIT 1`,
	).Scan(&authorID))
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT id FROM series LIMIT 1`,
	).Scan(&seriesID))

	// Авторы: add → IsFavorite=true → List → remove → IsFavorite=false.
	require.NoError(t, svc.AddFavoriteAuthor(ctx, userID, authorID))
	require.NoError(t, svc.AddFavoriteAuthor(ctx, userID, authorID)) // idempotent
	favA, err := svc.IsFavoriteAuthor(ctx, userID, authorID)
	require.NoError(t, err)
	require.True(t, favA)
	authors, err := svc.ListFavoriteAuthors(ctx, userID, 50, 0)
	require.NoError(t, err)
	require.Len(t, authors, 1)
	require.Equal(t, authorID, authors[0].ID)
	require.NotEmpty(t, authors[0].FullName)
	require.GreaterOrEqual(t, authors[0].BookCount, 1)
	require.NoError(t, svc.RemoveFavoriteAuthor(ctx, userID, authorID))
	favA, err = svc.IsFavoriteAuthor(ctx, userID, authorID)
	require.NoError(t, err)
	require.False(t, favA)

	// Серии: симметрично.
	require.NoError(t, svc.AddFavoriteSeries(ctx, userID, seriesID))
	require.NoError(t, svc.AddFavoriteSeries(ctx, userID, seriesID)) // idempotent
	favS, err := svc.IsFavoriteSeries(ctx, userID, seriesID)
	require.NoError(t, err)
	require.True(t, favS)
	seriesList, err := svc.ListFavoriteSeries(ctx, userID, 50, 0)
	require.NoError(t, err)
	require.Len(t, seriesList, 1)
	require.Equal(t, seriesID, seriesList[0].ID)
	require.GreaterOrEqual(t, seriesList[0].BookCount, 1)
	require.NoError(t, svc.RemoveFavoriteSeries(ctx, userID, seriesID))
	favS, err = svc.IsFavoriteSeries(ctx, userID, seriesID)
	require.NoError(t, err)
	require.False(t, favS)
}

// TestService_WorkLevelFavoriteRead — избранное/прочитано на уровне КНИГИ:
// избрали/прочитали одно издание → вся работа считается избранной/прочитанной.
func TestService_WorkLevelFavoriteRead(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPostgres(t, ctx)

	var userID, collID, archID, workID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ('w@e.com','W','x','user') RETURNING id`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO works (title, normalized_title) VALUES ('Оно','оно') RETURNING id`).Scan(&workID))
	mk := func(lib string) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id)
			VALUES ($1,$2,$3,$3,'fb2','Оно','оно',$4) RETURNING id`, collID, archID, lib, workID).Scan(&id))
		return id
	}
	_ = mk("L1")
	e2 := mk("L2")

	svc := history.New(pool)
	fav, err := svc.IsWorkFavorite(ctx, userID, workID)
	require.NoError(t, err)
	require.False(t, fav)
	rd, _, err := svc.WorkReadStatus(ctx, userID, workID)
	require.NoError(t, err)
	require.False(t, rd)

	// Избрали и прочитали ВТОРОЕ издание. Избранное — через сервис (служебная
	// полка kind='favorites', миграция 0023; таблицы favorites больше нет).
	require.NoError(t, svc.AddFavorite(ctx, userID, e2))
	_, err = pool.Exec(ctx, `INSERT INTO reads (user_id, book_id, completed_at) VALUES ($1,$2,now())`, userID, e2)
	require.NoError(t, err)

	fav, err = svc.IsWorkFavorite(ctx, userID, workID)
	require.NoError(t, err)
	require.True(t, fav, "избранное любого издания ⇒ книга избрана")
	rd, ca, err := svc.WorkReadStatus(ctx, userID, workID)
	require.NoError(t, err)
	require.True(t, rd, "прочитано любое издание ⇒ книга прочитана")
	require.NotNil(t, ca)
}

// TestService_Ratings — пользовательские оценки (work-level): set/update/remove,
// валидация 1–5, агрегат (средняя + число голосов по инстансу).
func TestService_Ratings(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPostgres(t, ctx)

	var u1, u2, workID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ('r1@e.com','R1','x','user') RETURNING id`).Scan(&u1))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ('r2@e.com','R2','x','user') RETURNING id`).Scan(&u2))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO works (title, normalized_title) VALUES ('Война и мир','война и мир') RETURNING id`).Scan(&workID))

	svc := history.New(pool)

	// Нет оценок → агрегат пустой, у юзера оценки нет.
	avg, cnt, err := svc.WorkRatingAggregate(ctx, workID)
	require.NoError(t, err)
	require.Zero(t, cnt)
	require.Zero(t, avg)
	_, has, err := svc.UserRating(ctx, u1, workID)
	require.NoError(t, err)
	require.False(t, has)

	// Валидация диапазона.
	require.ErrorIs(t, svc.SetRating(ctx, u1, workID, 0), history.ErrInvalidRating)
	require.ErrorIs(t, svc.SetRating(ctx, u1, workID, 6), history.ErrInvalidRating)

	// u1=4, u2=2 → avg 3.0, count 2.
	require.NoError(t, svc.SetRating(ctx, u1, workID, 4))
	require.NoError(t, svc.SetRating(ctx, u2, workID, 2))
	r, has, err := svc.UserRating(ctx, u1, workID)
	require.NoError(t, err)
	require.True(t, has)
	require.Equal(t, 4, r)
	avg, cnt, err = svc.WorkRatingAggregate(ctx, workID)
	require.NoError(t, err)
	require.Equal(t, 2, cnt)
	require.InDelta(t, 3.0, avg, 0.001)

	// Изменить оценку (upsert): u1 4→5 → avg 3.5.
	require.NoError(t, svc.SetRating(ctx, u1, workID, 5))
	r, _, err = svc.UserRating(ctx, u1, workID)
	require.NoError(t, err)
	require.Equal(t, 5, r)
	avg, _, err = svc.WorkRatingAggregate(ctx, workID)
	require.NoError(t, err)
	require.InDelta(t, 3.5, avg, 0.001)

	// Снять оценку u1 → остаётся u2=2 (count 1, avg 2.0); повторный DELETE — no-op.
	require.NoError(t, svc.RemoveRating(ctx, u1, workID))
	require.NoError(t, svc.RemoveRating(ctx, u1, workID))
	_, has, err = svc.UserRating(ctx, u1, workID)
	require.NoError(t, err)
	require.False(t, has)
	avg, cnt, err = svc.WorkRatingAggregate(ctx, workID)
	require.NoError(t, err)
	require.Equal(t, 1, cnt)
	require.InDelta(t, 2.0, avg, 0.001)
}

// TestService_RatingPrompts — отложенные запросы оценки: приобретение
// (earliest-wins), eligibility (задержка / read_signal / snooze / never с
// override read_signal'ом), авто-«Прочитана» при оценке.
func TestService_RatingPrompts(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPostgres(t, ctx)

	var userID, collID, archID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ('rp@e.com','RP','x','user') RETURNING id`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))

	mkWork := func(title string) int64 {
		var w int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO works (title, normalized_title) VALUES ($1,$2) RETURNING id`, title, title).Scan(&w))
		return w
	}
	mkBook := func(lib string, work int64) int64 {
		var id int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id)
			VALUES ($1,$2,$3,$3,'fb2',$4,$5,$6) RETURNING id`, collID, archID, lib, lib, lib, work).Scan(&id))
		return id
	}
	agePast := func(bookID int64) {
		_, err := pool.Exec(ctx,
			`UPDATE reads SET acquired_at = now() - interval '40 days' WHERE user_id=$1 AND book_id=$2`, userID, bookID)
		require.NoError(t, err)
	}
	svc := history.New(pool)
	inPool := func() map[int64]bool {
		items, err := svc.RateableWorks(ctx, userID, 30, 50)
		require.NoError(t, err)
		m := map[int64]bool{}
		for _, it := range items {
			m[it.WorkID] = true
		}
		return m
	}

	// W1 — приобретена 40 дней назад → пригодна по задержке.
	w1, b1 := mkWork("W1"), int64(0)
	b1 = mkBook("L1", w1)
	require.NoError(t, svc.RecordAcquisition(ctx, userID, b1))
	agePast(b1)
	require.NoError(t, svc.RecordAcquisition(ctx, userID, b1)) // повтор не перетирает acquired_at
	var acq time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT acquired_at FROM reads WHERE user_id=$1 AND book_id=$2`, userID, b1).Scan(&acq))
	require.Greater(t, time.Since(acq), 30*24*time.Hour, "acquired_at earliest-wins")

	// W2 — приобретена только что → по задержке ещё НЕ пригодна.
	w2 := mkWork("W2")
	b2 := mkBook("L2", w2)
	require.NoError(t, svc.RecordAcquisition(ctx, userID, b2))

	p := inPool()
	require.True(t, p[w1], "приобретена давно → в пуле")
	require.False(t, p[w2], "приобретена только что → не в пуле")

	// W2 → явная «Прочитана» (read_signal) → пригодна сразу.
	require.NoError(t, svc.MarkRead(ctx, userID, b2))
	require.True(t, inPool()[w2], "read_signal даёт пригодность сразу")

	// snooze W1 → ушла из пула.
	require.NoError(t, svc.SnoozeRatingPrompt(ctx, userID, w1, 30))
	require.False(t, inPool()[w1], "snooze скрывает")

	// never для прочитанной W2 → НЕ скрывает (read_signal перебивает).
	require.NoError(t, svc.DismissRatingPrompt(ctx, userID, w2))
	require.True(t, inPool()[w2], "read_signal перебивает never")

	// W3 — давняя + never → скрыта (нет read_signal); отметили прочитанной → вернулась.
	w3 := mkWork("W3")
	b3 := mkBook("L3", w3)
	require.NoError(t, svc.RecordAcquisition(ctx, userID, b3))
	agePast(b3)
	require.True(t, inPool()[w3])
	require.NoError(t, svc.DismissRatingPrompt(ctx, userID, w3))
	require.False(t, inPool()[w3], "never скрывает без read_signal")
	require.NoError(t, svc.MarkRead(ctx, userID, b3))
	require.True(t, inPool()[w3], "прочтение возвращает скрытую never")

	// Оценил W3 → ушёл из пула + авто-«Прочитана» (уже стояла, но проверим контракт).
	require.NoError(t, svc.SetRating(ctx, userID, w3, 4))
	require.False(t, inPool()[w3], "оценённое не в пуле")
	rd, _, err := svc.WorkReadStatus(ctx, userID, w3)
	require.NoError(t, err)
	require.True(t, rd, "оценка авто-проставляет «Прочитана»")

	// Оценка работы БЕЗ предшествующего чтения (W2 не была б оценена) — авто-mark.
	require.NoError(t, svc.SetRating(ctx, userID, w1, 5))
	rd, _, err = svc.WorkReadStatus(ctx, userID, w1)
	require.NoError(t, err)
	require.True(t, rd, "оценка W1 авто-проставила «Прочитана»")
}

// TestService_ContinueReading — блок «Продолжить чтение» на Главной:
//   - возвращает только книги с прогрессом (fraction > 0) и НЕ дочитанные
//     (completed_at IS NULL);
//   - дочитанные и нетронутые (fraction = 0 / нет записи) — не попадают;
//   - сортировка по updated_at DESC.
func TestService_ContinueReading(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := startPostgres(t, ctx)

	var userID, collID, archID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ('cr@e.com','CR','x','user') RETURNING id`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))

	// mkBook — книга + singleton-работа (инвариант work_id), возвращает (bookID, workID).
	mkBook := func(lib, title string) (int64, int64) {
		var wid int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO works (title, normalized_title) VALUES ($1, lower($1)) RETURNING id`, title).Scan(&wid))
		var bid int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id)
			VALUES ($1,$2,$3,$3,'fb2',$4, lower($4), $5) RETURNING id`,
			collID, archID, lib, title, wid).Scan(&bid))
		return bid, wid
	}

	inProgress, ipWork := mkBook("L-IP", "В процессе")
	finished, _ := mkBook("L-FIN", "Дочитана")
	untouched, _ := mkBook("L-UNT", "Не тронута")

	svc := history.New(pool)

	// Пусто пока нет ни одной записи reads.
	items, err := svc.ContinueReading(ctx, userID, 20)
	require.NoError(t, err)
	require.Empty(t, items)

	// in-progress: fraction>0, completed_at NULL.
	frac := 0.42
	require.NoError(t, svc.SavePosition(ctx, userID, inProgress, "cfi-1", &frac))
	// finished: есть прогресс, но дочитана → не в выдаче.
	f2 := 0.9
	require.NoError(t, svc.SavePosition(ctx, userID, finished, "cfi-2", &f2))
	require.NoError(t, svc.MarkRead(ctx, userID, finished))
	// untouched: только RecordRead (fraction остаётся NULL/0) → не в выдаче.
	require.NoError(t, svc.RecordRead(ctx, userID, untouched))

	items, err = svc.ContinueReading(ctx, userID, 20)
	require.NoError(t, err)
	require.Len(t, items, 1, "только in-progress книга")
	require.Equal(t, inProgress, items[0].ID)
	require.Equal(t, ipWork, items[0].WorkID)
	require.InDelta(t, 0.42, items[0].Fraction, 0.001)
	require.Equal(t, "В процессе", items[0].Title)
}

// TestService_SubscriptionFeed — блок «Новинки по подписанным авторам»:
//   - книги авторов из favorite_authors, отсортированные по date_added DESC;
//   - книги не-подписанных авторов не попадают;
//   - схлопывание по work_id (одна работа один раз);
//   - пустая выдача, если подписок нет.
func TestService_SubscriptionFeed(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool := startPostgres(t, ctx)

	var userID, collID, archID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ('sf@e.com','SF','x','user') RETURNING id`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO collections (name, inpx_filename) VALUES ('t','t.inpx') RETURNING id`).Scan(&collID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO archives (collection_id, filename) VALUES ($1,'a.zip') RETURNING id`, collID).Scan(&archID))

	var subAuthor, otherAuthor int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO authors (last_name, first_name, normalized_name) VALUES ('Подписан','Автор','подписан автор') RETURNING id`).Scan(&subAuthor))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO authors (last_name, first_name, normalized_name) VALUES ('Другой','Автор','другой автор') RETURNING id`).Scan(&otherAuthor))

	svc := history.New(pool)

	// Без подписок — пусто.
	items, err := svc.SubscriptionFeed(ctx, userID, 20)
	require.NoError(t, err)
	require.Empty(t, items)

	// mkBook — книга + singleton-работа + привязка автора + date_added.
	// Возвращает work_id (id издания тесту не нужен).
	mkBook := func(lib, title string, authorID int64, dateAdded string) int64 {
		var wid int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO works (title, normalized_title) VALUES ($1, lower($1)) RETURNING id`, title).Scan(&wid))
		var bid int64
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id, date_added)
			VALUES ($1,$2,$3,$3,'fb2',$4, lower($4), $5, $6) RETURNING id`,
			collID, archID, lib, title, wid, dateAdded).Scan(&bid))
		_, err := pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id) VALUES ($1,$2)`, bid, authorID)
		require.NoError(t, err)
		return wid
	}

	wNew := mkBook("L-NEW", "Новая", subAuthor, "2024-01-10") // ПОСЛЕ подписки
	mkBook("L-OLD", "Старая", subAuthor, "2020-01-01")        // ДО подписки — не новинка
	mkBook("L-OTH", "Чужая", otherAuthor, "2025-01-01")       // не подписан — не должна попасть

	// Подписка на автора датирована прошлым (2023-06-01): новинки = книги,
	// добавленные ПОСЛЕ этой даты. svc.AddFavoriteAuthor ставит added_at=now(),
	// поэтому вставляем подписку напрямую с нужной датой.
	_, err = pool.Exec(ctx,
		`INSERT INTO favorite_authors (user_id, author_id, added_at) VALUES ($1,$2,'2023-06-01')`,
		userID, subAuthor)
	require.NoError(t, err)

	items, err = svc.SubscriptionFeed(ctx, userID, 20)
	require.NoError(t, err)
	require.Len(t, items, 1,
		"только «Новая» (2024, после подписки); «Старая» (2020, до) и «Чужая» (чужой автор) исключены")
	require.Equal(t, wNew, items[0].WorkID)
	require.Equal(t, "Новая", items[0].Title)
	require.Contains(t, items[0].Authors[0], "Подписан")
	require.NotNil(t, items[0].AddedAt)

	// Схлопывание по работе: второе издание новейшей работы (тоже после
	// подписки) не должно задваивать ленту.
	_, err = pool.Exec(ctx, `
		INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id, date_added)
		VALUES ($1,$2,'L-NEW2','L-NEW2','fb2','Новая','новая',$3,'2024-02-01')`,
		collID, archID, wNew)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `
		INSERT INTO book_authors (book_id, author_id)
		SELECT id, $1 FROM books WHERE lib_id='L-NEW2'`, subAuthor)
	require.NoError(t, err)

	items, err = svc.SubscriptionFeed(ctx, userID, 20)
	require.NoError(t, err)
	require.Len(t, items, 1, "второе издание той же работы не задваивает ленту")

	// Подписка на СЕРИЮ (датирована 2025-01-01). Книги серии написаны
	// otherAuthor (на него НЕ подписаны) — попасть могут только через серию.
	var serID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO series (title, normalized_title) VALUES ('Моя Серия','моя серия') RETURNING id`).Scan(&serID))
	mkSeriesBook := func(lib, title, dateAdded string) int64 {
		var wid, bid int64
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO works (title, normalized_title) VALUES ($1, lower($1)) RETURNING id`, title).Scan(&wid))
		require.NoError(t, pool.QueryRow(ctx, `
			INSERT INTO books (collection_id, archive_id, lib_id, file_name, ext, title, normalized_title, work_id, series_id, date_added)
			VALUES ($1,$2,$3,$3,'fb2',$4, lower($4), $5, $6, $7) RETURNING id`,
			collID, archID, lib, title, wid, serID, dateAdded).Scan(&bid))
		_, e := pool.Exec(ctx, `INSERT INTO book_authors (book_id, author_id) VALUES ($1,$2)`, bid, otherAuthor)
		require.NoError(t, e)
		return bid
	}
	bSerNew := mkSeriesBook("L-SER-NEW", "Томик-новый", "2025-06-01") // после подписки
	mkSeriesBook("L-SER-OLD", "Томик-старый", "2024-01-01")           // ДО подписки — не новинка

	_, err = pool.Exec(ctx,
		`INSERT INTO favorite_series (user_id, series_id, added_at) VALUES ($1,$2,'2025-01-01')`,
		userID, serID)
	require.NoError(t, err)

	items, err = svc.SubscriptionFeed(ctx, userID, 20)
	require.NoError(t, err)
	require.Len(t, items, 2,
		"«Новая» (автор) + «Томик-новый» (серия); старый том серии (до подписки) исключён")
	ids := make(map[int64]bool, len(items))
	for _, it := range items {
		ids[it.ID] = true
	}
	require.True(t, ids[bSerNew], "новинка подписанной серии попадает в ленту")

	// Скрытие работы из ленты («не интересно») — она исчезает из выдачи и
	// (персистентно) не возвращается.
	require.NoError(t, svc.DismissFeedItem(ctx, userID, wNew))
	require.NoError(t, svc.DismissFeedItem(ctx, userID, wNew)) // идемпотентно
	items, err = svc.SubscriptionFeed(ctx, userID, 20)
	require.NoError(t, err)
	require.Len(t, items, 1, "скрытая работа автора ушла из ленты, осталась только серия")
	require.Equal(t, bSerNew, items[0].ID, "осталась новинка серии")
}

// TestService_FavoriteGenres — избранные жанры: add (идемпотентно) →
// ListFavoriteGenreIDs содержит id → remove → больше нет.
func TestService_FavoriteGenres(t *testing.T) {
	if testing.Short() {
		t.Skip("integration: requires docker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	pool := startPostgres(t, ctx)

	var userID, gID int64
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (email, display_name, password_hash, role) VALUES ('g@e.com','G','x','user') RETURNING id`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO genres (fb2_code, name_ru) VALUES ('sf', 'Фантастика') RETURNING id`).Scan(&gID))

	svc := history.New(pool)

	// Изначально пусто.
	ids, err := svc.ListFavoriteGenreIDs(ctx, userID)
	require.NoError(t, err)
	require.Empty(t, ids)

	// Add (идемпотентно).
	require.NoError(t, svc.AddFavoriteGenre(ctx, userID, gID))
	require.NoError(t, svc.AddFavoriteGenre(ctx, userID, gID))
	ids, err = svc.ListFavoriteGenreIDs(ctx, userID)
	require.NoError(t, err)
	require.Len(t, ids, 1)
	_, ok := ids[gID]
	require.True(t, ok)

	// Remove (идемпотентно).
	require.NoError(t, svc.RemoveFavoriteGenre(ctx, userID, gID))
	require.NoError(t, svc.RemoveFavoriteGenre(ctx, userID, gID))
	ids, err = svc.ListFavoriteGenreIDs(ctx, userID)
	require.NoError(t, err)
	require.Empty(t, ids)
}

// ── helpers (повтор из других пакетов) ─────────────────────────

func startPostgres(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()
	pgC, err := postgres.Run(ctx,
		"postgres:17-alpine",
		postgres.WithDatabase("skriptes_test"),
		postgres.WithUsername("skriptes"),
		postgres.WithPassword("skriptes"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = pgC.Terminate(context.Background()) })
	dsn, err := pgC.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)
	require.NoError(t, db.Migrate(dsn))
	pool, err := db.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	return pool
}

func startMeilisearch(t *testing.T, ctx context.Context) meili.ServiceManager {
	t.Helper()
	const masterKey = "test-master-key-1234567890"
	mC, err := tcmeili.Run(ctx, "getmeili/meilisearch:v1.13", tcmeili.WithMasterKey(masterKey))
	require.NoError(t, err)
	t.Cleanup(func() { _ = mC.Terminate(context.Background()) })
	addr, err := mC.Address(ctx)
	require.NoError(t, err)
	return meili.New(addr, meili.WithAPIKey(masterKey))
}
