// Package history — учёт активности пользователя (views, reads, favorites).
//
// Используется как:
//   - Источник данных для персонализированного re-ranking (PR4): bonus к
//     score книги если автор/серия уже встречались пользователю.
//   - Источник данных для UI: список избранного, "недавно открытые",
//     статистика чтения.
//
// Запись событий идёт fire-and-forget из HTTP-handler'ов (см. internal/api):
// ошибка записи не должна ломать основной запрос (загрузку карточки или
// скачивание книги), поэтому ошибки только логируются.
package history

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound — для get-операций.
var ErrNotFound = errors.New("not found")

// Service — единая точка для всех операций с историей.
// Все методы потокобезопасны (pgxpool сам управляет конкурентным доступом).
type Service struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// RecordView — фиксирует факт открытия карточки книги пользователем.
//
// Вставка идёт без upsert: views — лог-таблица (timeseries), несколько
// записей за день для одной (user, book) пары — это нормально и даёт
// корректную статистику "сколько раз открывали".
func (s *Service) RecordView(ctx context.Context, userID, bookID int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO views (user_id, book_id) VALUES ($1, $2)
	`, userID, bookID)
	if err != nil {
		return fmt.Errorf("insert view: %w", err)
	}
	return nil
}

// RecordRead — фиксирует факт скачивания/открытия книги.
//
// Слово "read" здесь в смысле "accessed" (download или fetch ридером),
// НЕ "completed". Сигнал «прочитано» — это reads.completed_at IS NOT NULL,
// устанавливается явно через MarkRead. RecordRead используется для
// re-ranking (книга, к которой обращались, — слабый сигнал интереса)
// и для тёплой памяти reads-row, на которую later может прийти
// SavePosition или MarkRead.
//
// reads имеет PRIMARY KEY (user_id, book_id) — повторные вызовы
// обновляют updated_at; last_pos и completed_at не трогаем.
func (s *Service) RecordRead(ctx context.Context, userID, bookID int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO reads (user_id, book_id, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (user_id, book_id)
		DO UPDATE SET updated_at = now()
	`, userID, bookID)
	if err != nil {
		return fmt.Errorf("upsert read: %w", err)
	}
	return nil
}

// MarkRead — пользователь явно отметил книгу как прочитанную.
// Идемпотентна: повторный вызов перетирает completed_at тем же now() —
// это нормально (точное время «первой отметки» нам не критично).
//
// Этот метод — основной сигнал «прочитано» для статистики автора/серии
// и для UI-чекмарка на карточке книги. Дёргается из POST /api/books/{id}/read
// и из ридера при дочитывании.
func (s *Service) MarkRead(ctx context.Context, userID, bookID int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO reads (user_id, book_id, completed_at, updated_at)
		VALUES ($1, $2, now(), now())
		ON CONFLICT (user_id, book_id)
		DO UPDATE SET completed_at = now(), updated_at = now()
	`, userID, bookID)
	if err != nil {
		return fmt.Errorf("mark read: %w", err)
	}
	return nil
}

// UnmarkRead — снять отметку «прочитано». Сама запись в reads остаётся
// (важно для re-ranking сигналов: пользователь интересовался книгой,
// даже если потом передумал её считать прочитанной).
func (s *Service) UnmarkRead(ctx context.Context, userID, bookID int64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE reads SET completed_at = NULL, updated_at = now()
		WHERE user_id = $1 AND book_id = $2
	`, userID, bookID)
	if err != nil {
		return fmt.Errorf("unmark read: %w", err)
	}
	return nil
}

