package metadata

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// queryRower — минимальный QueryRow (удовлетворяют и *pgxpool.Pool, и pgx.Tx).
type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// dominantLang возвращает самый частый нормализованный код языка по живым
// книгам — «язык библиотеки». Пусто, если книг с языком нет. lower(btrim(...))
// зеркалит нормализацию импорта (грабля №14), чтобы 'RU'/'ru-RU' не дробились.
func dominantLang(ctx context.Context, ex queryRower) (string, error) {
	var lang string
	err := ex.QueryRow(ctx, `
		SELECT lower(btrim(lang)) AS l
		FROM books
		WHERE deleted = false AND lang IS NOT NULL AND btrim(lang) <> ''
		GROUP BY l
		ORDER BY count(*) DESC, l
		LIMIT 1
	`).Scan(&lang)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return lang, err
}

// recomputeWorkTitles переписывает works.title/normalized_title на заголовок
// издания работы в языке domLang (предпочитая обложку → edition_year → id) —
// ТОЛЬКО для работ, у которых такое издание ЕСТЬ; остальные не трогаются.
//
// Зачем: при слиянии «перевод + оригинал» каноникой могло стать иноязычное
// издание, и works.title оставался, например, английским («Another Fine Myth»),
// хотя в библиотеке книга известна по русскому переводу. Карточка (COALESCE(
// w.title, b.title)) и works-индекс брали этот английский заголовок → рассинхрон
// со списком (b.title представителя) и провал поиска по русскому названию.
//
// ids == nil → все работы. Возвращает id работ, чьё название реально изменилось
// (для таргетного ресинка works-индекса). Идемпотентно (UPDATE ... IS DISTINCT).
func recomputeWorkTitles(ctx context.Context, ex interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}, domLang string, ids []int64) ([]int64, error) {
	if domLang == "" {
		return nil, nil
	}
	// pick: на каждую работу — лучшее издание в domLang (DISTINCT ON по work_id).
	// Фильтр lang = domLang в CTE ⇒ работы без издания в этом языке в pick не
	// попадают и UPDATE их не трогает (сохраняют текущее каноническое название —
	// не ломаем работы, у которых перевода на язык библиотеки просто нет).
	// $2::bigint[] IS NULL (nil-slice от pgx) → без фильтра по ids = все работы.
	const q = `
		WITH pick AS (
			SELECT DISTINCT ON (b.work_id)
			       b.work_id AS wid, b.title AS title, b.normalized_title AS ntitle
			FROM books b
			WHERE b.deleted = false AND b.work_id IS NOT NULL
			  AND lower(btrim(b.lang)) = $1
			  AND ($2::bigint[] IS NULL OR b.work_id = ANY($2))
			ORDER BY b.work_id,
			         (b.cover_path IS NOT NULL AND b.cover_path <> '') DESC,
			         b.edition_year DESC NULLS LAST,
			         b.id
		)
		UPDATE works w
		SET title = pick.title, normalized_title = pick.ntitle, updated_at = now()
		FROM pick
		WHERE w.id = pick.wid
		  AND (w.title IS DISTINCT FROM pick.title
		       OR w.normalized_title IS DISTINCT FROM pick.ntitle)
		  -- Не перетираем ручной оверрайд названия (грабля №19, metadata/overrides.go).
		  AND NOT EXISTS (SELECT 1 FROM metadata_overrides o
		                  WHERE o.target_kind='work' AND o.target_id=w.id AND o.field='title')
		RETURNING w.id
	`
	return scanInt64s(ctx, ex, q, domLang, ids)
}

// LocalizeWorkTitles — разовый backfill: вычисляет доминирующий язык библиотеки
// и переписывает works.title на него для всех работ, у которых есть издание в
// этом языке. Возвращает id изменённых работ (для ресинка works-индекса) и сам
// язык. Идемпотентно. На будущее тот же пересчёт делает фоновая группировка
// (см. WorkGrouper.apply) для затронутых работ.
func LocalizeWorkTitles(ctx context.Context, pool *pgxpool.Pool) ([]int64, string, error) {
	dom, err := dominantLang(ctx, pool)
	if err != nil {
		return nil, "", err
	}
	if dom == "" {
		return nil, "", nil
	}
	changed, err := recomputeWorkTitles(ctx, pool, dom, nil)
	return changed, dom, err
}
