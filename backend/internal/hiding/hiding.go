// Package hiding — скрытие авторов и работ на инстансе (админ, миграция 0064).
// Хранит явные скрытия (hidden_authors, hidden_works); флаги instance_hidden,
// по которым фильтруют списки, поиск и прямые ссылки, пересчитывает
// importer.SyncInstanceHidden — вызывающий (API) запускает его после изменения.
package hiding

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound — нет такого автора или работы.
var ErrNotFound = errors.New("hiding: not found")

// Store — явные скрытия.
type Store struct {
	pool *pgxpool.Pool
}

// New — хранилище поверх пула.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Kind — что скрыто.
type Kind string

const (
	KindAuthor Kind = "author"
	KindWork   Kind = "work"
	// KindBook, KindSeries — только для проверки видимости (Hidden): явно
	// скрываются авторы и работы.
	KindBook   Kind = "book"
	KindSeries Kind = "series"
)

// Item — строка списка «Скрытое» в админке.
type Item struct {
	Kind     Kind      `json:"kind"`
	ID       int64     `json:"id"`
	Name     string    `json:"name"`
	Note     string    `json:"note"`
	HiddenAt time.Time `json:"hidden_at"`
	// Books — сколько живых изданий скрыто из-за этой записи (автор: где он среди
	// авторов; работа: её издания).
	Books int `json:"books"`
}

