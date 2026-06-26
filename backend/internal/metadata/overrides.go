package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pgxExec — общий интерфейс *pgxpool.Pool и pgx.Tx: M:N-хелперы зовутся и в
// tx-правке (set/revert), и из пула при ре-апплае после импорта.
type pgxExec interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

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

// overrideField — спецификация скалярного поля: колонка + тип JSON-значения +
// indexed (поле в Meili → после правки ресинкаем works-индекс работы; lang).
type overrideField struct {
	column  string
	typ     string // "int" | "text"
	indexed bool
}

// bookScalarFields — allow-list edition-полей (kind='book'). Колонки ТОЛЬКО отсюда
// (не из ввода) → безопасно интерполировать в SQL-идентификатор.
var bookScalarFields = map[string]overrideField{
	"edition_year":  {"edition_year", "int", false},
	"isbn":          {"isbn", "text", false},
	"publisher":     {"publisher", "text", false},
	"translator":    {"translator", "text", false},
	"edition_title": {"edition_title", "text", false},
	"lang":          {"lang", "text", true}, // в works-индексе (lang[]) → ресинк
}

// workFields — allow-list work-полей (kind='work'). Материализация особая (title —
// две колонки; written_year — + источник; ser_no — скаляр), поэтому switch'ем.
var workFields = map[string]bool{"title": true, "written_year": true, "ser_no": true}

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
		if field == "genres" {
			return c.setWorkGenres(ctx, targetID, value, setBy)
		}
		if field == "authors" {
			return c.setWorkAuthors(ctx, targetID, value, setBy)
		}
		if field == "series" {
			return c.setWorkSeries(ctx, targetID, value, setBy)
		}
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
		if field == "genres" {
			return c.revertWorkGenres(ctx, targetID)
		}
		if field == "authors" {
			return c.revertWorkAuthors(ctx, targetID)
		}
		if field == "series" {
			return c.revertWorkSeries(ctx, targetID)
		}
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
	newVal = normalizeBookValue(spec.column, newVal)
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
	if spec.indexed {
		c.resyncBookWork(ctx, bookID)
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
	if spec.indexed {
		c.resyncBookWork(ctx, bookID)
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

// ── kind='work', field='genres': M:N (PR5) ────────────────────────────────
//
// Жанры карточки/индекса работы = union book_genres всех её живых изданий
// (queryWorkGenres, workDocSelect). Поэтому оверрайд жанров материализуется в
// book_genres ВСЕХ изданий работы (одинаковый набор → union = набор). original —
// per-edition снапшот кодов (точный откат). Импорт перетирает book_genres
// (replaceBookGenres) → нужен ре-апплай (см. ReapplyAfterImport). Не зеркалится в
// books-индекс (OPDS) — как title/lang, освежается полным импортом.

func (c *OverrideController) setWorkGenres(ctx context.Context, workID int64, value json.RawMessage, setBy int64) error {
	var v struct {
		Codes []string `json:"codes"`
	}
	if err := json.Unmarshal(value, &v); err != nil {
		return fmt.Errorf("invalid genres value: %w", err)
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := upsertLedger(ctx, tx, "work", workID, "genres", value, setBy, func() (json.RawMessage, error) {
		return captureWorkGenres(ctx, tx, workID)
	}); err != nil {
		return err
	}
	if err := materializeWorkGenres(ctx, tx, workID, v.Codes); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	c.syncWorkIndex(workID)
	c.logger.Info("metadata override set", "kind", "work", "target", workID, "field", "genres")
	return nil
}

func (c *OverrideController) revertWorkGenres(ctx context.Context, workID int64) error {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	orig, err := loadOriginal(ctx, tx, "work", workID, "genres")
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := restoreWorkGenres(ctx, tx, orig); err != nil {
		return err
	}
	if err := deleteLedger(ctx, tx, "work", workID, "genres"); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	c.syncWorkIndex(workID)
	c.logger.Info("metadata override reverted", "kind", "work", "target", workID, "field", "genres")
	return nil
}

// ensureGenreIDs резолвит fb2-коды в genre_id, создавая отсутствующие (зеркало
// importer.upsertGenre). Коды → lower+trim; пустые/дубли отброшены.
func ensureGenreIDs(ctx context.Context, ex pgxExec, codes []string) ([]int64, error) {
	ids := make([]int64, 0, len(codes))
	seen := map[string]bool{}
	for _, c := range codes {
		c = strings.ToLower(strings.TrimSpace(c))
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		var id int64
		if err := ex.QueryRow(ctx,
			`INSERT INTO genres (fb2_code) VALUES ($1)
			 ON CONFLICT (fb2_code) DO UPDATE SET fb2_code=EXCLUDED.fb2_code RETURNING id`,
			c).Scan(&id); err != nil {
			return nil, fmt.Errorf("ensure genre %q: %w", c, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// liveEditionIDs — id живых изданий работы.
func liveEditionIDs(ctx context.Context, ex pgxExec, workID int64) ([]int64, error) {
	rows, err := ex.Query(ctx, `SELECT id FROM books WHERE work_id=$1 AND deleted=false ORDER BY id`, workID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// captureWorkGenres — снапшот book_genres (коды) по каждому живому изданию работы.
// {"editions": {"<bookID>": ["sf", …]}}. pgx.ErrNoRows, если живых изданий нет.
func captureWorkGenres(ctx context.Context, ex pgxExec, workID int64) (json.RawMessage, error) {
	rows, err := ex.Query(ctx, `
		SELECT b.id, COALESCE(array_agg(g.fb2_code ORDER BY g.fb2_code)
		                      FILTER (WHERE g.fb2_code IS NOT NULL), '{}')
		FROM books b
		LEFT JOIN book_genres bg ON bg.book_id=b.id
		LEFT JOIN genres g ON g.id=bg.genre_id
		WHERE b.work_id=$1 AND b.deleted=false
		GROUP BY b.id`, workID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	editions := map[string][]string{}
	for rows.Next() {
		var id int64
		var codes []string
		if err := rows.Scan(&id, &codes); err != nil {
			return nil, err
		}
		editions[strconv.FormatInt(id, 10)] = codes
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(editions) == 0 {
		return nil, pgx.ErrNoRows
	}
	return json.Marshal(map[string]any{"editions": editions})
}

// setEditionGenres переписывает book_genres одного издания на genreIDs (DELETE+INSERT).
func setEditionGenres(ctx context.Context, ex pgxExec, bookID int64, genreIDs []int64) error {
	if _, err := ex.Exec(ctx, `DELETE FROM book_genres WHERE book_id=$1`, bookID); err != nil {
		return err
	}
	for _, gid := range genreIDs {
		if _, err := ex.Exec(ctx,
			`INSERT INTO book_genres (book_id, genre_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`,
			bookID, gid); err != nil {
			return err
		}
	}
	return nil
}

// materializeWorkGenres ставит набор жанров (коды) на ВСЕ живые издания работы.
func materializeWorkGenres(ctx context.Context, ex pgxExec, workID int64, codes []string) error {
	ids, err := ensureGenreIDs(ctx, ex, codes)
	if err != nil {
		return err
	}
	editions, err := liveEditionIDs(ctx, ex, workID)
	if err != nil {
		return err
	}
	if len(editions) == 0 {
		return ErrOverrideTargetNotFound
	}
	for _, bid := range editions {
		if err := setEditionGenres(ctx, ex, bid, ids); err != nil {
			return err
		}
	}
	return nil
}

// restoreWorkGenres восстанавливает per-edition снапшот из original_value.
func restoreWorkGenres(ctx context.Context, ex pgxExec, original json.RawMessage) error {
	var o struct {
		Editions map[string][]string `json:"editions"`
	}
	if err := json.Unmarshal(original, &o); err != nil {
		return err
	}
	for bidStr, codes := range o.Editions {
		bid, err := strconv.ParseInt(bidStr, 10, 64)
		if err != nil {
			continue
		}
		ids, err := ensureGenreIDs(ctx, ex, codes)
		if err != nil {
			return err
		}
		if err := setEditionGenres(ctx, ex, bid, ids); err != nil {
			return err
		}
	}
	return nil
}

// ── kind='work', field='authors': M:N (PR6) ──────────────────────────────
//
// Авторы карточки/works-индекса = union book_authors всех живых изданий работы
// (queryWorkAuthors, workDocSelect). Оверрайд упорядоченного набора author_id
// материализуется в book_authors ВСЕХ изданий (position = индекс) + обновляет
// works.primary_author_id = первый автор (нигде не пересчитывается — UPDATE
// primary_author_id есть ТОЛЬКО здесь и при INSERT работы; гейт recompute не нужен).
// original — per-edition снапшот (author_id, position); primary_author_id НЕ храним
// (импорт его не перетирает → выводим из восстановленных авторов при откате).
// Импорт (replaceBookAuthors) перетирает book_authors → нужен ре-апплай. Авторы
// выбираются из СУЩЕСТВУЮЩИХ (suggest); создание новых — отдельный follow-up.

func (c *OverrideController) setWorkAuthors(ctx context.Context, workID int64, value json.RawMessage, setBy int64) error {
	var v struct {
		AuthorIDs []int64 `json:"author_ids"`
	}
	if err := json.Unmarshal(value, &v); err != nil {
		return fmt.Errorf("invalid authors value: %w", err)
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := upsertLedger(ctx, tx, "work", workID, "authors", value, setBy, func() (json.RawMessage, error) {
		return captureWorkAuthors(ctx, tx, workID)
	}); err != nil {
		return err
	}
	if err := materializeWorkAuthors(ctx, tx, workID, v.AuthorIDs); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	c.syncWorkIndex(workID)
	c.logger.Info("metadata override set", "kind", "work", "target", workID, "field", "authors")
	return nil
}

func (c *OverrideController) revertWorkAuthors(ctx context.Context, workID int64) error {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	orig, err := loadOriginal(ctx, tx, "work", workID, "authors")
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := restoreWorkAuthors(ctx, tx, workID, orig); err != nil {
		return err
	}
	if err := deleteLedger(ctx, tx, "work", workID, "authors"); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	c.syncWorkIndex(workID)
	c.logger.Info("metadata override reverted", "kind", "work", "target", workID, "field", "authors")
	return nil
}

// captureWorkAuthors — снапшот book_authors (id+position) по каждому живому изданию
// работы + works.primary_author_id. pgx.ErrNoRows, если живых изданий нет.
func captureWorkAuthors(ctx context.Context, ex pgxExec, workID int64) (json.RawMessage, error) {
	rows, err := ex.Query(ctx, `
		SELECT b.id, COALESCE(jsonb_agg(jsonb_build_object('id', ba.author_id, 'pos', ba.position)
		                                ORDER BY ba.position) FILTER (WHERE ba.author_id IS NOT NULL), '[]'::jsonb)
		FROM books b
		LEFT JOIN book_authors ba ON ba.book_id=b.id
		WHERE b.work_id=$1 AND b.deleted=false
		GROUP BY b.id`, workID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	editions := map[string]json.RawMessage{}
	for rows.Next() {
		var id int64
		var arr json.RawMessage
		if err := rows.Scan(&id, &arr); err != nil {
			return nil, err
		}
		editions[strconv.FormatInt(id, 10)] = arr
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(editions) == 0 {
		return nil, pgx.ErrNoRows
	}
	// primary_author_id НЕ храним: импорт его не перетирает (остался бы = оверрайд),
	// поэтому при откате выводим из восстановленных авторов (см. restoreWorkAuthors).
	return json.Marshal(map[string]any{"editions": editions})
}

// setEditionAuthors переписывает book_authors одного издания на authorIDs (position = индекс).
func setEditionAuthors(ctx context.Context, ex pgxExec, bookID int64, authorIDs []int64) error {
	if _, err := ex.Exec(ctx, `DELETE FROM book_authors WHERE book_id=$1`, bookID); err != nil {
		return err
	}
	for i, aid := range authorIDs {
		if _, err := ex.Exec(ctx,
			`INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`,
			bookID, aid, i); err != nil {
			return err
		}
	}
	return nil
}

// materializeWorkAuthors ставит упорядоченный набор авторов на ВСЕ живые издания
// работы + works.primary_author_id = первый (NULL, если пусто).
func materializeWorkAuthors(ctx context.Context, ex pgxExec, workID int64, authorIDs []int64) error {
	editions, err := liveEditionIDs(ctx, ex, workID)
	if err != nil {
		return err
	}
	if len(editions) == 0 {
		return ErrOverrideTargetNotFound
	}
	for _, bid := range editions {
		if err := setEditionAuthors(ctx, ex, bid, authorIDs); err != nil {
			return err
		}
	}
	var primary any
	if len(authorIDs) > 0 {
		primary = authorIDs[0]
	}
	_, err = ex.Exec(ctx, `UPDATE works SET primary_author_id=$1, updated_at=now() WHERE id=$2`, primary, workID)
	return err
}

// restoreWorkAuthors восстанавливает per-edition снапшот book_authors и выводит
// works.primary_author_id из восстановленных авторов (первый по position).
func restoreWorkAuthors(ctx context.Context, ex pgxExec, workID int64, original json.RawMessage) error {
	var o struct {
		Editions map[string][]struct {
			ID  int64 `json:"id"`
			Pos int   `json:"pos"`
		} `json:"editions"`
	}
	if err := json.Unmarshal(original, &o); err != nil {
		return err
	}
	for bidStr, authors := range o.Editions {
		bid, err := strconv.ParseInt(bidStr, 10, 64)
		if err != nil {
			continue
		}
		if _, err := ex.Exec(ctx, `DELETE FROM book_authors WHERE book_id=$1`, bid); err != nil {
			return err
		}
		for _, a := range authors {
			if _, err := ex.Exec(ctx,
				`INSERT INTO book_authors (book_id, author_id, position) VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`,
				bid, a.ID, a.Pos); err != nil {
				return err
			}
		}
	}
	return setPrimaryFromEditions(ctx, ex, workID)
}

// setPrimaryFromEditions выводит works.primary_author_id из book_authors живых
// изданий работы (первый по position; NULL, если авторов нет).
func setPrimaryFromEditions(ctx context.Context, ex pgxExec, workID int64) error {
	var primary *int64
	err := ex.QueryRow(ctx, `
		SELECT ba.author_id FROM book_authors ba
		JOIN books b ON b.id=ba.book_id
		WHERE b.work_id=$1 AND b.deleted=false
		ORDER BY ba.position, ba.author_id LIMIT 1`, workID).Scan(&primary)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	_, err = ex.Exec(ctx, `UPDATE works SET primary_author_id=$1, updated_at=now() WHERE id=$2`, primary, workID)
	return err
}

// ── kind='work', field='series': перенос между сериями (PR7) ──────────────
//
// Серия читается тремя путями: card — COALESCE(w.series_id, b.series_id); works-
// индекс — w.series_id; страница /series/X — b.series_id (издания). Поэтому оверрайд
// материализуется в works.series_id/ser_no И в ВСЕ издания books.series_id/ser_no.
// value: {"series_id": X|null, "ser_no": N|null} (null = убрать из серии). original —
// per-edition снапшот (series_id, ser_no); works.* выводим из изданий при откате
// (как authors.primary — импорт works.* не перетирает). Гейт series-UPDATE recompute
// уже покрывает 'series' (PR3). Импорт перетирает books.series_id/ser_no → ре-апплай.
// Серия выбирается из СУЩЕСТВУЮЩИХ (suggest); создание новой — follow-up.

func (c *OverrideController) setWorkSeries(ctx context.Context, workID int64, value json.RawMessage, setBy int64) error {
	var v struct {
		SeriesID *int64 `json:"series_id"`
		SerNo    *int   `json:"ser_no"`
	}
	if err := json.Unmarshal(value, &v); err != nil {
		return fmt.Errorf("invalid series value: %w", err)
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := upsertLedger(ctx, tx, "work", workID, "series", value, setBy, func() (json.RawMessage, error) {
		return captureWorkSeries(ctx, tx, workID)
	}); err != nil {
		return err
	}
	if err := materializeWorkSeries(ctx, tx, workID, v.SeriesID, v.SerNo); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	c.syncWorkIndex(workID)
	c.logger.Info("metadata override set", "kind", "work", "target", workID, "field", "series")
	return nil
}

func (c *OverrideController) revertWorkSeries(ctx context.Context, workID int64) error {
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	orig, err := loadOriginal(ctx, tx, "work", workID, "series")
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := restoreWorkSeries(ctx, tx, workID, orig); err != nil {
		return err
	}
	if err := deleteLedger(ctx, tx, "work", workID, "series"); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	c.syncWorkIndex(workID)
	c.logger.Info("metadata override reverted", "kind", "work", "target", workID, "field", "series")
	return nil
}

// captureWorkSeries — снапшот (series_id, ser_no) по каждому живому изданию работы.
func captureWorkSeries(ctx context.Context, ex pgxExec, workID int64) (json.RawMessage, error) {
	var ed json.RawMessage
	if err := ex.QueryRow(ctx, `
		SELECT jsonb_object_agg(b.id::text, jsonb_build_object('series_id', b.series_id, 'ser_no', b.ser_no))
		FROM books b WHERE b.work_id=$1 AND b.deleted=false`, workID).Scan(&ed); err != nil {
		return nil, err
	}
	if len(ed) == 0 || string(ed) == "null" {
		return nil, pgx.ErrNoRows
	}
	return json.Marshal(map[string]json.RawMessage{"editions": ed})
}

// materializeWorkSeries ставит серию+номер на ВСЕ живые издания + works.series_id/ser_no.
func materializeWorkSeries(ctx context.Context, ex pgxExec, workID int64, seriesID *int64, serNo *int) error {
	editions, err := liveEditionIDs(ctx, ex, workID)
	if err != nil {
		return err
	}
	if len(editions) == 0 {
		return ErrOverrideTargetNotFound
	}
	for _, bid := range editions {
		if _, err := ex.Exec(ctx,
			`UPDATE books SET series_id=$1, ser_no=$2, updated_at=now() WHERE id=$3`, seriesID, serNo, bid); err != nil {
			return err
		}
	}
	_, err = ex.Exec(ctx, `UPDATE works SET series_id=$1, ser_no=$2, updated_at=now() WHERE id=$3`, seriesID, serNo, workID)
	return err
}

// restoreWorkSeries восстанавливает per-edition (series_id, ser_no) + выводит works.* из изданий.
func restoreWorkSeries(ctx context.Context, ex pgxExec, workID int64, original json.RawMessage) error {
	var o struct {
		Editions map[string]struct {
			SeriesID *int64 `json:"series_id"`
			SerNo    *int   `json:"ser_no"`
		} `json:"editions"`
	}
	if err := json.Unmarshal(original, &o); err != nil {
		return err
	}
	for bidStr, s := range o.Editions {
		bid, err := strconv.ParseInt(bidStr, 10, 64)
		if err != nil {
			continue
		}
		if _, err := ex.Exec(ctx,
			`UPDATE books SET series_id=$1, ser_no=$2, updated_at=now() WHERE id=$3`, s.SeriesID, s.SerNo, bid); err != nil {
			return err
		}
	}
	return setWorkSeriesFromEditions(ctx, ex, workID)
}

// setWorkSeriesFromEditions выводит works.series_id/ser_no из изданий (предпочитая
// издание с непустой серией; зеркало recompute, NULL — если серии нет).
func setWorkSeriesFromEditions(ctx context.Context, ex pgxExec, workID int64) error {
	var sid *int64
	var sn *int
	err := ex.QueryRow(ctx, `
		SELECT series_id, ser_no FROM books
		WHERE work_id=$1 AND deleted=false
		ORDER BY (series_id IS NOT NULL) DESC, id LIMIT 1`, workID).Scan(&sid, &sn)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	_, err = ex.Exec(ctx, `UPDATE works SET series_id=$1, ser_no=$2, updated_at=now() WHERE id=$3`, sid, sn, workID)
	return err
}

// captureWorkOriginal — снимок текущих значений work-поля (для отката).
func captureWorkOriginal(ctx context.Context, tx pgx.Tx, workID int64, field string) (json.RawMessage, error) {
	var sql string
	switch field {
	case "title":
		sql = `SELECT jsonb_build_object('title', title, 'normalized_title', normalized_title::text) FROM works WHERE id=$1`
	case "written_year":
		sql = `SELECT jsonb_build_object('written_year', written_year, 'written_year_source', written_year_source) FROM works WHERE id=$1`
	case "ser_no":
		sql = `SELECT jsonb_build_object('ser_no', ser_no) FROM works WHERE id=$1`
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
	case "ser_no":
		serNo, err := decodeScalar(value, "int")
		if err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE works SET ser_no=$1, updated_at=now() WHERE id=$2`, serNo, workID)
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
	case "ser_no":
		var o struct {
			SerNo *int `json:"ser_no"`
		}
		if err := json.Unmarshal(original, &o); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE works SET ser_no=$1, updated_at=now() WHERE id=$2`, o.SerNo, workID)
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

// normalizeBookValue — нормализация перед записью в колонку. lang → lower+trim
// (зеркало importer.normalizeLang; региональный субтег фронт не присылает).
func normalizeBookValue(column string, v any) any {
	if column == "lang" {
		if s, ok := v.(string); ok {
			return strings.ToLower(strings.TrimSpace(s))
		}
	}
	return v
}

// resyncBookWork — детачнутый ресинк works-индекса работы книги (после правки
// индексируемого edition-поля: lang → меняется lang[] работы).
func (c *OverrideController) resyncBookWork(ctx context.Context, bookID int64) {
	var workID *int64
	if err := c.pool.QueryRow(ctx, `SELECT work_id FROM books WHERE id=$1`, bookID).Scan(&workID); err != nil {
		c.logger.Warn("override: lookup work_id for resync failed", "book", bookID, "err", err)
		return
	}
	if workID != nil {
		c.syncWorkIndex(*workID)
	}
}

// ReapplyAfterImport ре-применяет book-уровневые оверрайды полей, которые импорт
// ПЕРЕЗАПИСЫВАЕТ (lang). Зовётся из main ПОСЛЕ imp.Run: обновляет original_value на
// свежеимпортированное значение (чтобы откат вернул именно его), затем заново
// материализует оверрайд. В конце — таргетный ресинк works-индекса затронутых работ.
func (c *OverrideController) ReapplyAfterImport(ctx context.Context) (int, error) {
	type item struct {
		bookID int64
		value  json.RawMessage
	}
	rows, err := c.pool.Query(ctx,
		`SELECT target_id, override_value FROM metadata_overrides WHERE target_kind='book' AND field='lang'`)
	if err != nil {
		return 0, err
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.bookID, &it.value); err != nil {
			rows.Close()
			return 0, err
		}
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	works := map[int64]struct{}{}
	for _, it := range items {
		newVal, derr := decodeScalar(it.value, "text")
		if derr != nil {
			continue
		}
		newVal = normalizeBookValue("lang", newVal)
		// original ← свежеимпортированный lang (до ре-материализации оверрайда).
		if _, err := c.pool.Exec(ctx, `
			UPDATE metadata_overrides
			SET original_value = jsonb_build_object('v', (SELECT lang FROM books WHERE id=$1))
			WHERE target_kind='book' AND target_id=$1 AND field='lang'
		`, it.bookID); err != nil {
			c.logger.Warn("reapply lang: refresh original failed", "book", it.bookID, "err", err)
			continue
		}
		if _, err := c.pool.Exec(ctx, `UPDATE books SET lang=$1, updated_at=now() WHERE id=$2`, newVal, it.bookID); err != nil {
			c.logger.Warn("reapply lang: materialize failed", "book", it.bookID, "err", err)
			continue
		}
		var workID *int64
		if c.pool.QueryRow(ctx, `SELECT work_id FROM books WHERE id=$1`, it.bookID).Scan(&workID) == nil && workID != nil {
			works[*workID] = struct{}{}
		}
	}
	total := len(items)

	// work-уровневые genres: импорт (replaceBookGenres) перетирает book_genres
	// изданий → ре-применяем оверрайд набора жанров на все живые издания работы.
	grows, err := c.pool.Query(ctx,
		`SELECT target_id, override_value FROM metadata_overrides WHERE target_kind='work' AND field='genres'`)
	if err != nil {
		return total, err
	}
	type gItem struct {
		workID int64
		value  json.RawMessage
	}
	var gItems []gItem
	for grows.Next() {
		var it gItem
		if err := grows.Scan(&it.workID, &it.value); err != nil {
			grows.Close()
			return total, err
		}
		gItems = append(gItems, it)
	}
	grows.Close()
	if err := grows.Err(); err != nil {
		return total, err
	}
	for _, it := range gItems {
		var v struct {
			Codes []string `json:"codes"`
		}
		if json.Unmarshal(it.value, &v) != nil {
			continue
		}
		// original ← свежий per-edition снапшот (после импорта), затем ре-материализуем.
		snap, serr := captureWorkGenres(ctx, c.pool, it.workID)
		if errors.Is(serr, pgx.ErrNoRows) {
			continue // работа без живых изданий
		}
		if serr != nil {
			c.logger.Warn("reapply genres: snapshot failed", "work", it.workID, "err", serr)
			continue
		}
		if _, err := c.pool.Exec(ctx,
			`UPDATE metadata_overrides SET original_value=$2 WHERE target_kind='work' AND target_id=$1 AND field='genres'`,
			it.workID, snap); err != nil {
			c.logger.Warn("reapply genres: refresh original failed", "work", it.workID, "err", err)
			continue
		}
		if err := materializeWorkGenres(ctx, c.pool, it.workID, v.Codes); err != nil {
			c.logger.Warn("reapply genres: materialize failed", "work", it.workID, "err", err)
			continue
		}
		works[it.workID] = struct{}{}
		total++
	}

	// work-уровневые authors: импорт (replaceBookAuthors) перетирает book_authors.
	arows, err := c.pool.Query(ctx,
		`SELECT target_id, override_value FROM metadata_overrides WHERE target_kind='work' AND field='authors'`)
	if err != nil {
		return total, err
	}
	type aItem struct {
		workID int64
		value  json.RawMessage
	}
	var aItems []aItem
	for arows.Next() {
		var it aItem
		if err := arows.Scan(&it.workID, &it.value); err != nil {
			arows.Close()
			return total, err
		}
		aItems = append(aItems, it)
	}
	arows.Close()
	if err := arows.Err(); err != nil {
		return total, err
	}
	for _, it := range aItems {
		var v struct {
			AuthorIDs []int64 `json:"author_ids"`
		}
		if json.Unmarshal(it.value, &v) != nil {
			continue
		}
		snap, serr := captureWorkAuthors(ctx, c.pool, it.workID)
		if errors.Is(serr, pgx.ErrNoRows) {
			continue
		}
		if serr != nil {
			c.logger.Warn("reapply authors: snapshot failed", "work", it.workID, "err", serr)
			continue
		}
		if _, err := c.pool.Exec(ctx,
			`UPDATE metadata_overrides SET original_value=$2 WHERE target_kind='work' AND target_id=$1 AND field='authors'`,
			it.workID, snap); err != nil {
			c.logger.Warn("reapply authors: refresh original failed", "work", it.workID, "err", err)
			continue
		}
		if err := materializeWorkAuthors(ctx, c.pool, it.workID, v.AuthorIDs); err != nil {
			c.logger.Warn("reapply authors: materialize failed", "work", it.workID, "err", err)
			continue
		}
		works[it.workID] = struct{}{}
		total++
	}

	// work-уровневые series: импорт перетирает books.series_id/ser_no изданий.
	srows, err := c.pool.Query(ctx,
		`SELECT target_id, override_value FROM metadata_overrides WHERE target_kind='work' AND field='series'`)
	if err != nil {
		return total, err
	}
	type sItem struct {
		workID int64
		value  json.RawMessage
	}
	var sItems []sItem
	for srows.Next() {
		var it sItem
		if err := srows.Scan(&it.workID, &it.value); err != nil {
			srows.Close()
			return total, err
		}
		sItems = append(sItems, it)
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		return total, err
	}
	for _, it := range sItems {
		var v struct {
			SeriesID *int64 `json:"series_id"`
			SerNo    *int   `json:"ser_no"`
		}
		if json.Unmarshal(it.value, &v) != nil {
			continue
		}
		snap, serr := captureWorkSeries(ctx, c.pool, it.workID)
		if errors.Is(serr, pgx.ErrNoRows) {
			continue
		}
		if serr != nil {
			c.logger.Warn("reapply series: snapshot failed", "work", it.workID, "err", serr)
			continue
		}
		if _, err := c.pool.Exec(ctx,
			`UPDATE metadata_overrides SET original_value=$2 WHERE target_kind='work' AND target_id=$1 AND field='series'`,
			it.workID, snap); err != nil {
			c.logger.Warn("reapply series: refresh original failed", "work", it.workID, "err", err)
			continue
		}
		if err := materializeWorkSeries(ctx, c.pool, it.workID, v.SeriesID, v.SerNo); err != nil {
			c.logger.Warn("reapply series: materialize failed", "work", it.workID, "err", err)
			continue
		}
		works[it.workID] = struct{}{}
		total++
	}

	if len(works) > 0 && c.resyncer != nil {
		ids := make([]int64, 0, len(works))
		for w := range works {
			ids = append(ids, w)
		}
		if err := c.resyncer.UpsertWorksToIndex(ctx, ids); err != nil {
			c.logger.Warn("reapply overrides: works index resync failed", "err", err)
		}
	}
	return total, nil
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
