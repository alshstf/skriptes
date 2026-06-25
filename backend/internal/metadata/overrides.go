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
// глобально). Значение МАТЕРИАЛИЗУЕТСЯ в реальную колонку (books.*/works.*), чтобы
// попадать в поиск/фильтры/фасеты; в metadata_overrides (миграция 0028) хранится
// original_value для отката + сам факт правки (индикатор «изменено» + гейты).
//
//   - edition-СКАЛЯРЫ (kind='book': edition_year/isbn/publisher/translator/
//     edition_title) — не индексируются и не перетираются импортом → материализация
//     прямо в books.* без ресинка/ре-апплая/гейтов (PR1).
//   - work-ПОЛЯ (kind='work': title/written_year) — материализуются в works.*,
//     ресинкаются в works-индекс, защищены гейтом recompute (NOT EXISTS
//     metadata_overrides в recomputeWorkAggregates/recomputeWorkTitles, иначе
//     группировка/merge/split их перетрут). Каталожные списки автора/серии читают
//     COALESCE(w.title, b.title) → видят work-оверрайд (PR2).
//
// ser_no/lang/genres/authors/перенос серий — следующими PR'ами (план
// ~/.claude/plans/cryptic-roaming-turing.md).
type OverrideController struct {
	pool     *pgxpool.Pool
	resyncer WorksIndexSyncer // таргетный ресинк works-индекса (nil → без ресинка)
	logger   *slog.Logger
}

// NewOverrideController. resyncer (обычно *importer.Importer) ресинкает works-индекс
// после правки индексируемого work-поля; nil допустим (без ресинка).
func NewOverrideController(pool *pgxpool.Pool, resyncer WorksIndexSyncer, logger *slog.Logger) *OverrideController {
	if logger == nil {
		logger = slog.Default()
	}
	return &OverrideController{pool: pool, resyncer: resyncer, logger: logger}
}

var (
	// ErrUnknownOverrideField — поле не из allow-list (или kind не поддержан).
	ErrUnknownOverrideField = errors.New("unknown override field")
	// ErrOverrideTargetNotFound — книга/работа с target_id не найдена.
	ErrOverrideTargetNotFound = errors.New("override target not found")
)

// overrideField — спецификация скалярного поля: колонка + тип JSON-значения.
type overrideField struct {
	column string
	typ    string // "int" | "text"
}

// bookScalarFields — allow-list edition-полей (kind='book'). Колонки ТОЛЬКО отсюда
// (не из ввода) → безопасно интерполировать в SQL-идентификатор.
var bookScalarFields = map[string]overrideField{
	"edition_year":  {"edition_year", "int"},
	"isbn":          {"isbn", "text"},
	"publisher":     {"publisher", "text"},
	"translator":    {"translator", "text"},
	"edition_title": {"edition_title", "text"},
}

// workFields — allow-list work-полей (kind='work'). Материализация особая (title —
// две колонки; written_year — + источник), поэтому обрабатываются switch'ем.
var workFields = map[string]bool{"title": true, "written_year": true}

// Override — факт правки поля (для индикаторов на карточке).
type Override struct {
	Kind  string `json:"kind"`
	Field string `json:"field"`
}

// SetOverride материализует правку поля и фиксирует её в леджере. setBy — id админа
// (0 → NULL). value — JSON {"v": …}.
func (c *OverrideController) SetOverride(ctx context.Context, kind string, targetID int64, field string, value json.RawMessage, setBy int64) error {
	switch kind {
	case "book":
		return c.setBookScalar(ctx, targetID, field, value, setBy)
	case "work":
		return c.setWorkField(ctx, targetID, field, value, setBy)
	default:
		return fmt.Errorf("%w: kind %q not supported", ErrUnknownOverrideField, kind)
	}
}

// RevertOverride возвращает поле к оригиналу и удаляет запись леджера.
func (c *OverrideController) RevertOverride(ctx context.Context, kind string, targetID int64, field string) error {
	switch kind {
	case "book":
		return c.revertBookScalar(ctx, targetID, field)
	case "work":
		return c.revertWorkField(ctx, targetID, field)
	default:
		return fmt.Errorf("%w: kind %q not supported", ErrUnknownOverrideField, kind)
	}
}

// ── kind='book': edition-скаляры (PR1) ────────────────────────────────────

func (c *OverrideController) setBookScalar(ctx context.Context, bookID int64, field string, value json.RawMessage, setBy int64) error {
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

	if err := upsertLedger(ctx, tx, "book", bookID, field, value, setBy, func() (json.RawMessage, error) {
		return captureScalar(ctx, tx, spec.column, bookID)
	}); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx,
		fmt.Sprintf(`UPDATE books SET %s=$1, updated_at=now() WHERE id=$2`, spec.column), newVal, bookID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrOverrideTargetNotFound
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	c.logger.Info("metadata override set", "kind", "book", "target", bookID, "field", field)
	return nil
}

