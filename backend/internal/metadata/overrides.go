package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// OverrideController — локальные ручные правки метаданных каталога (только админ,
// глобально). Значение МАТЕРИАЛИЗУЕТСЯ в реальную колонку (books.*), чтобы попадать
// в поиск/фильтры/фасеты; в metadata_overrides (миграция 0028) хранится
// original_value для отката + сам факт правки (индикатор «изменено» + гейты).
//
// PR1 — edition-уровневые СКАЛЯРНЫЕ поля (edition_year/isbn/publisher/translator/
// edition_title): не индексируются и НЕ перетираются импортом (enriched-поля), так
// что материализуются прямо в books.* без ресинка Meili / ре-апплая / гейта
// recompute. Work-уровневые (title/year/series, dual-write + ресинк + гейты) и lang
// (индексируется) — следующими PR'ами (см. план cryptic-roaming-turing).
type OverrideController struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// NewOverrideController — в PR2+ добавится resyncer (WorkIDResyncer) для ресинка
// Meili после правки индексируемых полей.
func NewOverrideController(pool *pgxpool.Pool, logger *slog.Logger) *OverrideController {
	if logger == nil {
		logger = slog.Default()
	}
	return &OverrideController{pool: pool, logger: logger}
}

var (
	// ErrUnknownOverrideField — поле не из allow-list (или kind ещё не поддержан).
	ErrUnknownOverrideField = errors.New("unknown override field")
	// ErrOverrideTargetNotFound — книга/работа с target_id не найдена.
	ErrOverrideTargetNotFound = errors.New("override target not found")
)

// overrideField — спецификация скалярного поля: колонка в books + тип JSON-значения.
type overrideField struct {
	column string
	typ    string // "int" | "text"
}

// bookScalarFields — allow-list edition-полей PR1. Колонки берутся ТОЛЬКО отсюда
// (не из пользовательского ввода) → безопасно интерполировать в SQL-идентификатор.
var bookScalarFields = map[string]overrideField{
	"edition_year":  {"edition_year", "int"},
	"isbn":          {"isbn", "text"},
	"publisher":     {"publisher", "text"},
	"translator":    {"translator", "text"},
	"edition_title": {"edition_title", "text"},
}

// Override — факт правки поля (для индикаторов на карточке).
type Override struct {
	Kind  string `json:"kind"`  // 'book' | 'work'
	Field string `json:"field"` // имя поля
}

// SetOverride материализует ручную правку поля книги и фиксирует её в леджере.
// setBy — id админа (0 → NULL). value — JSON {"v": …} (NULL представим как {"v":null}).
func (c *OverrideController) SetOverride(ctx context.Context, kind string, targetID int64, field string, value json.RawMessage, setBy int64) error {
	if kind != "book" {
		return fmt.Errorf("%w: kind %q not supported yet", ErrUnknownOverrideField, kind)
	}
	spec, ok := bookScalarFields[field]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownOverrideField, field)
	}
	newVal, err := decodeScalar(value, spec.typ)
	if err != nil {
		return err
	}

	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var hasLedger bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM metadata_overrides WHERE target_kind='book' AND target_id=$1 AND field=$2)`,
		targetID, field).Scan(&hasLedger); err != nil {
		return err
	}
	if hasLedger {
		// Повторная правка: НЕ перезахватываем original (откат к истинному оригиналу).
		if _, err := tx.Exec(ctx,
			`UPDATE metadata_overrides SET override_value=$3, set_by=NULLIF($4,0)::bigint, updated_at=now()
			 WHERE target_kind='book' AND target_id=$1 AND field=$2`,
			targetID, field, value, setBy); err != nil {
			return err
		}
	} else {
		orig, err := captureScalar(ctx, tx, spec.column, targetID)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOverrideTargetNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO metadata_overrides (target_kind, target_id, field, override_value, original_value, set_by)
			 VALUES ('book', $1, $2, $3, $4, NULLIF($5,0)::bigint)`,
			targetID, field, value, orig, setBy); err != nil {
			return err
		}
	}

	// Материализуем в колонку. column из allow-list → безопасно.
	tag, err := tx.Exec(ctx,
		fmt.Sprintf(`UPDATE books SET %s=$1, updated_at=now() WHERE id=$2`, spec.column),
		newVal, targetID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrOverrideTargetNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	c.logger.Info("metadata override set", "kind", kind, "target", targetID, "field", field)
	return nil
}

