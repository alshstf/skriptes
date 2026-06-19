// Package collections — личные полки пользователя.
//
// Полка (collection) — произвольный именованный список книг, который
// пользователь собирает вручную. В отличие от серий/жанров (приходят из
// метаданных) полка — чисто пользовательский артефакт.
//
// Все методы требуют userID и проверяют владение полкой: чужую полку нельзя
// ни прочитать, ни изменить (возвращается ErrNotFound, чтобы не светить факт
// существования id). Модель данных — миграция 0021_collections.
package collections

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound — полка не существует ИЛИ принадлежит другому пользователю
// (намеренно не различаем — не светим чужие id).
var ErrNotFound = errors.New("collection not found")

// ErrEmptyName — имя полки после trim пустое.
var ErrEmptyName = errors.New("collection name is empty")

// ErrSystemCollection — служебную полку («Избранное») нельзя переименовать/удалить.
var ErrSystemCollection = errors.New("system collection cannot be modified")

// ErrReservedName — имя зарезервировано за служебной полкой (нельзя создать дубль).
var ErrReservedName = errors.New("collection name is reserved")

// maxNameLen — потолок длины имени полки (защита от мусорного ввода;
// домашняя библиотека, длинные имена ни к чему).
const maxNameLen = 200

// kind полки + имя служебной «Избранное» (миграция 0023). favorites-полка —
// служебная: ★ книги = членство в ней; одна на юзера (partial unique).
const (
	kindUser      = "user"
	kindFavorites = "favorites"
	favoritesName = "Избранное"
)

// Service — операции с полками. Потокобезопасен (pgxpool сам управляет
// конкурентным доступом).
type Service struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool}
}

// Collection — строка в списке полок пользователя.
type Collection struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// Kind — "user" (обычная полка) | "favorites" (служебная «Избранное»: ★ книги).
	Kind      string    `json:"kind"`
	BookCount int       `json:"book_count"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CollectionBook — компактная строка книги внутри полки (как FavoriteItem).
type CollectionBook struct {
	ID      int64     `json:"id"`
	Title   string    `json:"title"`
	Authors []string  `json:"authors"`
	Series  string    `json:"series,omitempty"`
	Lang    string    `json:"lang,omitempty"`
	WorkID  *int64    `json:"work_id,omitempty"`
	AddedAt time.Time `json:"added_at"`
}

// normalizeName — trim + потолок длины. Возвращает ErrEmptyName, если после
// trim ничего не осталось.
func normalizeName(name string) (string, error) {
	n := strings.TrimSpace(name)
	if n == "" {
		return "", ErrEmptyName
	}
	if len(n) > maxNameLen {
		n = strings.TrimSpace(n[:maxNameLen])
	}
	return n, nil
}

// CreateCollection — создать полку. Возвращает её id.
func (s *Service) CreateCollection(ctx context.Context, userID int64, name string) (Collection, error) {
	n, err := normalizeName(name)
	if err != nil {
		return Collection{}, err
	}
	// «Избранное» зарезервировано за служебной полкой — дубль руками нельзя.
	if strings.EqualFold(n, favoritesName) {
		return Collection{}, ErrReservedName
	}
	c := Collection{Name: n, Kind: kindUser}
	err = s.pool.QueryRow(ctx, `
		INSERT INTO user_collections (user_id, name, kind) VALUES ($1, $2, 'user')
		RETURNING id, created_at, updated_at
	`, userID, n).Scan(&c.ID, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return Collection{}, fmt.Errorf("insert collection: %w", err)
	}
	return c, nil
}

// RenameCollection — переименовать СВОЮ полку. ErrNotFound, если полки нет или
// она чужая (WHERE user_id гейтит владение в самом UPDATE).
func (s *Service) RenameCollection(ctx context.Context, userID, id int64, name string) error {
	n, err := normalizeName(name)
	if err != nil {
		return err
	}
	if strings.EqualFold(n, favoritesName) {
		return ErrReservedName
	}
	// Служебную «Избранное» переименовывать нельзя.
	var kind string
	err = s.pool.QueryRow(ctx, `SELECT kind FROM user_collections WHERE id = $1 AND user_id = $2`, id, userID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lookup collection: %w", err)
	}
	if kind != kindUser {
		return ErrSystemCollection
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE user_collections SET name = $1, updated_at = now()
		WHERE id = $2 AND user_id = $3
	`, n, id, userID); err != nil {
		return fmt.Errorf("rename collection: %w", err)
	}
	return nil
}