func (c *OverrideController) revertBookScalar(ctx context.Context, bookID int64, field string) error {
	spec, ok := bookScalarFields[field]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownOverrideField, field)
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	orig, err := loadOriginal(ctx, tx, "book", bookID, field)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	oldVal, err := decodeScalar(orig, spec.typ)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		fmt.Sprintf(`UPDATE books SET %s=$1, updated_at=now() WHERE id=$2`, spec.column), oldVal, bookID); err != nil {
		return err
	}
	if err := deleteLedger(ctx, tx, "book", bookID, field); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	c.logger.Info("metadata override reverted", "kind", "book", "target", bookID, "field", field)
	return nil
}

// ── kind='work': title / written_year (PR2, материализация в works.*) ─────

func (c *OverrideController) setWorkField(ctx context.Context, workID int64, field string, value json.RawMessage, setBy int64) error {
	if !workFields[field] {
		return fmt.Errorf("%w: %s", ErrUnknownOverrideField, field)
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := upsertLedger(ctx, tx, "work", workID, field, value, setBy, func() (json.RawMessage, error) {
		return captureWorkOriginal(ctx, tx, workID, field)
	}); err != nil {
		return err
	}
	if err := materializeWork(ctx, tx, workID, field, value); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	c.syncWorkIndex(workID)
	c.logger.Info("metadata override set", "kind", "work", "target", workID, "field", field)
	return nil
}

func (c *OverrideController) revertWorkField(ctx context.Context, workID int64, field string) error {
	if !workFields[field] {
		return fmt.Errorf("%w: %s", ErrUnknownOverrideField, field)
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	orig, err := loadOriginal(ctx, tx, "work", workID, field)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := restoreWork(ctx, tx, workID, field, orig); err != nil {
		return err
	}
	if err := deleteLedger(ctx, tx, "work", workID, field); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	c.syncWorkIndex(workID)
	c.logger.Info("metadata override reverted", "kind", "work", "target", workID, "field", field)
	return nil
}

// captureWorkOriginal — снимок текущих значений work-поля (для отката).
func captureWorkOriginal(ctx context.Context, tx pgx.Tx, workID int64, field string) (json.RawMessage, error) {
	var sql string
	switch field {
	case "title":
		sql = `SELECT jsonb_build_object('title', title, 'normalized_title', normalized_title::text) FROM works WHERE id=$1`
	case "written_year":
		sql = `SELECT jsonb_build_object('written_year', written_year, 'written_year_source', written_year_source) FROM works WHERE id=$1`
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownOverrideField, field)
	}
	var raw json.RawMessage
	err := tx.QueryRow(ctx, sql, workID).Scan(&raw)
	return raw, err
}

// materializeWork пишет override_value в works.*. title → title+normalized_title
// (normalize = lower+trim+collapse-spaces, зеркало importer.normalize); written_year
// → written_year + source='override'.
func materializeWork(ctx context.Context, tx pgx.Tx, workID int64, field string, value json.RawMessage) error {
	switch field {
	case "title":
		title, err := decodeScalar(value, "text")
		if err != nil {
			return err
		}
		s, _ := title.(string)
		if s == "" {
			return fmt.Errorf("override: title must not be empty")
		}
		tag, err := tx.Exec(ctx, `
			UPDATE works SET title=$1,
			    normalized_title=lower(btrim(regexp_replace($1, '\s+', ' ', 'g')))::citext,
			    updated_at=now()
			WHERE id=$2`, s, workID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrOverrideTargetNotFound
		}
	case "written_year":
		year, err := decodeScalar(value, "int")
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx,
			`UPDATE works SET written_year=$1, written_year_source='override', updated_at=now() WHERE id=$2`,
			year, workID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrOverrideTargetNotFound
		}
	default:
		return fmt.Errorf("%w: %s", ErrUnknownOverrideField, field)
	}
	return nil
}

// restoreWork восстанавливает work-поле из original_value (составной объект).
func restoreWork(ctx context.Context, tx pgx.Tx, workID int64, field string, original json.RawMessage) error {
	switch field {
	case "title":
		var o struct {
			Title      *string `json:"title"`
			Normalized *string `json:"normalized_title"`
		}
		if err := json.Unmarshal(original, &o); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`UPDATE works SET title=$1, normalized_title=$2::citext, updated_at=now() WHERE id=$3`,
			o.Title, o.Normalized, workID)
		return err
	case "written_year":
		var o struct {
			Year   *int    `json:"written_year"`
			Source *string `json:"written_year_source"`
		}
		if err := json.Unmarshal(original, &o); err != nil {
			return err
		}
		_, err := tx.Exec(ctx,
			`UPDATE works SET written_year=$1, written_year_source=$2, updated_at=now() WHERE id=$3`,
			o.Year, o.Source, workID)
		return err
	default:
		return fmt.Errorf("%w: %s", ErrUnknownOverrideField, field)
	}
}