// IsRead — true если есть запись в reads с completed_at IS NOT NULL.
// Совместимость с существующими callers; новый код должен использовать
// ReadStatus который возвращает ещё и timestamp + fraction одним запросом.
func (s *Service) IsRead(ctx context.Context, userID, bookID int64) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM reads
		              WHERE user_id = $1 AND book_id = $2
		                AND completed_at IS NOT NULL)
	`, userID, bookID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("query is_read: %w", err)
	}
	return exists, nil
}

// ReadStatus — расширенный аналог IsRead: возвращает is_read flag,
// completed_at (когда отметили прочитанной — для отображения даты в
// карточке книги) и fraction (для «Продолжить N%» на кнопке открытия
// ридера). Все nil/0 если строки в reads нет.
//
// Один запрос вместо трёх — для handleGetBook где мы обогащаем ответ
// user-specific полями.
func (s *Service) ReadStatus(ctx context.Context, userID, bookID int64) (isRead bool, completedAt *time.Time, fraction *float64, err error) {
	var ca *time.Time
	var fr *float64
	err = s.pool.QueryRow(ctx, `
		SELECT completed_at, fraction FROM reads
		WHERE user_id = $1 AND book_id = $2
	`, userID, bookID).Scan(&ca, &fr)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil, nil, nil
		}
		return false, nil, nil, fmt.Errorf("query read status: %w", err)
	}
	return ca != nil, ca, fr, nil
}

// IsWorkFavorite — книга (логическая работа) в избранном, если избрано ЛЮБОЕ
// её издание. Для singleton-работы совпадает с IsFavorite по изданию.
func (s *Service) IsWorkFavorite(ctx context.Context, userID, workID int64) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM favorites f
			JOIN books b ON b.id = f.book_id
			WHERE f.user_id = $1 AND b.work_id = $2
		)
	`, userID, workID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("query work favorite: %w", err)
	}
	return ok, nil
}

// FavoriteWorkSet — подмножество переданных work_id, у которых избрано ЛЮБОЕ
// издание (для пометки is_favorite в выдаче по работам, например в Cmd+K).
func (s *Service) FavoriteWorkSet(ctx context.Context, userID int64, workIDs []int64) (map[int64]struct{}, error) {
	out := map[int64]struct{}{}
	if len(workIDs) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT DISTINCT b.work_id
		FROM favorites f
		JOIN books b ON b.id = f.book_id
		WHERE f.user_id = $1 AND b.work_id = ANY($2)
	`, userID, workIDs)
	if err != nil {
		return out, fmt.Errorf("query favorite works: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return out, err
		}
		out[id] = struct{}{}
	}
	return out, rows.Err()
}

// EditionRead — состояние чтения одного издания пользователем.
type EditionRead struct {
	Fraction  *float64
	Completed bool
}

// WorkEditionReads — состояние чтения по КАЖДОМУ изданию работы для
// пользователя: map book_id → {fraction, completed}. Нужен для отображения
// прогресса на каждой строке секции «Издания» (позиция/прогресс per-edition).
func (s *Service) WorkEditionReads(ctx context.Context, userID, workID int64) (map[int64]EditionRead, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT r.book_id, r.fraction, (r.completed_at IS NOT NULL)
		FROM reads r
		JOIN books b ON b.id = r.book_id
		WHERE r.user_id = $1 AND b.work_id = $2
	`, userID, workID)
	if err != nil {
		return nil, fmt.Errorf("query work edition reads: %w", err)
	}
	defer rows.Close()
	out := map[int64]EditionRead{}
	for rows.Next() {
		var id int64
		var er EditionRead
		if err := rows.Scan(&id, &er.Fraction, &er.Completed); err != nil {
			return nil, err
		}
		out[id] = er
	}
	return out, rows.Err()
}

// WorkReadStatus — «прочитана» на уровне работы: прочитано ЛЮБОЕ издание.
// completedAt — самая поздняя дата прочтения среди изданий (NULL → не прочитана).
// Прогресс чтения (fraction) остаётся per-edition — он привязан к конкретному
// файлу (CFI), агрегировать его нельзя.
func (s *Service) WorkReadStatus(ctx context.Context, userID, workID int64) (isRead bool, completedAt *time.Time, err error) {
	var ca *time.Time
	err = s.pool.QueryRow(ctx, `
		SELECT max(r.completed_at)
		FROM reads r
		JOIN books b ON b.id = r.book_id
		WHERE r.user_id = $1 AND b.work_id = $2 AND r.completed_at IS NOT NULL
	`, userID, workID).Scan(&ca)
	if err != nil {
		return false, nil, fmt.Errorf("query work read status: %w", err)
	}
	return ca != nil, ca, nil
}