// DeleteCollection — удалить СВОЮ полку (membership уносит CASCADE).
// Идемпотентность здесь НЕ нужна: повторный DELETE по уже удалённой/чужой
// полке отдаёт ErrNotFound (фронт показывает «полка не найдена»).
func (s *Service) DeleteCollection(ctx context.Context, userID, id int64) error {
	// Служебную «Избранное» удалять нельзя.
	var kind string
	err := s.pool.QueryRow(ctx, `SELECT kind FROM user_collections WHERE id = $1 AND user_id = $2`, id, userID).Scan(&kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lookup collection: %w", err)
	}
	if kind != kindUser {
		return ErrSystemCollection
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM user_collections WHERE id = $1 AND user_id = $2`, id, userID); err != nil {
		return fmt.Errorf("delete collection: %w", err)
	}
	return nil
}

// ListCollections — полки пользователя с числом книг. book_count считает
// только живые книги (deleted=false) — полка может содержать книгу, которую
// потом убрали из коллекции (DEL=1); считать её не нужно. Сортировка — свежие
// сверху (updated_at desc).
func (s *Service) ListCollections(ctx context.Context, userID int64) ([]Collection, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.name, c.kind, c.created_at, c.updated_at,
		       (SELECT count(*) FROM user_collection_books cb
		        JOIN books b ON b.id = cb.book_id AND b.deleted = false
		        WHERE cb.collection_id = c.id)::int AS book_count
		FROM user_collections c
		WHERE c.user_id = $1
		ORDER BY (c.kind = 'favorites') DESC, c.updated_at DESC, c.id DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list collections: %w", err)
	}
	defer rows.Close()
	out := make([]Collection, 0)
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.ID, &c.Name, &c.Kind, &c.CreatedAt, &c.UpdatedAt, &c.BookCount); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CollectionShelf — лёгкая строка «полка, содержащая книгу» (id+name+kind) для
// индикации членства на карточке книги (без book_count и дат). Kind нужен фронту,
// чтобы исключить служебную «Избранное» из чипов (её передаёт ★).
type CollectionShelf struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// CollectionsForBook — полки текущего юзера, содержащие данную книгу (издание).
// Для индикации членства на карточке («На полках: …»). Членство per-edition
// (по book_id) — согласовано с AddToShelfDialog, который добавляет по book.id
// представительного издания. Сортировка по имени.
func (s *Service) CollectionsForBook(ctx context.Context, userID, bookID int64) ([]CollectionShelf, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT c.id, c.name, c.kind
		FROM user_collections c
		JOIN user_collection_books cb ON cb.collection_id = c.id
		WHERE c.user_id = $1 AND cb.book_id = $2
		ORDER BY c.name, c.id
	`, userID, bookID)
	if err != nil {
		return nil, fmt.Errorf("collections for book: %w", err)
	}
	defer rows.Close()
	out := make([]CollectionShelf, 0)
	for rows.Next() {
		var sh CollectionShelf
		if err := rows.Scan(&sh.ID, &sh.Name, &sh.Kind); err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

// ownsCollection — true если полка id принадлежит userID. Используется как
// гейт перед операциями с книгами полки.
func (s *Service) ownsCollection(ctx context.Context, userID, id int64) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM user_collections WHERE id = $1 AND user_id = $2)
	`, id, userID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("check collection owner: %w", err)
	}
	return ok, nil
}

// AddBookToCollection — добавить книгу в СВОЮ полку. Идемпотентна (повторный
// add — no-op). Чужая/несуществующая полка → ErrNotFound. updated_at полки
// бампается, чтобы недавно пополненная всплывала вверх списка.
func (s *Service) AddBookToCollection(ctx context.Context, userID, collID, bookID int64) error {
	return s.mutateMembership(ctx, userID, collID, `
		INSERT INTO user_collection_books (collection_id, book_id) VALUES ($1, $2)
		ON CONFLICT (collection_id, book_id) DO NOTHING
	`, collID, bookID)
}

// RemoveBookFromCollection — убрать книгу из СВОЕЙ полки. Идемпотентна.
func (s *Service) RemoveBookFromCollection(ctx context.Context, userID, collID, bookID int64) error {
	return s.mutateMembership(ctx, userID, collID, `
		DELETE FROM user_collection_books WHERE collection_id = $1 AND book_id = $2
	`, collID, bookID)
}

// mutateMembership — общий путь add/remove: в ОДНОЙ транзакции
//  1. блокируем строку полки FOR UPDATE с гейтом владения (id+user_id) —
//     если её нет/чужая → ErrNotFound; блокировка закрывает гонку с
//     конкурентным DeleteCollection (иначе INSERT упал бы FK-ошибкой = 500);
//  2. выполняем сам membership-запрос (INSERT/DELETE);
//  3. бампаем updated_at полки.
//
// Всё атомарно — частичных состояний (книга добавлена, но updated_at не
// обновлён) не остаётся, и ошибка updated_at-бампа больше не глотается.
func (s *Service) mutateMembership(ctx context.Context, userID, collID int64, membershipSQL string, args ...any) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var owned int64
	err = tx.QueryRow(ctx, `
		SELECT id FROM user_collections WHERE id = $1 AND user_id = $2 FOR UPDATE
	`, collID, userID).Scan(&owned)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("lock collection: %w", err)
	}
	if _, err := tx.Exec(ctx, membershipSQL, args...); err != nil {
		return fmt.Errorf("mutate membership: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE user_collections SET updated_at = now() WHERE id = $1`, collID); err != nil {
		return fmt.Errorf("bump updated_at: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// ListCollectionBooks — книги в СВОЕЙ полке (живые), свежедобавленные сверху.
// Чужая/несуществующая полка → ErrNotFound.
//
// Скрываем deleted-книги тем же приёмом что и ListFavorites: книгу могли убрать
// из коллекции (DEL=1) — она не должна болтаться зомби.
func (s *Service) ListCollectionBooks(ctx context.Context, userID, collID int64) ([]CollectionBook, error) {
	owns, err := s.ownsCollection(ctx, userID, collID)
	if err != nil {
		return nil, err
	}
	if !owns {
		return nil, ErrNotFound
	}
	rows, err := s.pool.Query(ctx, `
		SELECT b.id, b.title, b.lang, b.work_id, cb.added_at,
		       COALESCE(
		           array_agg(DISTINCT TRIM(CONCAT_WS(' ', a.last_name, a.first_name, a.middle_name))) FILTER (WHERE a.id IS NOT NULL),
		           ARRAY[]::text[]
		       ),
		       ser.title
		FROM user_collection_books cb
		JOIN books b ON b.id = cb.book_id AND b.deleted = false
		LEFT JOIN book_authors ba ON ba.book_id = b.id
		LEFT JOIN authors a ON a.id = ba.author_id
		LEFT JOIN series ser ON ser.id = b.series_id
		WHERE cb.collection_id = $1
		GROUP BY b.id, b.title, b.lang, b.work_id, cb.added_at, ser.title
		ORDER BY cb.added_at DESC, b.id DESC
	`, collID)
	if err != nil {
		return nil, fmt.Errorf("list collection books: %w", err)
	}
	defer rows.Close()
	out := make([]CollectionBook, 0)
	for rows.Next() {
		var (
			it     CollectionBook
			lang   *string
			series *string
		)
		if err := rows.Scan(&it.ID, &it.Title, &lang, &it.WorkID, &it.AddedAt, &it.Authors, &series); err != nil {
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
	return out, rows.Err()
}