// syncWorkIndex — таргетный ресинк works-индекса после правки (детачнуто, чтобы не
// держать админ-запрос). Зеркало WorkGroupController.syncSearchAfterManual.
func (c *OverrideController) syncWorkIndex(workID int64) {
	if c.resyncer == nil {
		return
	}
	go func() {
		ctx := context.Background()
		if err := c.resyncer.UpsertWorksToIndex(ctx, []int64{workID}); err != nil {
			c.logger.Warn("override: works index resync failed", "work", workID, "err", err)
		}
	}()
}

// ── Общее: леджер, откат-всё, индикаторы ──────────────────────────────────

// RevertAllForBook откатывает все правки книги И её работы.
func (c *OverrideController) RevertAllForBook(ctx context.Context, bookID int64) error {
	var workID *int64
	if err := c.pool.QueryRow(ctx, `SELECT work_id FROM books WHERE id=$1`, bookID).Scan(&workID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrOverrideTargetNotFound
		}
		return err
	}
	bookFs, err := scanStrings(ctx, c.pool,
		`SELECT field FROM metadata_overrides WHERE target_kind='book' AND target_id=$1`, bookID)
	if err != nil {
		return err
	}
	for _, f := range bookFs {
		if err := c.RevertOverride(ctx, "book", bookID, f); err != nil {
			return err
		}
	}
	if workID != nil {
		workFs, err := scanStrings(ctx, c.pool,
			`SELECT field FROM metadata_overrides WHERE target_kind='work' AND target_id=$1`, *workID)
		if err != nil {
			return err
		}
		for _, f := range workFs {
			if err := c.RevertOverride(ctx, "work", *workID, f); err != nil {
				return err
			}
		}
	}
	return nil
}

// OverridesForWork — правки изданий работы (per book_id) + work-уровневые, для
// индикаторов «изменено» на карточке (api-слой, только админу).
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

// upsertLedger вставляет/обновляет запись правки. capture зовётся ТОЛЬКО при первой
// правке (повторная НЕ перезахватывает original → откат к истинному оригиналу).
func upsertLedger(ctx context.Context, tx pgx.Tx, kind string, targetID int64, field string, value json.RawMessage, setBy int64, capture func() (json.RawMessage, error)) error {
	var has bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM metadata_overrides WHERE target_kind=$1 AND target_id=$2 AND field=$3)`,
		kind, targetID, field).Scan(&has); err != nil {
		return err
	}
	if has {
		_, err := tx.Exec(ctx,
			`UPDATE metadata_overrides SET override_value=$4, set_by=NULLIF($5,0)::bigint, updated_at=now()
			 WHERE target_kind=$1 AND target_id=$2 AND field=$3`,
			kind, targetID, field, value, setBy)
		return err
	}
	orig, err := capture()
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrOverrideTargetNotFound
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO metadata_overrides (target_kind, target_id, field, override_value, original_value, set_by)
		 VALUES ($1, $2, $3, $4, $5, NULLIF($6,0)::bigint)`,
		kind, targetID, field, value, orig, setBy)
	return err
}

func loadOriginal(ctx context.Context, tx pgx.Tx, kind string, targetID int64, field string) (json.RawMessage, error) {
	var raw json.RawMessage
	err := tx.QueryRow(ctx,
		`SELECT original_value FROM metadata_overrides WHERE target_kind=$1 AND target_id=$2 AND field=$3`,
		kind, targetID, field).Scan(&raw)
	return raw, err
}

func deleteLedger(ctx context.Context, tx pgx.Tx, kind string, targetID int64, field string) error {
	_, err := tx.Exec(ctx,
		`DELETE FROM metadata_overrides WHERE target_kind=$1 AND target_id=$2 AND field=$3`, kind, targetID, field)
	return err
}

// captureScalar возвращает текущее значение колонки books как JSONB {"v": …}.
func captureScalar(ctx context.Context, tx pgx.Tx, column string, bookID int64) (json.RawMessage, error) {
	var raw json.RawMessage
	err := tx.QueryRow(ctx,
		fmt.Sprintf(`SELECT jsonb_build_object('v', %s) FROM books WHERE id=$1`, column), bookID).Scan(&raw)
	return raw, err
}

// decodeScalar разворачивает {"v": …} в типизированное значение (nil → NULL).
func decodeScalar(raw json.RawMessage, typ string) (any, error) {
	var w struct {
		V json.RawMessage `json:"v"`
	}
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, fmt.Errorf("invalid override value: %w", err)
	}
	if len(w.V) == 0 || string(w.V) == "null" {
		return nil, nil
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