// Hide — скрыть автора или работу (повтор — no-op, заметка обновляется).
func (s *Store) Hide(ctx context.Context, kind Kind, id, by int64, note string) error {
	table, col, target, err := tables(kind)
	if err != nil {
		return err
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+target+` WHERE id = $1)`, id).Scan(&exists); err != nil {
		return fmt.Errorf("hiding: %s %d: %w", kind, id, err)
	}
	if !exists {
		return ErrNotFound
	}
	var byArg any
	if by > 0 {
		byArg = by
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO `+table+` (`+col+`, hidden_by, note) VALUES ($1, $2, $3)
		ON CONFLICT (`+col+`) DO UPDATE SET note = EXCLUDED.note`, id, byArg, note)
	if err != nil {
		return fmt.Errorf("hiding: hide %s %d: %w", kind, id, err)
	}
	return nil
}

// Unhide — вернуть (не было скрыто — ErrNotFound).
func (s *Store) Unhide(ctx context.Context, kind Kind, id int64) error {
	table, col, _, err := tables(kind)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM `+table+` WHERE `+col+` = $1`, id)
	if err != nil {
		return fmt.Errorf("hiding: unhide %s %d: %w", kind, id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func tables(kind Kind) (table, col, target string, err error) {
	switch kind {
	case KindAuthor:
		return "hidden_authors", "author_id", "authors", nil
	case KindWork:
		return "hidden_works", "work_id", "works", nil
	}
	return "", "", "", fmt.Errorf("hiding: unknown kind %q", kind)
}

// List — всё явно скрытое, новые сверху.
func (s *Store) List(ctx context.Context) ([]Item, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT 'author', a.id,
		       btrim(concat_ws(' ', a.last_name, a.first_name, a.middle_name)),
		       h.note, h.hidden_at,
		       (SELECT count(*) FROM book_authors ba JOIN books b ON b.id = ba.book_id
		        WHERE ba.author_id = a.id AND b.deleted = false)
		FROM hidden_authors h JOIN authors a ON a.id = h.author_id
		UNION ALL
		SELECT 'work', w.id, w.title, h.note, h.hidden_at,
		       (SELECT count(*) FROM books b WHERE b.work_id = w.id AND b.deleted = false)
		FROM hidden_works h JOIN works w ON w.id = h.work_id
		ORDER BY 5 DESC`)
	if err != nil {
		return nil, fmt.Errorf("hiding: list: %w", err)
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Item, error) {
		var it Item
		return it, r.Scan(&it.Kind, &it.ID, &it.Name, &it.Note, &it.HiddenAt, &it.Books)
	})
}

// Preview — сколько живых изданий и работ скроет скрытие (для подтверждения в
// UI): автор — все книги, где он среди авторов, и работы, где он автор по
// большинству изданий; работа — её издания.
func (s *Store) Preview(ctx context.Context, kind Kind, id int64) (books, works int, err error) {
	switch kind {
	case KindAuthor:
		err = s.pool.QueryRow(ctx, `
			SELECT count(DISTINCT b.id), count(DISTINCT b.work_id)
			FROM book_authors ba JOIN books b ON b.id = ba.book_id
			WHERE ba.author_id = $1 AND b.deleted = false`, id).Scan(&books, &works)
	case KindWork:
		works = 1
		err = s.pool.QueryRow(ctx, `SELECT count(*) FROM books WHERE work_id = $1 AND deleted = false`, id).Scan(&books)
	default:
		err = fmt.Errorf("hiding: unknown kind %q", kind)
	}
	if err != nil {
		return 0, 0, fmt.Errorf("hiding: preview %s %d: %w", kind, id, err)
	}
	return books, works, nil
}

// Hidden — скрыт ли на инстансе объект по id: KindBook, KindWork, KindAuthor —
// флаги instance_hidden; KindSeries — у серии есть живые издания и все скрыты.
// Нет объекта — false (404 отдаст сам обработчик).
func (s *Store) Hidden(ctx context.Context, kind Kind, id int64) (bool, error) {
	var q string
	switch kind {
	case KindBook:
		q = `SELECT COALESCE((SELECT instance_hidden FROM books WHERE id = $1), false)`
	case KindWork:
		q = `SELECT COALESCE((SELECT instance_hidden FROM works WHERE id = $1), false)`
	case KindAuthor:
		q = `SELECT COALESCE((SELECT instance_hidden FROM authors WHERE id = $1), false)`
	case KindSeries:
		q = `SELECT EXISTS (SELECT 1 FROM books WHERE series_id = $1 AND deleted = false)
		        AND NOT EXISTS (SELECT 1 FROM books WHERE series_id = $1 AND deleted = false AND NOT instance_hidden)`
	default:
		return false, fmt.Errorf("hiding: unknown kind %q", kind)
	}
	var hidden bool
	if err := s.pool.QueryRow(ctx, q, id).Scan(&hidden); err != nil {
		return false, fmt.Errorf("hiding: hidden %s %d: %w", kind, id, err)
	}
	return hidden, nil
}

// Ref — автор или работа, из-за которых объект скрыт.
type Ref struct {
	Kind Kind   `json:"kind"`
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// Status — состояние скрытия объекта для админской плашки: Explicit — скрыт сам
// (кнопка «Вернуть»), Via — скрытые автор(ы) или работа, из-за которых он
// скрыт (возвращать их).
type Status struct {
	Hidden   bool  `json:"hidden"`
	Explicit bool  `json:"explicit"`
	Via      []Ref `json:"via"`
}

// StatusOf — состояние скрытия автора, работы или книги.
func (s *Store) StatusOf(ctx context.Context, kind Kind, id int64) (Status, error) {
	st := Status{Via: []Ref{}}
	var flagQ, explicitQ, viaQ string
	switch kind {
	case KindAuthor:
		flagQ = `SELECT COALESCE((SELECT instance_hidden FROM authors WHERE id = $1), false)`
		explicitQ = `SELECT EXISTS (SELECT 1 FROM hidden_authors WHERE author_id = $1)`
	case KindWork:
		flagQ = `SELECT COALESCE((SELECT instance_hidden FROM works WHERE id = $1), false)`
		explicitQ = `SELECT EXISTS (SELECT 1 FROM hidden_works WHERE work_id = $1)`
		viaQ = `SELECT DISTINCT 'author', a.id, btrim(concat_ws(' ', a.last_name, a.first_name, a.middle_name))
		        FROM books b JOIN book_authors ba ON ba.book_id = b.id
		        JOIN hidden_authors ha ON ha.author_id = ba.author_id JOIN authors a ON a.id = ha.author_id
		        WHERE b.work_id = $1 AND b.deleted = false`
	case KindBook:
		flagQ = `SELECT COALESCE((SELECT instance_hidden FROM books WHERE id = $1), false)`
		explicitQ = `SELECT false`
		viaQ = `SELECT 'author', a.id, btrim(concat_ws(' ', a.last_name, a.first_name, a.middle_name))
		        FROM book_authors ba JOIN hidden_authors ha ON ha.author_id = ba.author_id
		        JOIN authors a ON a.id = ha.author_id WHERE ba.book_id = $1
		        UNION
		        SELECT 'work', w.id, w.title FROM books b JOIN works w ON w.id = b.work_id
		        WHERE b.id = $1 AND w.instance_hidden`
	default:
		return st, fmt.Errorf("hiding: unknown kind %q", kind)
	}
	if err := s.pool.QueryRow(ctx, flagQ, id).Scan(&st.Hidden); err != nil {
		return st, fmt.Errorf("hiding: status %s %d: %w", kind, id, err)
	}
	if err := s.pool.QueryRow(ctx, explicitQ, id).Scan(&st.Explicit); err != nil {
		return st, fmt.Errorf("hiding: status %s %d: %w", kind, id, err)
	}
	if viaQ != "" && st.Hidden {
		rows, err := s.pool.Query(ctx, viaQ, id)
		if err != nil {
			return st, fmt.Errorf("hiding: status via %s %d: %w", kind, id, err)
		}
		refs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Ref, error) {
			var ref Ref
			return ref, r.Scan(&ref.Kind, &ref.ID, &ref.Name)
		})
		if err != nil {
			return st, fmt.Errorf("hiding: status via %s %d: %w", kind, id, err)
		}
		st.Via = append(st.Via, refs...)
	}
	return st, nil
}