// SavePosition — сохраняет позицию чтения (epub-cfi) + fraction
// прогресса (0.0–1.0). Upsert: если строки в reads ещё нет, создаём её.
//
// Пустая строка pos допустима — означает «сбросить позицию». fraction=nil
// означает «не обновлять прогресс» (не перетирать предыдущее значение).
// fraction=0 → 0% (явный сброс в начало).
func (s *Service) SavePosition(ctx context.Context, userID, bookID int64, pos string, fraction *float64) error {
	var p *string
	if pos != "" {
		p = &pos
	}
	// Зажимаем fraction в [0, 1] — защита от мусорного input'а.
	if fraction != nil {
		if *fraction < 0 {
			f := 0.0
			fraction = &f
		} else if *fraction > 1 {
			f := 1.0
			fraction = &f
		}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO reads (user_id, book_id, last_pos, fraction, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (user_id, book_id)
		DO UPDATE SET
			last_pos = EXCLUDED.last_pos,
			fraction = COALESCE(EXCLUDED.fraction, reads.fraction),
			updated_at = now()
	`, userID, bookID, p, fraction)
	if err != nil {
		return fmt.Errorf("save position: %w", err)
	}
	return nil
}

// GetPosition — последняя сохранённая позиция (epub-cfi). Если строки
// в reads нет ИЛИ last_pos NULL — возвращает "" без ошибки. Caller
// использует это чтобы решить, открывать книгу с начала или с позиции.
func (s *Service) GetPosition(ctx context.Context, userID, bookID int64) (string, error) {
	var pos *string
	err := s.pool.QueryRow(ctx, `
		SELECT last_pos FROM reads WHERE user_id = $1 AND book_id = $2
	`, userID, bookID).Scan(&pos)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("query position: %w", err)
	}
	if pos == nil {
		return "", nil
	}
	return *pos, nil
}

// AddFavorite — добавить книгу в избранное.
// Идемпотентна: повторный вызов не падает и не меняет added_at.
func (s *Service) AddFavorite(ctx context.Context, userID, bookID int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO favorites (user_id, book_id) VALUES ($1, $2)
		ON CONFLICT (user_id, book_id) DO NOTHING
	`, userID, bookID)
	if err != nil {
		return fmt.Errorf("insert favorite: %w", err)
	}
	return nil
}

// RemoveFavorite — убрать из избранного. Идемпотентна.
func (s *Service) RemoveFavorite(ctx context.Context, userID, bookID int64) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM favorites WHERE user_id = $1 AND book_id = $2
	`, userID, bookID)
	if err != nil {
		return fmt.Errorf("delete favorite: %w", err)
	}
	return nil
}

// IsFavorite — true если пользователь добавил книгу в избранное.
func (s *Service) IsFavorite(ctx context.Context, userID, bookID int64) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM favorites WHERE user_id = $1 AND book_id = $2)
	`, userID, bookID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("query favorite: %w", err)
	}
	return exists, nil
}

// ListFavorites — последние limit книг в избранном пользователя.
// Возвращает ID + минимальные поля для рендера списка; полную карточку
// фронт получит отдельным запросом если нужно.
//
// Скрываем deleted-книги: если книгу убрали из коллекции (DEL=1), она
// не должна болтаться в "избранном" как зомби.
func (s *Service) ListFavorites(ctx context.Context, userID int64, limit, offset int) ([]FavoriteItem, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.pool.Query(ctx, `
		SELECT b.id, b.title, b.lang, b.lib_id, f.added_at,
		       COALESCE(
		           array_agg(DISTINCT TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name))) FILTER (WHERE a.id IS NOT NULL),
		           ARRAY[]::text[]
		       ),
		       ser.title
		FROM favorites f
		JOIN books b ON b.id = f.book_id AND b.deleted = false
		LEFT JOIN book_authors ba ON ba.book_id = b.id
		LEFT JOIN authors a ON a.id = ba.author_id
		LEFT JOIN series ser ON ser.id = b.series_id
		WHERE f.user_id = $1
		GROUP BY b.id, b.title, b.lang, b.lib_id, f.added_at, ser.title
		ORDER BY f.added_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query favorites: %w", err)
	}
	defer rows.Close()
	out := make([]FavoriteItem, 0)
	for rows.Next() {
		var (
			it     FavoriteItem
			lang   *string
			series *string
		)
		if err := rows.Scan(&it.ID, &it.Title, &lang, &it.LibID, &it.AddedAt, &it.Authors, &series); err != nil {
			return nil, err
		}
		if lang != nil {
			it.Lang = *lang
		}
		if series != nil {
			it.Series = *series
		}
		out = append(out, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ── Favorites для авторов ───────────────────────────────────────

// AddFavoriteAuthor / RemoveFavoriteAuthor / IsFavoriteAuthor — симметричные
// AddFavorite/Remove/Is для книг, но работают на таблице favorite_authors.
// Семантика: "пользователь следит за автором" (для будущей ленты новинок
// и для bonus'а в персональном re-ranking).

func (s *Service) AddFavoriteAuthor(ctx context.Context, userID, authorID int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO favorite_authors (user_id, author_id) VALUES ($1, $2)
		ON CONFLICT (user_id, author_id) DO NOTHING
	`, userID, authorID)
	if err != nil {
		return fmt.Errorf("insert favorite author: %w", err)
	}
	return nil
}

