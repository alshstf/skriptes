package collections

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Умные полки (#389): сохранённые фильтры каталога /books. Книг не хранят —
// состав каждый раз считает поиск по тем же фильтрам (с учётом скрытого
// контента и «Только непрочитанные»), поэтому полка сама обновляется с
// импортом и чтением.

// ErrBadFilters — фильтры полки не прошли проверку.
var ErrBadFilters = errors.New("bad smart shelf filters")

// maxSmartShelves — потолок умных полок на пользователя.
const maxSmartShelves = 50

// ErrTooMany — умных полок слишком много.
var ErrTooMany = errors.New("too many smart shelves")

// SmartFilters — фильтры каталога /books (зеркало BooksSearch на фронте).
type SmartFilters struct {
	Q        string   `json:"q,omitempty"`
	Genres   []string `json:"genres,omitempty"`
	Lang     string   `json:"lang,omitempty"`
	SrcLang  string   `json:"src_lang,omitempty"`
	Kind     string   `json:"kind,omitempty"`
	YearFrom int      `json:"year_from,omitempty"`
	YearTo   int      `json:"year_to,omitempty"`
	SeriesID int64    `json:"series_id,omitempty"`
	AuthorID int64    `json:"author_id,omitempty"`
	Sort     string   `json:"sort,omitempty"`
	Unread   bool     `json:"unread,omitempty"`
	Awards   []string `json:"awards,omitempty"`
	HasAward bool     `json:"has_award,omitempty"`
}

// validate — пустые фильтры (весь каталог) и мусор не сохраняем.
func (f SmartFilters) validate() error {
	if len(f.Q) > 200 || len(f.Genres) > 50 || len(f.Lang) > 16 || len(f.SrcLang) > 16 || len(f.Kind) > 16 ||
		len(f.Awards) > 50 {
		return ErrBadFilters
	}
	for _, a := range f.Awards {
		if a == "" || len(a) > 64 {
			return ErrBadFilters
		}
	}
	for _, g := range f.Genres {
		if g == "" || len(g) > 64 {
			return ErrBadFilters
		}
	}
	if f.YearFrom < 0 || f.YearTo < 0 || f.SeriesID < 0 || f.AuthorID < 0 {
		return ErrBadFilters
	}
	switch f.Sort {
	case "", "year_desc", "year_asc":
	default:
		return ErrBadFilters
	}
	if f.Q == "" && len(f.Genres) == 0 && f.Lang == "" && f.SrcLang == "" && f.Kind == "" && f.YearFrom == 0 &&
		f.YearTo == 0 && f.SeriesID == 0 && f.AuthorID == 0 && !f.Unread && len(f.Awards) == 0 && !f.HasAward {
		return ErrBadFilters
	}
	return nil
}

// SmartShelf — умная полка пользователя.
type SmartShelf struct {
	ID        int64        `json:"id"`
	Name      string       `json:"name"`
	Filters   SmartFilters `json:"filters"`
	CreatedAt time.Time    `json:"created_at"`
	UpdatedAt time.Time    `json:"updated_at"`
}

// ListSmartShelves — умные полки пользователя в порядке создания.
func (s *Service) ListSmartShelves(ctx context.Context, userID int64) ([]SmartShelf, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, filters, created_at, updated_at FROM smart_shelves
		WHERE user_id = $1 ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, fmt.Errorf("list smart shelves: %w", err)
	}
	defer rows.Close()
	out := []SmartShelf{}
	for rows.Next() {
		var sh SmartShelf
		var raw []byte
		if err := rows.Scan(&sh.ID, &sh.Name, &raw, &sh.CreatedAt, &sh.UpdatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &sh.Filters) // неизвестные поля прошлых версий — игнор
		out = append(out, sh)
	}
	return out, rows.Err()
}

// CreateSmartShelf — сохранить фильтры как полку.
func (s *Service) CreateSmartShelf(ctx context.Context, userID int64, name string, f SmartFilters) (SmartShelf, error) {
	n, err := normalizeName(name)
	if err != nil {
		return SmartShelf{}, err
	}
	if err := f.validate(); err != nil {
		return SmartShelf{}, err
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM smart_shelves WHERE user_id = $1`, userID).Scan(&count); err != nil {
		return SmartShelf{}, err
	}
	if count >= maxSmartShelves {
		return SmartShelf{}, ErrTooMany
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return SmartShelf{}, err
	}
	sh := SmartShelf{Name: n, Filters: f}
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO smart_shelves (user_id, name, filters) VALUES ($1, $2, $3)
		RETURNING id, created_at, updated_at`, userID, n, raw).Scan(&sh.ID, &sh.CreatedAt, &sh.UpdatedAt); err != nil {
		return SmartShelf{}, fmt.Errorf("insert smart shelf: %w", err)
	}
	return sh, nil
}

// UpdateSmartShelf — переименовать и/или заменить фильтры СВОЕЙ полки (nil — не менять).
func (s *Service) UpdateSmartShelf(ctx context.Context, userID, id int64, name *string, f *SmartFilters) (SmartShelf, error) {
	var sh SmartShelf
	var raw []byte
	err := s.pool.QueryRow(ctx, `SELECT name, filters FROM smart_shelves WHERE id = $1 AND user_id = $2`, id, userID).
		Scan(&sh.Name, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return SmartShelf{}, ErrNotFound
	}
	if err != nil {
		return SmartShelf{}, err
	}
	_ = json.Unmarshal(raw, &sh.Filters)
	if name != nil {
		n, err := normalizeName(*name)
		if err != nil {
			return SmartShelf{}, err
		}
		sh.Name = n
	}
	if f != nil {
		if err := f.validate(); err != nil {
			return SmartShelf{}, err
		}
		sh.Filters = *f
	}
	if raw, err = json.Marshal(sh.Filters); err != nil {
		return SmartShelf{}, err
	}
	sh.ID = id
	if err := s.pool.QueryRow(ctx, `
		UPDATE smart_shelves SET name = $3, filters = $4, updated_at = now() WHERE id = $1 AND user_id = $2
		RETURNING created_at, updated_at`, id, userID, sh.Name, raw).Scan(&sh.CreatedAt, &sh.UpdatedAt); err != nil {
		return SmartShelf{}, fmt.Errorf("update smart shelf: %w", err)
	}
	return sh, nil
}

// DeleteSmartShelf — удалить СВОЮ полку (чужая/нет — ErrNotFound).
func (s *Service) DeleteSmartShelf(ctx context.Context, userID, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM smart_shelves WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete smart shelf: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