// RevertOverride возвращает поле к оригиналу и убирает запись из леджера.
func (c *OverrideController) RevertOverride(ctx context.Context, kind string, targetID int64, field string) error {
	if kind != "book" {
		return fmt.Errorf("%w: kind %q not supported yet", ErrUnknownOverrideField, kind)
	}
	spec, ok := bookScalarFields[field]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownOverrideField, field)
	}

	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var orig json.RawMessage
	err = tx.QueryRow(ctx,
		`SELECT original_value FROM metadata_overrides WHERE target_kind='book' AND target_id=$1 AND field=$2`,
		targetID, field).Scan(&orig)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // нечего откатывать
	}
	if err != nil {
		return err
	}
	oldVal, err := decodeScalar(orig, spec.typ)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		fmt.Sprintf(`UPDATE books SET %s=$1, updated_at=now() WHERE id=$2`, spec.column),
		oldVal, targetID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM metadata_overrides WHERE target_kind='book' AND target_id=$1 AND field=$2`,
		targetID, field); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	c.logger.Info("metadata override reverted", "kind", kind, "target", targetID, "field", field)
	return nil
}

// RevertAllForBook откатывает все правки книги (PR2+ — и правки её work).
func (c *OverrideController) RevertAllForBook(ctx context.Context, bookID int64) error {
	fields, err := scanStrings(ctx, c.pool,
		`SELECT field FROM metadata_overrides WHERE target_kind='book' AND target_id=$1`, bookID)
	if err != nil {
		return err
	}
	for _, f := range fields {
		if err := c.RevertOverride(ctx, "book", bookID, f); err != nil {
			return err
		}
	}
	return nil
}

// OverridesForWork — правки всех изданий работы (per book_id) + work-уровневые,
// для отрисовки индикаторов «изменено» на карточке (api-слой, только админу).
func (c *OverrideController) OverridesForWork(ctx context.Context, workID int64) (perBook map[int64][]string, workFields []string, err error) {
	rows, err := c.pool.Query(ctx, `
		SELECT target_kind, target_id, field
		FROM metadata_overrides
		WHERE (target_kind='work' AND target_id=$1)
		   OR (target_kind='book' AND target_id IN (SELECT id FROM books WHERE work_id=$1))
	`, workID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	perBook = map[int64][]string{}
	for rows.Next() {
		var kind, field string
		var tid int64
		if err := rows.Scan(&kind, &tid, &field); err != nil {
			return nil, nil, err
		}
		if kind == "work" {
			workFields = append(workFields, field)
		} else {
			perBook[tid] = append(perBook[tid], field)
		}
	}
	return perBook, workFields, rows.Err()
}

// captureScalar возвращает текущее значение колонки как JSONB {"v": …} (для отката).
func captureScalar(ctx context.Context, ex queryRower, column string, bookID int64) (json.RawMessage, error) {
	var raw json.RawMessage
	err := ex.QueryRow(ctx,
		fmt.Sprintf(`SELECT jsonb_build_object('v', %s) FROM books WHERE id=$1`, column),
		bookID).Scan(&raw)
	return raw, err
}

// decodeScalar разворачивает {"v": …} в типизированное значение для UPDATE
// (nil → NULL). typ управляет проверкой типа JSON.
func decodeScalar(raw json.RawMessage, typ string) (any, error) {
	var w struct {
		V json.RawMessage `json:"v"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, fmt.Errorf("invalid override value: %w", err)
	}
	if len(w.V) == 0 || string(w.V) == "null" {
		return nil, nil // NULL
	}
	switch typ {
	case "int":
		var n int
		if err := json.Unmarshal(w.V, &n); err != nil {
			return nil, fmt.Errorf("override value: expected int: %w", err)
		}
		return n, nil
	case "text":
		var s string
		if err := json.Unmarshal(w.V, &s); err != nil {
			return nil, fmt.Errorf("override value: expected text: %w", err)
		}
		return s, nil
	default:
		return nil, fmt.Errorf("override: unknown field type %q", typ)
	}
}

// scanStrings — мелкий хелпер (зеркало scanInt64s) для одноколоночных text-выборок.
func scanStrings(ctx context.Context, ex interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}, sql string, args ...any) ([]string, error) {
	rows, err := ex.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