func (s *Service) RemoveFavoriteAuthor(ctx context.Context, userID, authorID int64) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM favorite_authors WHERE user_id = $1 AND author_id = $2
	`, userID, authorID)
	if err != nil {
		return fmt.Errorf("delete favorite author: %w", err)
	}
	return nil
}

func (s *Service) IsFavoriteAuthor(ctx context.Context, userID, authorID int64) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM favorite_authors WHERE user_id = $1 AND author_id = $2)
	`, userID, authorID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("query favorite author: %w", err)
	}
	return exists, nil
}

// ListFavoriteAuthors — авторы, на которых подписан пользователь,
// с числом их книг (живых) и временем подписки. Используется для UI
// "Мои подписки" и как сигнал для re-ranking.
func (s *Service) ListFavoriteAuthors(ctx context.Context, userID int64, limit, offset int) ([]FavoriteAuthorItem, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.pool.Query(ctx, `
		SELECT a.id, a.last_name, a.first_name, a.middle_name, fa.added_at,
		       (SELECT count(*) FROM book_authors ba
		        JOIN books b ON b.id = ba.book_id
		        WHERE ba.author_id = a.id AND b.deleted = false) AS book_count
		FROM favorite_authors fa
		JOIN authors a ON a.id = fa.author_id
		WHERE fa.user_id = $1
		ORDER BY fa.added_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query favorite authors: %w", err)
	}
	defer rows.Close()
	out := make([]FavoriteAuthorItem, 0)
	for rows.Next() {
		var (
			it                  FavoriteAuthorItem
			last, first, middle string
		)
		if err := rows.Scan(&it.ID, &last, &first, &middle, &it.AddedAt, &it.BookCount); err != nil {
			return nil, err
		}
		it.FullName = composeName(last, first, middle)
		out = append(out, it)
	}
	return out, rows.Err()
}

// ── Favorites для серий ─────────────────────────────────────────

func (s *Service) AddFavoriteSeries(ctx context.Context, userID, seriesID int64) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO favorite_series (user_id, series_id) VALUES ($1, $2)
		ON CONFLICT (user_id, series_id) DO NOTHING
	`, userID, seriesID)
	if err != nil {
		return fmt.Errorf("insert favorite series: %w", err)
	}
	return nil
}

func (s *Service) RemoveFavoriteSeries(ctx context.Context, userID, seriesID int64) error {
	_, err := s.pool.Exec(ctx, `
		DELETE FROM favorite_series WHERE user_id = $1 AND series_id = $2
	`, userID, seriesID)
	if err != nil {
		return fmt.Errorf("delete favorite series: %w", err)
	}
	return nil
}

func (s *Service) IsFavoriteSeries(ctx context.Context, userID, seriesID int64) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM favorite_series WHERE user_id = $1 AND series_id = $2)
	`, userID, seriesID).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("query favorite series: %w", err)
	}
	return exists, nil
}

// ListFavoriteSeries — серии в подписках, с автором (если у серии один)
// и числом книг в серии.
func (s *Service) ListFavoriteSeries(ctx context.Context, userID int64, limit, offset int) ([]FavoriteSeriesItem, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.pool.Query(ctx, `
		SELECT s.id, s.title, fs.added_at,
		       COALESCE(NULLIF(TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name)), ''), ''),
		       (SELECT count(*) FROM books b WHERE b.series_id = s.id AND b.deleted = false) AS book_count
		FROM favorite_series fs
		JOIN series s ON s.id = fs.series_id
		LEFT JOIN authors a ON a.id = s.author_id
		WHERE fs.user_id = $1
		ORDER BY fs.added_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query favorite series: %w", err)
	}
	defer rows.Close()
	out := make([]FavoriteSeriesItem, 0)
	for rows.Next() {
		var it FavoriteSeriesItem
		if err := rows.Scan(&it.ID, &it.Title, &it.AddedAt, &it.AuthorName, &it.BookCount); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// composeName — то же что fullName() в catalog, но вынесено сюда чтобы
// не тащить лишний пакет. Скучный код, но один источник правды.
func composeName(last, first, middle string) string {
	parts := make([]string, 0, 3)
	if last != "" {
		parts = append(parts, last)
	}
	if first != "" {
		parts = append(parts, first)
	}
	if middle != "" {
		parts = append(parts, middle)
	}
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}

// RecentViews — последние просмотры пользователя (для "недавно открытые").
// Возвращает по 1 записи на (book_id) — последний viewed_at.
func (s *Service) RecentViews(ctx context.Context, userID int64, limit int) ([]ViewedItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		SELECT b.id, b.title, MAX(v.viewed_at) AS last_viewed,
		       COALESCE(
		           array_agg(DISTINCT TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name))) FILTER (WHERE a.id IS NOT NULL),
		           ARRAY[]::text[]
		       )
		FROM views v
		JOIN books b ON b.id = v.book_id AND b.deleted = false
		LEFT JOIN book_authors ba ON ba.book_id = b.id
		LEFT JOIN authors a ON a.id = ba.author_id
		WHERE v.user_id = $1
		GROUP BY b.id, b.title
		ORDER BY last_viewed DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent views: %w", err)
	}
	defer rows.Close()
	out := make([]ViewedItem, 0)
	for rows.Next() {
		var it ViewedItem
		if err := rows.Scan(&it.ID, &it.Title, &it.LastViewedAt, &it.Authors); err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// ContinueReading — книги «в процессе»: записи в reads с прогрессом
// (fraction > 0), но ещё не отмеченные прочитанными (completed_at IS NULL).
// Сортировка по updated_at DESC — самые свежие сверху («продолжить с того,
// на чём остановился»). По образцу RecentViews: JOIN авторов + серии, скрываем
// deleted-книги.
//
// ID = reads.book_id (издание): прогресс/позиция привязаны к конкретному
// fb2-файлу. work_id отдаём для ссылки карточки (/works/{work_id}). cover_path
// COALESCE'ится с обложкой любого живого издания работы — как в books.hydrateCovers,
// чтобы у издания без своей обложки она бралась из соседнего.
func (s *Service) ContinueReading(ctx context.Context, userID int64, limit int) ([]ContinueItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `
		SELECT b.id, b.work_id, b.title, b.lib_id, ser.title,
		       COALESCE(r.fraction, 0), r.updated_at,
		       COALESCE(b.cover_path, (
		           SELECT bb.cover_path FROM books bb
		           WHERE bb.work_id = b.work_id AND bb.deleted = false
		             AND bb.cover_path IS NOT NULL AND bb.cover_path <> ''
		           ORDER BY bb.id LIMIT 1
		       ), ''),
		       COALESCE(
		           array_agg(DISTINCT TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name))) FILTER (WHERE a.id IS NOT NULL),
		           ARRAY[]::text[]
		       )
		FROM reads r
		JOIN books b ON b.id = r.book_id AND b.deleted = false
		LEFT JOIN series ser ON ser.id = b.series_id
		LEFT JOIN book_authors ba ON ba.book_id = b.id
		LEFT JOIN authors a ON a.id = ba.author_id
		WHERE r.user_id = $1
		  AND r.completed_at IS NULL
		  AND COALESCE(r.fraction, 0) > 0
		GROUP BY b.id, b.work_id, b.title, b.lib_id, ser.title, r.fraction, r.updated_at, b.cover_path
		ORDER BY r.updated_at DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("query continue reading: %w", err)
	}
	defer rows.Close()
	out := make([]ContinueItem, 0)
	for rows.Next() {
		var (
			it     ContinueItem
			workID *int64
			series *string
		)
		if err := rows.Scan(&it.ID, &workID, &it.Title, &it.LibID, &series,
			&it.Fraction, &it.UpdatedAt, &it.CoverPath, &it.Authors); err != nil {
			return nil, err
		}
		if workID != nil {
			it.WorkID = *workID
		}
		if series != nil {
			it.Series = *series
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// SubscriptionFeed — свежие книги авторов, на которых подписан пользователь
// (favorite_authors). «Свежесть» = books.date_added (когда книга появилась в
// библиотеке, см. граблю про date_added ≠ год написания — для «новинок»
// добавление в библиотеку и есть корректный сигнал).
//
// Схлопывание по работе: одна логическая книга в ленте один раз. Берём
// представительное издание (DISTINCT ON по COALESCE(work_id, -id), внутри
// группы — самое свежее date_added, тай-брейк по min id). Скрываем deleted.
func (s *Service) SubscriptionFeed(ctx context.Context, userID int64, limit int) ([]FeedItem, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	// Шаг 1 — представитель на работу: среди живых книг подписанных авторов
	// выбираем по одному изданию на work (самое свежее date_added).
	// Шаг 2 — обогащаем представителя авторами/серией/обложкой.
	rows, err := s.pool.Query(ctx, `
		WITH rep AS (
			SELECT DISTINCT ON (COALESCE(b.work_id, -b.id))
			       b.id, b.work_id, b.date_added
			FROM favorite_authors fa
			JOIN book_authors ba ON ba.author_id = fa.author_id
			JOIN books b ON b.id = ba.book_id AND b.deleted = false
			WHERE fa.user_id = $1
			ORDER BY COALESCE(b.work_id, -b.id),
			         b.date_added DESC NULLS LAST, b.id
		)
		SELECT b.id, b.work_id, b.title, b.lib_id, b.date_added, ser.title,
		       COALESCE(b.cover_path, (
		           SELECT bb.cover_path FROM books bb
		           WHERE bb.work_id = b.work_id AND bb.deleted = false
		             AND bb.cover_path IS NOT NULL AND bb.cover_path <> ''
		           ORDER BY bb.id LIMIT 1
		       ), ''),
		       COALESCE(
		           array_agg(DISTINCT TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name))) FILTER (WHERE a.id IS NOT NULL),
		           ARRAY[]::text[]
		       )
		FROM rep
		JOIN books b ON b.id = rep.id
		LEFT JOIN series ser ON ser.id = b.series_id
		LEFT JOIN book_authors ba ON ba.book_id = b.id
		LEFT JOIN authors a ON a.id = ba.author_id
		GROUP BY b.id, b.work_id, b.title, b.lib_id, b.date_added, ser.title, b.cover_path
		ORDER BY b.date_added DESC NULLS LAST, b.id DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("query subscription feed: %w", err)
	}
	defer rows.Close()
	out := make([]FeedItem, 0)
	for rows.Next() {
		var (
			it     FeedItem
			workID *int64
			added  pgtype.Date
			series *string
		)
		if err := rows.Scan(&it.ID, &workID, &it.Title, &it.LibID, &added, &series,
			&it.CoverPath, &it.Authors); err != nil {
			return nil, err
		}
		if workID != nil {
			it.WorkID = *workID
		}
		if added.Valid {
			t := added.Time
			it.AddedAt = &t
		}
		if series != nil {
			it.Series = *series
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// FavoritesCount — количество избранного у пользователя. Нужно для
// пагинации в UI; считается отдельным быстрым запросом, чтобы не
// тащить через GROUP BY в основной выборке.
func (s *Service) FavoritesCount(ctx context.Context, userID int64) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM favorites f
		JOIN books b ON b.id = f.book_id AND b.deleted = false
		WHERE f.user_id = $1
	`, userID).Scan(&n)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("count favorites: %w", err)
	}
	return n, nil
}
